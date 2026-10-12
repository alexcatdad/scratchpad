package backend_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func settingsHeaderRequest(t *testing.T, origin, header string, body map[string]any) (int, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(body)
	request, _ := http.NewRequest("PATCH", origin+"/api/v1/settings", bytes.NewReader(data))
	request.Header.Set("Authorization", "Bearer "+restoredToken)
	if header != "" {
		request.Header.Set("If-Match", header)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if json.NewDecoder(response.Body).Decode(&result) != nil {
		t.Fatal("settings returned non-JSON")
	}
	return response.StatusCode, result
}

func TestFreshGlobalSettingsAcceptHeaderZeroAndBodyPrecedence(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, result := settingsHeaderRequest(t, origin, `"0"`, map[string]any{"settings": map[string]any{"aiEnabled": true}})
			if status != 200 || result["settings"].(map[string]any)["version"] != float64(1) || result["settings"].(map[string]any)["aiEnabled"] != true {
				t.Fatalf("header zero did not initialize global settings: HTTP %d", status)
			}
			status, result = settingsHeaderRequest(t, origin, `"0"`, map[string]any{"expectedVersion": 1, "settings": map[string]any{}})
			if status != 200 || result["settings"].(map[string]any)["version"] != float64(2) {
				t.Fatal("explicit body precondition did not take precedence")
			}
			status, _ = settingsHeaderRequest(t, origin, `"1"`, map[string]any{"settings": map[string]any{"aiEnabled": false}})
			if status != 409 {
				t.Fatal("stale header overwrote current settings")
			}
			status, _ = settingsHeaderRequest(t, origin, `"2"`, map[string]any{"expectedVersion": false, "settings": map[string]any{"aiEnabled": false}})
			if status != 400 {
				t.Fatal("valid header bypassed invalid explicit body version")
			}
			status, result = curationRequest(t, origin, "GET", "/settings", nil)
			if status != 200 || result["settings"].(map[string]any)["version"] != float64(2) || result["settings"].(map[string]any)["aiEnabled"] != true {
				t.Fatal("failed preconditions changed global/provider state")
			}
		})
	}
}

func TestGitRemoteResolutionRejectsEmptyRepositoryBeforeCreatingState(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			for _, remote := range []string{"https://github.com", "https://github.com/", "ssh://git@github.com", "git@github.com:/", "https://github.com/.git", "https://github.com/team/..", "https://github.com/team/%2e%2e", "https://github.com/team/.git"} {
				status, response := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": remote}}}, restoredToken)
				if status != 400 || response["error"].(map[string]any)["code"] != "PROJECT_IDENTITY_REQUIRED" {
					t.Errorf("empty repository remote accepted: %s HTTP %d", remote, status)
				}
			}
			status, projects := projectRequest(t, origin, "GET", "", nil, restoredToken)
			if status != 200 || len(projects["projects"].([]any)) != 0 {
				t.Fatal("invalid remotes created project state")
			}
			status, archive := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("audit archive unavailable")
			}
			for _, value := range archive["data"].(map[string]any)["audit"].([]any) {
				if value.(map[string]any)["action"] == "project.created" {
					t.Fatal("invalid remote created a project audit")
				}
			}
			for _, remote := range []string{"https://github.com/team/repo.git/", "git@github.com:team/repo.git", "ssh://git@github.com/repo.git"} {
				status, response := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": remote}}}, restoredToken)
				if status != 200 || response["project"].(map[string]any)["name"] != "repo" || response["project"].(map[string]any)["slug"] != "repo" {
					t.Fatal("valid repository path stopped resolving")
				}
			}
		})
	}
}
