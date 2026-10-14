package backend_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestSingletonRelationshipWarningPreservesValidCompletionSibling(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			provider := completionService(t, func(input map[string]any) any {
				id := input["records"].([]any)[0].(map[string]any)["id"]
				return map[string]any{"artifacts": []any{
					map[string]any{"kind": "relationship_candidate", "title": "Unsupported self relationship", "content": map[string]any{"text": "One record cannot establish a relationship", "fromRecordId": id, "toRecordId": id, "relationshipType": "supports"}, "sourceRecordIds": []any{id, id}},
					map[string]any{"kind": "summary", "title": "Valid sibling", "content": map[string]any{"text": "Preserve the supplied decision"}, "sourceRecordIds": []any{id}},
				}}
			})
			defer provider.Close()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []string{"relationship_candidate", "summary"}})
			if status != 200 {
				t.Fatal("settings failed")
			}
			project := jobProject(t, origin, "Relationship evidence", true, false)
			record := curationCapture(t, origin, project, "Immutable source")
			status, queued := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "analyze", "projectId": project})
			if status != 202 {
				t.Fatal("enqueue failed")
			}
			job := waitAIJob(t, origin, queued["job"].(map[string]any)["id"].(string), "completed")
			if job["attempts"] != float64(1) || !reflect.DeepEqual(job["warnings"], []any{map[string]any{"code": "AI_INSUFFICIENT_EVIDENCE", "kind": "relationship_candidate", "count": float64(1)}}) {
				t.Fatal("singleton relationship was not rejected with one evidence warning")
			}
			status, result := curationRequest(t, origin, "GET", "/suggestions?projectId="+project, nil)
			if status != 200 || len(result["suggestions"].([]any)) != 1 {
				t.Fatal("valid completion sibling was lost")
			}
			artifact := result["suggestions"].([]any)[0].(map[string]any)
			if artifact["kind"] != "summary" || artifact["status"] != "pending" || artifact["authority"] != "derived" || !reflect.DeepEqual(artifact["sourceRecordIds"], []any{record["id"]}) {
				t.Fatal("valid sibling lost source/privacy contract")
			}
		})
	}
}

func TestCompletionModelChangeRetainsOrdinarySemanticEmbeddings(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
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
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "model": "completion-before", "embeddingModel": "synthetic-embedding", "embeddingDimensions": 2})
			if status != 200 {
				t.Fatal("settings failed")
			}
			project := jobProject(t, origin, "Embedding compatibility", true, false)
			record := curationCapture(t, origin, project, "Durable semantic evidence")
			status, queued := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "embed", "projectId": project})
			if status != 202 {
				t.Fatal("enqueue failed")
			}
			waitAIJob(t, origin, queued["job"].(map[string]any)["id"].(string), "completed")
			check := func(want int) {
				status, result := curationRequest(t, origin, "POST", "/search/semantic", map[string]any{"projectId": project, "query": "Retain durable knowledge"})
				if status != 200 || len(result["results"].([]any)) != want {
					t.Fatalf("compatible index lost: HTTP%d %v", status, result)
				}
				if want == 1 && result["results"].([]any)[0].(map[string]any)["record"].(map[string]any)["id"] != record["id"] {
					t.Fatal("wrong source returned")
				}
			}
			check(1)
			status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "model": "completion-after"})
			if status != 200 {
				t.Fatal("completion update failed")
			}
			check(1)
			status, jobs := curationRequest(t, origin, "GET", "/ai/jobs", nil)
			if status != 200 || len(jobs["jobs"].([]any)) != 1 {
				t.Fatal("model-only change unexpectedly required re-embedding")
			}
			status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 2, "embeddingModel": "different-embedding"})
			if status != 200 {
				t.Fatal("embedding update failed")
			}
			check(0)
		})
	}
}
