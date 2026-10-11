package backend_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOwnerProfileStartsAtZeroAndRetainsVersionedName(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			var version float64
			for _, phase := range []string{"initial", "restart"} {
				t.Run(phase, func(t *testing.T) {
					origin := startServer(t, path, databaseURL)
					status, body := curationRequest(t, origin, "GET", "/profile", nil)
					if status != 200 {
						t.Fatalf("profile unavailable: %d", status)
					}
					profile := body["profile"].(map[string]any)
					if profile["version"] != version || profile["github"] != nil {
						t.Fatal("profile version/defaults changed")
					}
					if phase == "restart" && profile["displayName"] != "Synthetic owner" {
						t.Fatal("profile name did not survive restart")
					}
					status, body = curationRequest(t, origin, "PATCH", "/profile", map[string]any{"displayName": "  Synthetic owner  ", "expectedVersion": version})
					if status != 200 || body["profile"].(map[string]any)["displayName"] != "Synthetic owner" {
						t.Fatal("versioned profile update failed")
					}
					version++
					status, _ = curationRequest(t, origin, "PATCH", "/profile", map[string]any{"displayName": "Stale", "expectedVersion": version - 1})
					if status != 409 {
						t.Fatal("stale profile update accepted")
					}
				})
			}
		})
	}
}

func TestPublicGithubProfileDoesNotAuthorizeOwner(t *testing.T) {
	githubService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "unexpected credentials", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"login":"Owner-Example","type":"User","name":"Synthetic public owner","avatar_url":"https://avatars.githubusercontent.com/u/42?v=4"}`)
	}))
	path, _, local := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	status, body := curationRequest(t, origin, "POST", "/profile/github", map[string]any{"username": " owner-example ", "expectedVersion": 0})
	if status != 200 {
		t.Fatalf("public profile fetch unavailable: %d", status)
	}
	profile := body["profile"].(map[string]any)
	github := profile["github"].(map[string]any)
	if profile["version"] != float64(1) || github["username"] != "Owner-Example" || github["profileUrl"] != "https://github.com/Owner-Example" || github["avatarUrl"] != "https://avatars.githubusercontent.com/u/42?v=4" {
		t.Fatal("public presentation fields changed")
	}
	if githubStatus(t, origin, local)["binding"] != nil {
		t.Fatal("public profile established an authorization binding")
	}
	status, body = curationRequest(t, origin, "DELETE", "/profile/github", map[string]any{"expectedVersion": 1})
	if status != 200 || body["profile"].(map[string]any)["github"] != nil || body["profile"].(map[string]any)["version"] != float64(2) {
		t.Fatal("public profile unlink changed owner access or version")
	}
}

func TestPublicGithubFailuresKeepProfileUnchanged(t *testing.T) {
	for _, mode := range []string{"mismatch", "organization", "redirect", "oversized", "missing", "rate-limit"} {
		t.Run(mode, func(t *testing.T) {
			githubService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "redirect":
					w.Header().Set("Location", "https://attacker.invalid/")
					w.WriteHeader(302)
				case "oversized":
					io.WriteString(w, strings.Repeat("x", 65537))
				case "missing":
					http.Error(w, "synthetic-secret", 404)
				case "rate-limit":
					http.Error(w, "synthetic-secret", 429)
				case "organization":
					io.WriteString(w, `{"login":"owner-example","type":"Organization","name":null,"avatar_url":null}`)
				default:
					io.WriteString(w, `{"login":"another-owner","type":"User","name":null,"avatar_url":null}`)
				}
			}))
			path, _, _ := githubHistoricalBrowser(t, "sqlite")
			origin := startServer(t, path, "")
			status, body := curationRequest(t, origin, "POST", "/profile/github", map[string]any{"username": "owner-example", "expectedVersion": 0})
			expected := 503
			if mode == "missing" {
				expected = 404
			}
			data, _ := json.Marshal(body)
			if status != expected || strings.Contains(string(data), "synthetic-secret") {
				t.Fatal("public GitHub failure did not preserve safe error contract")
			}
			_, body = curationRequest(t, origin, "GET", "/profile", nil)
			if body["profile"].(map[string]any)["version"] != float64(0) || body["profile"].(map[string]any)["github"] != nil {
				t.Fatal("failed public fetch changed profile state")
			}
		})
	}
}
func TestGithubProfileRechecksVersionAfterExternalWait(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	githubService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		case <-r.Context().Done():
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"login":"owner-example","type":"User","name":null,"avatar_url":null}`)
	}))
	path, _, _ := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	data := []byte(`{"username":"owner-example","expectedVersion":0}`)
	req, _ := http.NewRequest("POST", origin+"/api/v1/profile/github", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+restoredToken)
	result := make(chan *http.Response, 1)
	failures := make(chan error, 1)
	go func() {
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			failures <- err
		} else {
			result <- res
		}
	}()
	select {
	case <-entered:
	case <-time.After(6 * time.Second):
		t.Fatal("profile fetch did not reach external boundary")
	}
	status, _ := curationRequest(t, origin, "PATCH", "/profile", map[string]any{"displayName": "Newer owner name", "expectedVersion": 0})
	if status != 200 {
		close(release)
		t.Fatal("external profile request blocked owner update")
	}
	close(release)
	select {
	case res := <-result:
		res.Body.Close()
		if res.StatusCode != 409 {
			t.Fatal("stale public fetch overwrote a newer profile")
		}
	case err := <-failures:
		t.Fatal(err)
	case <-time.After(6 * time.Second):
		t.Fatal("profile callback did not finish")
	}
	_, body := curationRequest(t, origin, "GET", "/profile", nil)
	if body["profile"].(map[string]any)["displayName"] != "Newer owner name" || body["profile"].(map[string]any)["github"] != nil {
		t.Fatal("concurrent public fetch corrupted profile")
	}
}

func TestImportedGithubPresentationOmitsUnsafeUrlsAndFields(t *testing.T) {
	path, _, _ := githubHistoricalBrowser(t, "sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]any{"id": "owner-profile", "version": 1, "displayName": "Imported owner", "createdAt": "2026-10-01T00:00:00.000Z", "github": map[string]any{"username": "owner-example", "displayName": nil, "avatarUrl": "javascript:synthetic", "profileUrl": "https://attacker.invalid/private", "fetchedAt": "2026-10-01T00:00:00.000Z", "accessToken": "synthetic-secret"}}
	data, _ := json.Marshal(row)
	if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('profile','owner-profile',$1,1,'2026-10-01T00:00:00.000Z')`, string(data)); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	origin := startServer(t, path, "")
	_, body := curationRequest(t, origin, "GET", "/profile", nil)
	github := body["profile"].(map[string]any)["github"].(map[string]any)
	if github["avatarUrl"] != nil || github["profileUrl"] != "https://github.com/owner-example" || github["accessToken"] != nil {
		t.Fatal("imported public presentation exposed untrusted URL or fields")
	}
}

func TestPublicAvatarNormalizesAllowedOrigin(t *testing.T) {
	githubService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"login":"owner-example","type":"User","name":null,"avatar_url":"HTTPS://AVATARS.GITHUBUSERCONTENT.COM:443"}`)
	}))
	path, _, _ := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	status, body := curationRequest(t, origin, "POST", "/profile/github", map[string]any{"username": "owner-example", "expectedVersion": 0})
	if status != 200 || body["profile"].(map[string]any)["github"].(map[string]any)["avatarUrl"] != "https://avatars.githubusercontent.com/" {
		t.Fatal("allowed avatar origin did not retain canonical presentation")
	}
}
