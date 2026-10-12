package backend_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmbeddingWorkerPreservesUnicodeAcrossLongRecordChunks(t *testing.T) {
	var fox atomic.Bool
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Input) != 1 {
			http.Error(w, "invalid input", 400)
			return
		}
		calls.Add(1)
		if strings.Contains(body.Input[0], "🦊") {
			fox.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer provider.Close()
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("provider settings failed")
	}
	projectID := jobProject(t, origin, "Long Unicode history", true, false)
	status, _ = captureRequest(t, origin, projectID, "", map[string]any{"type": "decision", "title": "Unicode boundary", "authority": "explicit", "payload": map[string]any{"decision": strings.Repeat("x", 5989) + "🦊tail"}})
	if status != 201 {
		t.Fatal("long capture failed")
	}
	status, response := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "embed", "projectId": projectID})
	if status != 202 {
		t.Fatal("enqueue failed")
	}
	waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "completed")
	if calls.Load() != 2 || !fox.Load() {
		t.Fatal("long-record splitting lost the Unicode evidence at its boundary")
	}
}

func TestEmbeddingWorkerDiscardsResultsAfterConsentRevocation(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
			}))
			defer func() { unblock(); provider.Close() }()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
			if status != 200 {
				t.Fatal("provider settings failed")
			}
			projectID := jobProject(t, origin, "Consent during processing", true, false)
			curationCapture(t, origin, projectID, "Consent is required")
			status, response := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "embed", "projectId": projectID})
			if status != 202 {
				t.Fatal("enqueue failed")
			}
			select {
			case <-started:
			case <-time.After(4 * time.Second):
				t.Fatal("worker did not call provider")
			}
			status, project := projectRequest(t, origin, "GET", "/"+projectID, nil, restoredToken)
			if status != 200 {
				t.Fatal("project missing")
			}
			settings := project["project"].(map[string]any)["settings"].(map[string]any)
			settings["aiProcessing"] = false
			status, _ = projectRequest(t, origin, "PATCH", "/"+projectID+"/settings", map[string]any{"expectedVersion": 2, "settings": settings}, restoredToken)
			if status != 200 {
				t.Fatal("provider wait blocked consent revocation")
			}
			unblock()
			waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "failed")
			status, _ = curationRequest(t, origin, "POST", "/search/semantic", map[string]any{"projectId": projectID, "query": "consent"})
			if status != 403 {
				t.Fatal("revoked consent allowed query processing")
			}
			settings["aiProcessing"] = true
			status, _ = projectRequest(t, origin, "PATCH", "/"+projectID+"/settings", map[string]any{"expectedVersion": 3, "settings": settings}, restoredToken)
			if status != 200 {
				t.Fatal("consent restoration failed")
			}
			status, response = curationRequest(t, origin, "POST", "/search/semantic", map[string]any{"projectId": projectID, "query": "consent"})
			if status != 200 || response["indexRequired"] != true || len(response["results"].([]any)) != 0 {
				t.Fatal("revoked in-flight output was activated")
			}
		})
	}
}

func TestEmbeddingWorkerRecoversExpiredLeaseAcrossCompetingServers(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
			}))
			defer provider.Close()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
			if status != 200 {
				t.Fatal("provider settings failed")
			}
			projectID := jobProject(t, origin, "Recovered lease", true, false)
			curationCapture(t, origin, projectID, "Preserve abandoned work")
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			job := map[string]any{"id": "job_abandoned", "version": 2, "createdAt": "2026-10-01T00:00:00Z", "type": "embed", "status": "running", "attempts": 1, "leaseToken": "synthetic-abandoned-lease", "leaseUntil": "2026-10-01T00:00:00Z", "scope": map[string]any{"projectIds": []string{projectID}, "crossProject": false}}
			raw, _ := json.Marshal(job)
			_, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_job','job_abandoned',$1,2,'2026-10-01T00:00:00Z')`, string(raw))
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			second := startServer(t, path, databaseURL)
			completed := waitAIJob(t, second, "job_abandoned", "completed")
			if completed["attempts"] != float64(2) || completed["sourceCount"] != float64(1) || completed["leaseToken"] != nil || completed["leaseUntil"] != nil {
				t.Fatal("abandoned lease was not replaced and completed")
			}
			time.Sleep(1200 * time.Millisecond)
			if calls.Load() != 1 {
				t.Fatalf("competing workers processed the source %d times", calls.Load())
			}
		})
	}
}

func waitAIJob(t *testing.T, origin, id, state string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for {
		status, result := curationRequest(t, origin, "GET", "/ai/jobs", nil)
		if status != 200 {
			t.Fatal("job listing failed")
		}
		for _, item := range result["jobs"].([]any) {
			job := item.(map[string]any)
			if job["id"] == id {
				if job["status"] == state {
					return job
				}
				if job["status"] == "failed" || time.Now().After(deadline) {
					t.Fatalf("job did not reach %s: %+v", state, job)
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("job disappeared")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestEmbeddingWorkerMakesSemanticResultsDurable(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Input []string `json:"input"`
				}
				if r.URL.Path != "/v1/embeddings" || json.NewDecoder(r.Body).Decode(&request) != nil {
					http.Error(w, "unexpected request", 400)
					return
				}
				data := []any{}
				for index := range request.Input {
					data = append(data, map[string]any{"index": index, "embedding": []float64{1, 0}})
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"data": data})
			}))
			defer provider.Close()
			path, databaseURL := portableInstance(t, engine)
			var projectID, recordID string
			for _, phase := range []string{"execute", "restart"} {
				t.Run(phase, func(t *testing.T) {
					origin := startServer(t, path, databaseURL)
					if phase == "execute" {
						status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
						if status != 200 {
							t.Fatal("provider settings failed")
						}
						projectID = jobProject(t, origin, "Worker semantic history", true, false)
						recordID = curationCapture(t, origin, projectID, "Preserve durable memory")["id"].(string)
						status, response := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "embed", "projectId": projectID})
						if status != 202 {
							t.Fatal("job enqueue failed")
						}
						job := waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "completed")
						if job["sourceCount"] != float64(1) || job["attempts"] != float64(1) {
							t.Fatal("worker completion omitted processed source")
						}
					}
					status, response := curationRequest(t, origin, "POST", "/search/semantic", map[string]any{"projectId": projectID, "query": "Keep knowledge after restart"})
					if status != 200 {
						t.Fatalf("semantic search failed: %d %+v", status, response)
					}
					results := response["results"].([]any)
					if len(results) != 1 || results[0].(map[string]any)["record"].(map[string]any)["id"] != recordID || results[0].(map[string]any)["score"] != float64(1) {
						t.Fatal("completed worker result missing from semantic search")
					}
				})
			}
		})
	}
}
