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

func TestAISettingsRejectDocumentExportAnalysisModeAtomically(t *testing.T) {
	allowed := []any{"summary", "classification", "duplicate_candidate", "relationship_candidate", "contradiction", "cluster", "pattern", "recommendation"}
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, before := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0, "model": "retained-analysis-model", "analysisModes": allowed})
			if status != 200 {
				t.Fatal("eight analysis modes rejected")
			}
			for _, modes := range []any{[]any{"export"}, []any{"summary", "export"}} {
				status, result := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "enabled": true, "model": "must-not-be-saved", "analysisModes": modes})
				if status != 400 || result["error"].(map[string]any)["code"] != "VALIDATION_FAILED" {
					t.Fatalf("document export accepted as analysis mode: HTTP %d %+v", status, result)
				}
				status, after := curationRequest(t, origin, "GET", "/ai/settings", nil)
				if status != 200 || !reflect.DeepEqual(after, before) {
					t.Fatal("rejected analysis mode changed configuration or version")
				}
			}
			for i, mode := range allowed {
				status, result := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": i + 1, "analysisModes": []any{mode}})
				if status != 200 || !reflect.DeepEqual(result["analysisModes"], []any{mode}) {
					t.Fatalf("supported analysis mode %v rejected: HTTP %d %+v", mode, status, result)
				}
			}
		})
	}
}

func TestHistoricalExportModeDoesNotEnableAnalysisDocumentsOrBlockOrdinaryMemory(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var completions, embeddings atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/embeddings" {
					embeddings.Add(1)
					json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []int{1, 0}}}})
					return
				}
				completions.Add(1)
				var request struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) != 2 {
					http.Error(w, "invalid", 400)
					return
				}
				var input map[string]any
				if json.Unmarshal([]byte(request.Messages[1].Content), &input) != nil {
					http.Error(w, "invalid", 400)
					return
				}
				id := input["records"].([]any)[0].(map[string]any)["id"]
				output, _ := json.Marshal(map[string]any{"artifacts": []any{map[string]any{"kind": "export", "title": "Explicit private document", "content": map[string]any{"text": "Retain cited source evidence", "markdown": "## Explicitly requested document"}, "sourceRecordIds": []any{id}}}})
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(output)}}}})
			}))
			defer provider.Close()
			path, databaseURL := portableInstance(t, engine)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{"id": "global", "version": 7, "createdAt": "2026-10-01T00:00:00Z", "enabled": true, "baseUrl": provider.URL + "/v1", "model": "historical-completion", "embeddingModel": "historical-embedding", "embeddingDimensions": 2, "scheduleMinutes": 0, "analysisModes": []any{"export"}, "dispatchGeneration": 1})
			_, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_settings','global',$1,7,'2026-10-01T00:00:00Z')`, string(raw))
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			origin := startServer(t, path, databaseURL)
			status, settings := curationRequest(t, origin, "GET", "/ai/settings", nil)
			if status != 200 || len(settings["analysisModes"].([]any)) != 0 || settings["version"] != float64(7) {
				t.Fatalf("historical document mode blocks or enables effective analysis: HTTP %d %+v", status, settings)
			}
			ready, err := http.Get(origin + "/ready")
			if err != nil {
				t.Fatal(err)
			}
			ready.Body.Close()
			if ready.StatusCode != 200 {
				t.Fatal("historical analysis mode blocked core readiness")
			}
			project := jobProject(t, origin, "Historical separate documents", true, false)
			record := curationCapture(t, origin, project, "Keep ordinary memory and optional execution separate")
			status, result := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "analyze", "projectId": project})
			if status != 202 {
				t.Fatal("historical analysis enqueue failed")
			}
			job := waitAIJob(t, origin, result["job"].(map[string]any)["id"].(string), "completed")
			if len(job["artifactIds"].([]any)) != 0 || completions.Load() != 0 || embeddings.Load() != 0 {
				t.Fatal("historical export mode caused implicit provider/document execution")
			}
			status, result = curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "embed", "projectId": project})
			if status != 202 {
				t.Fatal("historical settings blocked embedding enqueue")
			}
			waitAIJob(t, origin, result["job"].(map[string]any)["id"].(string), "completed")
			status, result = curationRequest(t, origin, "POST", "/summaries/export", map[string]any{"projectId": project, "format": "handoff"})
			if status != 202 {
				t.Fatal("explicit document enqueue failed")
			}
			job = waitAIJob(t, origin, result["job"].(map[string]any)["id"].(string), "completed")
			if len(job["artifactIds"].([]any)) != 1 || completions.Load() != 1 || embeddings.Load() != 1 {
				t.Fatal("explicit embedding/document execution was blocked or duplicated")
			}
			status, detail := curationRequest(t, origin, "GET", "/records/"+record["id"].(string), nil)
			if status != 200 || !reflect.DeepEqual(detail["record"], record) {
				t.Fatal("historical settings changed immutable memory")
			}
			status, retained := curationRequest(t, origin, "GET", "/ai/settings", nil)
			if status != 200 || !reflect.DeepEqual(retained, settings) {
				t.Fatal("startup/execution rewrote historical configuration or version")
			}
		})
	}
}
