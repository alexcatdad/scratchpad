package backend_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestFailedPassageRetryRejectsUnavailableOrChangedGenerationWithoutRewritingHistory(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			t.Setenv("SCRATCHPAD_RAG_ENABLED", "true")
			var path, databaseURL string
			if engine == "postgres" {
				path, databaseURL = ragInstance(t)
			} else {
				path, databaseURL = portableInstance(t, engine)
			}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Input []string `json:"input"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					http.Error(w, "invalid", 400)
					return
				}
				data := []any{}
				for i := range body.Input {
					data = append(data, map[string]any{"index": i, "embedding": []int{1, 0}})
				}
				json.NewEncoder(w).Encode(map[string]any{"data": data})
			}))
			defer provider.Close()
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingModel": "historical-retry", "embeddingDimensions": 2})
			if status != 200 {
				t.Fatal("configuration failed")
			}
			project := jobProject(t, origin, "Preserved retry generation", true, false)
			record := curationCapture(t, origin, project, "Source preserved across retry")
			job := map[string]any{"id": "job_unavailable", "type": "rag_index", "scope": map[string]any{"projectIds": []any{project}, "crossProject": false}, "recordId": record["id"], "fingerprint": "unavailable", "processingVersion": "passage-v1", "createdAt": "2026-10-01T00:00Z"}
			if engine == "postgres" {
				job = waitRAGJobs(t, origin, 1)[0].(map[string]any)
			}
			job["status"], job["version"], job["attempts"], job["lastError"] = "failed", 3, 3, "Synthetic retained failure"
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			raw, _ := json.Marshal(job)
			if engine == "postgres" {
				_, err = db.Exec(`UPDATE entities SET data=$1,version=3 WHERE kind='ai_job' AND id=$2`, string(raw), job["id"])
			} else {
				_, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_job',$1,$2,3,$3)`, job["id"], string(raw), job["createdAt"])
			}
			if err != nil {
				t.Fatal(err)
			}
			id := job["id"].(string)
			status, before := curationRequest(t, origin, "GET", "/ai/jobs?jobId="+id, nil)
			if status != 200 {
				t.Fatal("failed history unavailable")
			}
			status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "enabled": true, "embeddingModel": "current-retry"})
			if status != 200 {
				t.Fatal("generation change failed")
			}
			status, _ = curationRequest(t, origin, "POST", "/ai/jobs/"+id+"/retry", map[string]any{"expectedVersion": 3})
			if status != 409 {
				t.Fatalf("unclaimable retry accepted: HTTP%d", status)
			}
			status, after := curationRequest(t, origin, "GET", "/ai/jobs?jobId="+id, nil)
			if status != 200 || !reflect.DeepEqual(before["jobs"], after["jobs"]) {
				t.Fatal("rejected retry rewrote failed history")
			}
			if engine == "postgres" {
				status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 2, "embeddingModel": "historical-retry"})
				if status != 200 {
					t.Fatal("compatible restoration failed")
				}
				job["processingVersion"] = "retired-processor"
				raw, _ = json.Marshal(job)
				if _, err = db.Exec(`UPDATE entities SET data=$1 WHERE kind='ai_job' AND id=$2`, string(raw), id); err != nil {
					t.Fatal(err)
				}
				status, _ = curationRequest(t, origin, "POST", "/ai/jobs/"+id+"/retry", map[string]any{"expectedVersion": 3})
				if status != 409 {
					t.Fatal("retired processing version was silently activated")
				}
				job["processingVersion"] = "passage-v1"
				raw, _ = json.Marshal(job)
				if _, err = db.Exec(`UPDATE entities SET data=$1 WHERE kind='ai_job' AND id=$2`, string(raw), id); err != nil {
					t.Fatal(err)
				}
				status, retried := curationRequest(t, origin, "POST", "/ai/jobs/"+id+"/retry", map[string]any{"expectedVersion": 3})
				if status != 202 || retried["job"].(map[string]any)["id"] != id || retried["job"].(map[string]any)["fingerprint"] != job["fingerprint"] {
					t.Fatal("compatible retry did not preserve source generation")
				}
				waitAIJob(t, origin, id, "completed")
			}
		})
	}
}

func TestRequestedDocumentWithNoSourcesFailsClearlyWithoutCallingProvider(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "unexpected", 503) }))
			defer provider.Close()
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []any{}})
			if status != 200 {
				t.Fatal("configuration failed")
			}
			project := jobProject(t, origin, "Empty private document", true, false)
			status, queued := curationRequest(t, origin, "POST", "/summaries/export", map[string]any{"projectId": project, "format": "handoff"})
			if status != 202 {
				t.Fatal("document enqueue failed")
			}
			job := waitProviderReviewJob(t, origin, queued["job"].(map[string]any)["id"].(string))
			if job["status"] != "failed" || job["attempts"] != float64(1) || job["lastError"] != "No source records are available for this document." || calls.Load() != 0 {
				t.Fatalf("empty document was reported successful or retried: %+v, calls%d", job, calls.Load())
			}
			status, suggestions := curationRequest(t, origin, "GET", "/suggestions?projectId="+project, nil)
			if status != 200 || len(suggestions["suggestions"].([]any)) != 0 {
				t.Fatal("empty document invented an artifact")
			}
		})
	}
}
