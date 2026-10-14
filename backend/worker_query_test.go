package backend_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAISchedulingFindsValidOverlappingScopePastMalformedHistoricalJobs(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		for _, aliases := range []string{"case", "long-s", "conflicting"} {
			t.Run(engine+"/"+aliases, func(t *testing.T) {
				path, databaseURL := portableInstance(t, engine)
				origin := startServer(t, path, databaseURL)
				first := jobProject(t, origin, "Scoped scheduler first", true, true)
				second := jobProject(t, origin, "Scoped scheduler second", true, true)
				driver, dsn := "sqlite", path
				if engine == "postgres" {
					driver, dsn = "pgx", databaseURL
				}
				db, err := sql.Open(driver, dsn)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				// Historical operational input includes extension fields and malformed scopes.
				// Public listing and scheduling must retain history and skip unusable scopes.
				for i := 0; i < 146; i++ {
					id := fmt.Sprintf("job_scope_%03d", i)
					status := "completed"
					scope := map[string]any{"projectIds": []any{first, second}, "crossProject": true}
					if i >= 80 {
						status = "queued"
						scope["projectIds"] = []any{first, second, 7}
					}
					if i == 145 {
						scope = map[string]any{"projectIdſ": []any{first, second}, "croſſProject": true}
					}
					var admittedScope any = scope
					if i == 145 {
						switch aliases {
						case "case":
							admittedScope = map[string]any{"ProjectIds": []any{first, second}, "CrossProject": true}
						case "conflicting":
							admittedScope = json.RawMessage(fmt.Sprintf("{\"projectIds\":[%q,%q],\"ProjectIds\":[\"proj_unrelated_alias\"],\"crossProject\":true,\"CrossProject\":false}", first, second))
						}
					}
					job := map[string]any{"id": id, "version": 1, "createdAt": "2026-10-01T00:00:00Z", "type": "analyze", "status": status, "attempts": 0, "runAfter": "2099-01-01T00:00:00Z", "scope": admittedScope, "historicalExtension": strings.Repeat("preserve ", 512)}
					raw, _ := json.Marshal(job)
					if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_job',$1,$2,1,'2026-10-01T00:00:00Z')`, id, string(raw)); err != nil {
						t.Fatal(err)
					}
				}
				status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 1})
				if status != 200 {
					t.Fatal("scheduler configuration failed")
				}
				jobs := scheduledJobs(t, origin, 148)
				embeds := map[string]bool{}
				for _, value := range jobs {
					job := value.(map[string]any)
					if strings.HasPrefix(job["id"].(string), "job_scope_") {
						if job["historicalExtension"] != strings.Repeat("preserve ", 512) {
							t.Fatal("job listing dropped historical extensions")
						}
						continue
					}
					if job["type"] != "embed" {
						t.Fatal("malformed candidates concealed valid overlapping analysis work")
					}
					scope := job["scope"].(map[string]any)
					ids := scope["projectIds"].([]any)
					if len(ids) != 1 || scope["crossProject"] != false {
						t.Fatal("scheduled work escaped its admitted scope")
					}
					embeds[ids[0].(string)] = true
				}
				if !embeds[first] || !embeds[second] {
					t.Fatal("valid active analysis suppressed unrelated embedding work")
				}
			})
		}
	}
}

func TestAIEmbeddingLoadsOnlyAdmittedIndexedProjectSources(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Input []string `json:"input"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Input) != 1 || !strings.Contains(body.Input[0], "ONLY_ADMITTED_WITNESS") || strings.Contains(body.Input[0], "OUTSIDE_WITNESS") {
					http.Error(w, "scope violation", 400)
					return
				}
				w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
			}))
			defer provider.Close()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			selected := jobProject(t, origin, "Admitted embedding", true, false)
			outside := jobProject(t, origin, "Unselected embedding", true, false)
			private := jobProject(t, origin, "Forbidden embedding", false, false)
			curationCapture(t, origin, selected, "ONLY_ADMITTED_WITNESS")
			for i := 0; i < 12; i++ {
				for _, id := range []string{outside, private} {
					status, _ := captureRequest(t, origin, id, "", map[string]any{"type": "finding", "title": "OUTSIDE_WITNESS", "authority": "observed", "payload": map[string]any{"finding": strings.Repeat("unrelated corpus ", 500)}})
					if status != 201 {
						t.Fatal("outside corpus fixture failed")
					}
				}
			}
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
			if status != 200 {
				t.Fatal("embedding fixture failed")
			}
			status, result := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "embed", "projectId": selected})
			if status != 202 {
				t.Fatal("scoped embedding enqueue failed")
			}
			completed := waitAIJob(t, origin, result["job"].(map[string]any)["id"].(string), "completed")
			if completed["sourceCount"] != float64(1) {
				t.Fatal("embedding processed records outside the selected project")
			}
		})
	}
}
