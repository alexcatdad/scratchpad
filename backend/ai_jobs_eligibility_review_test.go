package backend_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJobActivityIncludesEnabledFutureWorkAndRunningAttemptsWithoutProviderCalls(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			t.Setenv("SCRATCHPAD_RAG_ENABLED", "true")
			path, databaseURL := portableInstance(t, engine)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for id, typ := range map[string]string{"job_future": "embed", "job_unavailable": "rag_index", "job_unknown": "unknown"} {
				job := map[string]any{"id": id, "type": typ, "status": "queued", "createdAt": "2026-10-01T00:00Z", "runAfter": "9999-01-01T00:00:00Z", "fingerprint": "unavailable-generation", "processingVersion": "passage-v1"}
				raw, _ := json.Marshal(job)
				if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_job',$1,$2,1,'2026-10-01T00:00Z')`, id, string(raw)); err != nil {
					t.Fatal(err)
				}
			}
			origin := startServer(t, path, databaseURL)
			check := func(expected bool) {
				t.Helper()
				status, result := curationRequest(t, origin, "GET", "/ai/jobs", nil)
				if status != 200 || result["hasActive"] != expected {
					t.Fatalf("unexpected queued/running activity: HTTP%d %+v", status, result)
				}
			}
			check(false)
			if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_job','job_running','{"id":"job_running","type":"embed","status":"running","leaseUntil":"9999-01-01T00:00:00Z"}',1,'2026-10-01T00:00Z')`); err != nil {
				t.Fatal(err)
			}
			check(true)
			completed := `{"id":"job_running","type":"embed","status":"completed"}`
			if _, err = db.Exec(`UPDATE entities SET data=$1 WHERE kind='ai_job' AND id='job_running'`, completed); err != nil {
				t.Fatal(err)
			}
			check(false)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": "http://127.0.0.1:1/v1"})
			if status != 200 {
				t.Fatal("enabled future queue fixture failed")
			}
			check(true)
			completed = `{"id":"job_future","type":"embed","status":"completed"}`
			if _, err = db.Exec(`UPDATE entities SET data=$1 WHERE kind='ai_job' AND id='job_future'`, completed); err != nil {
				t.Fatal(err)
			}
			check(false)
			if len(allAIJobs(t, origin)) != 4 {
				t.Fatal("activity filtering deleted retained history")
			}
		})
	}
}

func TestDormantRAGJobsDoNotKeepPollingActiveAndResumeWithCompatibleSettings(t *testing.T) {
	path, databaseURL := ragInstance(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input []string `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "invalid", 400)
			return
		}
		data := []any{}
		for i := range request.Input {
			data = append(data, map[string]any{"index": i, "embedding": []int{1, 0}})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingModel": "historical-generation", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("provider fixture failed")
	}
	project := jobProject(t, origin, "Dormant indexing", true, false)
	curationCapture(t, origin, project, "Preserve the original source and generation")
	old := waitRAGJobs(t, origin, 1)[0].(map[string]any)
	status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "enabled": true, "embeddingModel": "current-generation"})
	if status != 200 {
		t.Fatal("generation change failed")
	}
	jobs := waitRAGJobs(t, origin, 2)
	var currentID string
	for _, item := range jobs {
		job := item.(map[string]any)
		if job["fingerprint"] != old["fingerprint"] {
			currentID = job["id"].(string)
		}
	}
	if currentID == "" {
		t.Fatal("current source generation was not queued")
	}
	waitAIJob(t, origin, currentID, "completed")
	status, result := curationRequest(t, origin, "GET", "/ai/jobs?jobId="+old["id"].(string), nil)
	if status != 200 || result["hasActive"] != false || len(result["jobs"].([]any)) != 1 {
		t.Fatalf("dormant generation kept polling active: HTTP%d %+v", status, result)
	}
	retained := result["jobs"].([]any)[0].(map[string]any)
	if retained["status"] != "queued" || retained["fingerprint"] != old["fingerprint"] || retained["attempts"] != float64(0) {
		t.Fatal("dormant generation provenance was rewritten or dispatched")
	}
	status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 2, "embeddingModel": "historical-generation"})
	if status != 200 {
		t.Fatal("compatible settings restoration failed")
	}
	resumed := waitAIJob(t, origin, old["id"].(string), "completed")
	if resumed["fingerprint"] != old["fingerprint"] || len(allAIJobs(t, origin)) != 2 {
		t.Fatal("restoration replaced retained generation instead of resuming it")
	}
}
