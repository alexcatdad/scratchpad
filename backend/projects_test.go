package backend_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func projectRequest(t *testing.T, origin, method, path string, body any, token string) (int, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, origin+"/api/v1/projects"+path, bytes.NewReader(data))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var result map[string]any
	if json.NewDecoder(res.Body).Decode(&result) != nil {
		t.Fatalf("non-JSON project response: %d", res.StatusCode)
	}
	return res.StatusCode, result
}
func TestExplicitProjectResolutionPersists(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			var id string
			t.Run("create", func(t *testing.T) {
				origin := startServer(t, path, databaseURL)
				status, body := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "  Sample Project  "}, restoredToken)
				if status != 200 {
					t.Fatalf("explicit resolution unavailable: %d", status)
				}
				project := body["project"].(map[string]any)
				id = project["id"].(string)
				if project["name"] != "Sample Project" || project["slug"] != "sample-project" || project["kind"] != "normal" || project["version"] != float64(1) {
					t.Fatal("incorrect project identity")
				}
				settings := project["settings"].(map[string]any)
				if settings["crossProjectAnalysis"] != true || settings["aiProcessing"] != false || len(settings["enabledRecordTypes"].([]any)) != 8 {
					t.Fatal("incorrect normal-project defaults")
				}
			})
			t.Run("restart", func(t *testing.T) {
				origin := startServer(t, path, databaseURL)
				status, body := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "sample project"}, restoredToken)
				if status != 200 || body["project"].(map[string]any)["id"] != id {
					t.Fatal("explicit identity changed after restart")
				}
				status, body = projectRequest(t, origin, "GET", "", nil, restoredToken)
				if status != 200 || len(body["projects"].([]any)) != 1 {
					t.Fatal("project listing lost the created project")
				}
			})
		})
	}
}

func TestGitRemotesResolveOneCanonicalProject(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			var id string
			for _, remote := range []string{"git@GitHub.com:owner/example.git", "https://github.com/owner/example.git", "ssh://git@github.com/owner/example"} {
				status, body := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": remote}}}, restoredToken)
				if status != 200 {
					t.Fatalf("Git resolution unavailable: %d", status)
				}
				project := body["project"].(map[string]any)
				if id == "" {
					id = project["id"].(string)
				}
				if project["id"] != id || project["name"] != "example" {
					t.Fatal("equivalent remotes created different projects")
				}
			}
			status, body := projectRequest(t, origin, "GET", "", nil, restoredToken)
			if status != 200 || len(body["projects"].([]any)) != 1 {
				t.Fatal("canonical project was duplicated")
			}
			sources := body["projects"].([]any)[0].(map[string]any)["sources"].([]any)
			if len(sources) != 1 || sources[0].(map[string]any)["identity"] != "github.com/owner/example" {
				t.Fatal("source identity was not preserved")
			}
		})
	}
}

func TestProjectUpdatesRequireCurrentVersion(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			_, body := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Original"}, restoredToken)
			id := body["project"].(map[string]any)["id"].(string)
			status, _ := projectRequest(t, origin, "PATCH", "/"+id, map[string]any{"name": "Changed"}, restoredToken)
			if status != 428 {
				t.Fatalf("project update did not require a version: %d", status)
			}
			status, body = projectRequest(t, origin, "PATCH", "/"+id, map[string]any{"name": "Changed", "kind": "external", "expectedVersion": 1}, restoredToken)
			if status != 200 {
				t.Fatalf("project update unavailable: %d", status)
			}
			project := body["project"].(map[string]any)
			if project["name"] != "Changed" || project["slug"] != "original" || project["version"] != float64(2) || project["settings"].(map[string]any)["crossProjectAnalysis"] != false {
				t.Fatal("project update lost identity or external consent defaults")
			}
			status, body = projectRequest(t, origin, "PATCH", "/"+id, map[string]any{"name": "Stale", "expectedVersion": 1}, restoredToken)
			if status != 409 || body["error"].(map[string]any)["details"].(map[string]any)["currentVersion"] != float64(2) {
				t.Fatal("stale project update was accepted")
			}
			status, body = projectRequest(t, origin, "GET", "/"+id, nil, restoredToken)
			if status != 200 || body["project"].(map[string]any)["name"] != "Changed" {
				t.Fatal("rejected update changed current state")
			}
		})
	}
}

