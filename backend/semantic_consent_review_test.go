package backend_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestAllProjectSemanticSearchRejectsConsentWithdrawnDuringProviderWait(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Input []string `json:"input"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Input) != 1 {
					http.Error(w, "invalid", 400)
					return
				}
				if body.Input[0] == "BLOCK_SCOPE_CONSENT" {
					close(started)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []int{1, 0}}}})
			}))
			defer func() { unblock(); provider.Close() }()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
			if status != 200 {
				t.Fatal("settings failed")
			}
			withdrawn := jobProject(t, origin, "Withdrawn participant", true, true)
			retained := jobProject(t, origin, "Retained participant", true, true)
			retainedRecord := ""
			for _, project := range []string{withdrawn, retained} {
				record := curationCapture(t, origin, project, "Independent semantic source")
				if project == retained {
					retainedRecord = record["id"].(string)
				}
				status, queued := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "embed", "projectId": project})
				if status != 202 {
					t.Fatal("enqueue failed")
				}
				waitAIJob(t, origin, queued["job"].(map[string]any)["id"].(string), "completed")
			}
			status, result := curationRequest(t, origin, "POST", "/search/semantic", map[string]any{"crossProject": true, "query": "Both eligible sources"})
			if status != 200 || len(result["results"].([]any)) != 2 {
				t.Fatal("initial all-project index was not eligible")
			}
			type response struct {
				status int
				body   map[string]any
			}
			done := make(chan response, 1)
			go func() {
				status, body := curationRequest(t, origin, "POST", "/search/semantic", map[string]any{"crossProject": true, "query": "BLOCK_SCOPE_CONSENT"})
				done <- response{status, body}
			}()
			select {
			case <-started:
			case <-time.After(4 * time.Second):
				t.Fatal("query did not dispatch")
			}
			status, project := projectRequest(t, origin, "GET", "/"+withdrawn, nil, restoredToken)
			if status != 200 {
				t.Fatal("project lookup failed")
			}
			current := project["project"].(map[string]any)
			settings := current["settings"].(map[string]any)
			settings["aiProcessing"] = false
			status, _ = projectRequest(t, origin, "PATCH", "/"+withdrawn+"/settings", map[string]any{"expectedVersion": current["version"], "settings": settings}, restoredToken)
			if status != 200 {
				t.Fatal("provider latency blocked consent withdrawal")
			}
			unblock()
			select {
			case completed := <-done:
				if completed.status != 403 || completed.body["error"].(map[string]any)["code"] != "AI_NOT_ALLOWED" {
					t.Fatalf("withdrawn project evidence leaked from admitted snapshot: HTTP%d %v", completed.status, completed.body)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("query did not finish after release")
			}
			status, result = curationRequest(t, origin, "POST", "/search/semantic", map[string]any{"crossProject": true, "query": "Fresh permitted scope"})
			if status != 200 || len(result["results"].([]any)) != 1 || result["results"].([]any)[0].(map[string]any)["record"].(map[string]any)["id"] != retainedRecord {
				t.Fatal("fresh all-project query lost retained evidence or included withdrawn source")
			}
		})
	}
}
