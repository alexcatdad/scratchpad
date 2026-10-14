package backend_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func reviewCapture(t *testing.T, origin, key string, body map[string]any) (int, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(body)
	request, _ := http.NewRequest("POST", origin+"/api/v1/records", bytes.NewReader(data))
	request.Header.Set("Authorization", "Bearer "+restoredToken)
	request.Header.Set("Idempotency-Key", key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if json.NewDecoder(response.Body).Decode(&result) != nil {
		t.Fatal("capture returned non-JSON")
	}
	return response.StatusCode, result
}

func TestCaptureRetriesBindSubmittedGitContext(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			status, resolved := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Context retries"}, restoredToken)
			if status != 200 {
				t.Fatal("project unavailable")
			}
			body := map[string]any{
				"projectId":  resolved["project"].(map[string]any)["id"],
				"record":     map[string]any{"type": "finding", "title": "Context identity", "authority": "observed", "payload": map[string]any{"finding": "Preserve the captured checkout context."}},
				"gitContext": map[string]any{"branch": "main", "commit": "synthetic-original", "dirty": false},
			}
			status, first := reviewCapture(t, origin, "context-identity", body)
			if status != 201 {
				t.Fatal("initial capture failed")
			}
			status, replay := reviewCapture(t, origin, "context-identity", body)
			if status != 201 || !reflect.DeepEqual(first["record"], replay["record"]) {
				t.Fatal("identical context replay changed immutable capture")
			}
			for _, variant := range []struct {
				name    string
				context any
			}{
				{"branch", map[string]any{"branch": "feature", "commit": "synthetic-original", "dirty": false}},
				{"commit", map[string]any{"branch": "main", "commit": "synthetic-new", "dirty": false}},
				{"dirty", map[string]any{"branch": "main", "commit": "synthetic-original", "dirty": true}},
				{"malformed", "invalid-context"},
				{"null", nil},
			} {
				t.Run(variant.name, func(t *testing.T) {
					body["gitContext"] = variant.context
					status, result := reviewCapture(t, origin, "context-identity", body)
					if status != 409 || result["error"].(map[string]any)["code"] != "CONFLICT" {
						t.Fatalf("changed %s reused capture identity: HTTP %d", variant.name, status)
					}
				})
			}
			delete(body, "gitContext")
			status, _ = reviewCapture(t, origin, "context-identity", body)
			if status != 409 {
				t.Fatal("omitting original context reused capture identity")
			}
			status, first = reviewCapture(t, origin, "absent-context", body)
			if status != 201 {
				t.Fatal("capture without context failed")
			}
			body["gitContext"] = map[string]any{}
			status, _ = reviewCapture(t, origin, "absent-context", body)
			if status != 409 {
				t.Fatal("explicit empty context was conflated with absence")
			}
			body["gitContext"] = "invalid-context"
			status, _ = reviewCapture(t, origin, "invalid-first-context", body)
			if status != 400 {
				t.Fatal("malformed first context was not rejected before saving")
			}
		})
	}
}

func TestGlobalSettingsAuditsUseLiveProviderEnablement(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/settings", map[string]any{"expectedVersion": 0, "settings": map[string]any{"aiEnabled": true}})
			if status != 200 {
				t.Fatal("initial global setting failed")
			}
			status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "enabled": false})
			if status != 200 {
				t.Fatal("direct provider change failed")
			}
			for _, update := range []struct {
				version float64
				changes map[string]any
				enabled bool
			}{{1, map[string]any{}, false}, {2, map[string]any{"aiEnabled": true}, true}} {
				status, response := curationRequest(t, origin, "PATCH", "/settings", map[string]any{"expectedVersion": update.version, "settings": update.changes})
				if status != 200 || response["settings"].(map[string]any)["aiEnabled"] != update.enabled {
					t.Fatal("global update did not report live provider state")
				}
				status, archive := curationRequest(t, origin, "POST", "/export", map[string]any{})
				if status != 200 {
					t.Fatal("audit export failed")
				}
				found := false
				for _, value := range archive["data"].(map[string]any)["audit"].([]any) {
					audit := value.(map[string]any)
					if audit["action"] != "settings.updated" {
						continue
					}
					next := audit["next"].(map[string]any)
					if next["version"] != update.version+1 {
						continue
					}
					found = true
					if audit["previous"].(map[string]any)["aiEnabled"] != false || next["aiEnabled"] != update.enabled {
						t.Fatalf("settings audit version %.0f used stale global AI state", update.version+1)
					}
				}
				if !found {
					t.Fatal("global update audit missing")
				}
			}
		})
	}
}