func TestProjectSettingsRespectVersionAndExternalConsent(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			_, body := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Consent"}, restoredToken)
			id := body["project"].(map[string]any)["id"].(string)
			settings := map[string]any{"repoMirroring": map[string]any{"enabled": true, "recordTypes": []string{"finding"}}, "crossProjectAnalysis": true, "aiProcessing": true}
			status, body := projectRequest(t, origin, "PATCH", "/"+id+"/settings", map[string]any{"settings": settings, "expectedVersion": 1}, restoredToken)
			if status != 200 || body["version"] != float64(2) {
				t.Fatalf("settings update unavailable: %d", status)
			}
			if len(body["settings"].(map[string]any)["enabledRecordTypes"].([]any)) != 8 {
				t.Fatal("legacy missing record types did not receive defaults")
			}
			status, body = projectRequest(t, origin, "PATCH", "/"+id, map[string]any{"kind": "external", "expectedVersion": 2}, restoredToken)
			if status != 200 {
				t.Fatal("external conversion failed")
			}
			current := body["project"].(map[string]any)["settings"].(map[string]any)
			if current["crossProjectAnalysis"] != false || current["repoMirroring"].(map[string]any)["enabled"] != false || current["aiProcessing"] != true {
				t.Fatal("external conversion did not reset outward-sharing consent")
			}
			settings["enabledRecordTypes"] = []string{"unsupported"}
			status, _ = projectRequest(t, origin, "PATCH", "/"+id+"/settings", map[string]any{"settings": settings, "expectedVersion": 3}, restoredToken)
			if status != 400 {
				t.Fatal("unknown record type was accepted")
			}
			status, body = projectRequest(t, origin, "GET", "/"+id+"/settings", nil, restoredToken)
			if status != 200 || body["version"] != float64(3) {
				t.Fatal("rejected settings update changed project version")
			}
		})
	}
}

func TestUnicodeGitHostUsesCanonicalIdentity(t *testing.T) {
	path, _, _ := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	var id string
	for _, remote := range []string{"https://münich.example/team/repo.git", "https://xn--mnich-kva.example/team/repo.git"} {
		status, body := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": remote}}}, restoredToken)
		if status != 200 {
			t.Fatal("valid international Git host was rejected")
		}
		next := body["project"].(map[string]any)["id"].(string)
		if id != "" && next != id {
			t.Fatal("international Git host created duplicate project identities")
		}
		id = next
	}
}

func TestConcurrentResolutionAndUpdatesPreserveOneProject(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			results := make(chan map[string]any, 8)
			failures := make(chan error, 8)
			for range 8 {
				go func() {
					data, _ := json.Marshal(map[string]any{"context": map[string]any{"git": map[string]any{"remote": "https://github.com/owner/concurrent.git"}}})
					req, _ := http.NewRequest("POST", origin+"/api/v1/projects/resolve", bytes.NewReader(data))
					req.Header.Set("Authorization", "Bearer "+restoredToken)
					res, err := http.DefaultClient.Do(req)
					if err != nil {
						failures <- err
						return
					}
					defer res.Body.Close()
					var body map[string]any
					err = json.NewDecoder(res.Body).Decode(&body)
					if err != nil {
						failures <- err
						return
					}
					if res.StatusCode != 200 {
						failures <- fmt.Errorf("resolution failed: %d", res.StatusCode)
						return
					}
					results <- body
				}()
			}
			var id string
			for range 8 {
				select {
				case result := <-results:
					next := result["project"].(map[string]any)["id"].(string)
					if id != "" && next != id {
						t.Fatal("concurrent agents resolved different projects")
					}
					id = next
				case err := <-failures:
					t.Fatal(err)
				}
			}
			status, body := projectRequest(t, origin, "GET", "", nil, restoredToken)
			if status != 200 || len(body["projects"].([]any)) != 1 {
				t.Fatal("concurrent resolution duplicated the project")
			}
			codes := make(chan int, 2)
			for _, name := range []string{"First", "Second"} {
				go func() {
					data, _ := json.Marshal(map[string]any{"name": name, "expectedVersion": 1})
					req, _ := http.NewRequest("PATCH", origin+"/api/v1/projects/"+id, bytes.NewReader(data))
					req.Header.Set("Authorization", "Bearer "+restoredToken)
					res, err := http.DefaultClient.Do(req)
					if err != nil {
						failures <- err
						return
					}
					res.Body.Close()
					codes <- res.StatusCode
				}()
			}
			counts := map[int]int{}
			for range 2 {
				select {
				case code := <-codes:
					counts[code]++
				case err := <-failures:
					t.Fatal(err)
				}
			}
			if counts[200] != 1 || counts[409] != 1 {
				t.Fatal("concurrent updates lost optimistic locking", counts)
			}
		})
	}
}
func TestProjectBrowserMutationsRequireOrigin(t *testing.T) {
	path, _, local := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	for _, requestOrigin := range []string{"", "http://attacker.invalid", origin} {
		data := []byte(`{"name":"Browser"}`)
		req, _ := http.NewRequest("POST", origin+"/api/v1/projects/resolve-explicit", bytes.NewReader(data))
		req.AddCookie(local)
		req.Header.Set("Origin", requestOrigin)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		expected := 403
		if requestOrigin == origin {
			expected = 200
		}
		if res.StatusCode != expected {
			t.Fatal("browser-origin enforcement regressed")
		}
	}
	status, _ := projectRequest(t, origin, "GET", "", nil, "")
	if status != 401 {
		t.Fatal("anonymous caller could read projects")
	}
}

