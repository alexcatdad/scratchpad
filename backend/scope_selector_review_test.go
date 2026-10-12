package backend_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAIAndRetrievalRejectConflictingProjectSelectors(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
			}))
			defer provider.Close()
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
			if status != 200 {
				t.Fatal("provider fixture failed")
			}
			forbidden := jobProject(t, origin, "Forbidden single selector", false, false)
			allowed := jobProject(t, origin, "Allowed array selector", true, false)
			for _, route := range []string{"/ai/jobs", "/summaries/export", "/search/semantic", "/rag/index", "/rag/retrieve"} {
				t.Run(route, func(t *testing.T) {
					body := map[string]any{"projectId": forbidden, "projectIds": []string{allowed}, "type": "embed", "query": "synthetic scope evidence", "recordId": "rec_scope_fixture"}
					status, result := curationRequest(t, origin, "POST", route, body)
					if status != 400 || result["error"].(map[string]any)["code"] != "VALIDATION_FAILED" {
						t.Fatalf("conflicting scope selected one representation on %s: HTTP%d", route, status)
					}
				})
			}
			t.Run("suggestions", func(t *testing.T) {
				status, result := curationRequest(t, origin, "GET", "/suggestions?projectId="+forbidden+"&projectIds="+allowed, nil)
				if status != 400 || result["error"].(map[string]any)["code"] != "VALIDATION_FAILED" {
					t.Fatalf("suggestion scope ignored conflicting selector: HTTP%d", status)
				}
			})
			for caseIndex, selectors := range []string{"projectId=" + forbidden + "&projectIds=" + allowed, "projectId=&projectIds=" + allowed, "projectId=" + allowed + "&projectIds="} {
				t.Run(fmt.Sprintf("coverage_%d", caseIndex), func(t *testing.T) {
					status, result := curationRequest(t, origin, "GET", "/rag/coverage?"+selectors, nil)
					if status != 400 || result["error"].(map[string]any)["code"] != "VALIDATION_FAILED" {
						t.Fatalf("coverage accepted conflicting query selector presence: HTTP%d", status)
					}
				})
			}
			status, jobs := curationRequest(t, origin, "GET", "/ai/jobs", nil)
			if status != 200 || len(jobs["jobs"].([]any)) != 0 {
				t.Fatal("rejected ambiguous scope queued work")
			}
			for _, scope := range []map[string]any{{"projectId": allowed, "type": "embed"}, {"projectIds": []string{allowed}, "type": "embed"}} {
				status, _ := curationRequest(t, origin, "POST", "/ai/jobs", scope)
				if status != 202 {
					t.Fatal("unambiguous admitted project scope changed")
				}
			}
		})
	}
}