func TestGitRemoteDotSegmentsRetainCanonicalSource(t *testing.T) {
	path, _, _ := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	var id string
	for _, remote := range []string{"https://github.com/owner/repo.git", "HTTPS://GitHub.com:443/owner/temp/%2e%2e/repo.git?ignored=synthetic#fragment"} {
		status, body := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": remote}}}, restoredToken)
		if status != 200 {
			t.Fatal("valid Git remote was rejected")
		}
		next := body["project"].(map[string]any)["id"].(string)
		if id != "" && next != id {
			t.Fatal("normalized URL path created a different project")
		}
		id = next
	}
}

func TestProjectDefaultsAreInheritedWithoutLeakingUnknownFields(t *testing.T) {
	path, _, _ := githubHistoricalBrowser(t, "sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"enabledRecordTypes": []string{"finding"}, "repoMirroring": map[string]any{"enabled": true, "recordTypes": []string{"finding"}}, "crossProjectAnalysis": false, "aiProcessing": true, "apiKey": "synthetic-secret-must-not-leak"}
	row, _ := json.Marshal(map[string]any{"defaultProjectSettings": settings})
	if _, err = db.Exec(`UPDATE entities SET data=$1 WHERE kind='settings' AND id='global'`, string(row)); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	origin := startServer(t, path, "")
	_, body := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Inherited"}, restoredToken)
	current := body["project"].(map[string]any)["settings"].(map[string]any)
	if current["aiProcessing"] != true || current["apiKey"] != nil || len(current["enabledRecordTypes"].([]any)) != 1 {
		t.Fatal("project did not inherit canonical administrator defaults")
	}
	_, body = projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Independent", "kind": "external"}, restoredToken)
	current = body["project"].(map[string]any)["settings"].(map[string]any)
	if current["repoMirroring"].(map[string]any)["enabled"] != false || current["aiProcessing"] != false || len(current["enabledRecordTypes"].([]any)) != 8 {
		t.Fatal("external project inherited normal-project sharing defaults")
	}
}

func TestLegacyShortIPv4GitHostRetainsProjectIdentity(t *testing.T) {
	path, _, _ := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	var id string
	for _, remote := range []string{"https://127.0.0.1/owner/repo.git", "http://127.1/owner/repo.git", "http://0x7f000001/owner/repo.git"} {
		status, body := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": remote}}}, restoredToken)
		if status != 200 {
			t.Fatal("legacy IPv4 Git host rejected")
		}
		next := body["project"].(map[string]any)["id"].(string)
		if id != "" && next != id {
			t.Fatal("legacy IPv4 Git host changed project identity")
		}
		id = next
	}
}

func TestSCPUnicodeHostRetainsLegacyEscapedSource(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			var opaqueID string
			for _, remote := range []string{"git@münich.example:team/repo.git", "ssh://git@münich.example/team/repo.git", "git://münich.example/team/repo.git"} {
				status, body := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": remote}}}, restoredToken)
				if status != 200 {
					t.Fatal("opaque Unicode remote rejected")
				}
				id := body["project"].(map[string]any)["id"].(string)
				if opaqueID != "" && id != opaqueID {
					t.Fatal("SCP, SSH and Git opaque identities differ")
				}
				opaqueID = id
			}
			status, body := projectRequest(t, origin, "GET", "", nil, restoredToken)
			if status != 200 {
				t.Fatal("source listing unavailable")
			}
			sources := body["projects"].([]any)[0].(map[string]any)["sources"].([]any)
			if sources[0].(map[string]any)["identity"] != "m%c3%bcnich.example/team/repo" {
				t.Fatal("opaque hostname changed its historical source identity")
			}
			var httpsID string
			for _, remote := range []string{"https://münich.example/team/repo.git", "https://xn--mnich-kva.example/team/repo.git"} {
				status, body := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": remote}}}, restoredToken)
				if status != 200 {
					t.Fatal("HTTP Unicode remote rejected")
				}
				id := body["project"].(map[string]any)["id"].(string)
				if httpsID != "" && id != httpsID {
					t.Fatal("HTTP IDNA aliases changed project identity")
				}
				httpsID = id
			}
			if httpsID == opaqueID {
				t.Fatal("import compatibility changed opaque URL identity to HTTP IDNA")
			}
		})
	}
}
