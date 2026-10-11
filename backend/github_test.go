package backend_test

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func githubSetup(t *testing.T, path, databaseURL string) string {
	t.Helper()
	cmd := exec.Command(binary, "setup")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_PATH="+path, "SCRATCHPAD_DATABASE_URL="+databaseURL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("owner setup failed", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return lines[len(lines)-1]
}
func githubRequest(t *testing.T, origin, method, route string, body any, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, origin+"/api/v1/auth/github"+route, bytes.NewReader(data))
	req.Header.Set("Origin", origin)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}
func githubOptions(t *testing.T, origin, intent, setup string, cookies ...*http.Cookie) (*url.URL, *http.Cookie) {
	t.Helper()
	res := githubRequest(t, origin, "POST", "/options", map[string]any{"intent": intent, "setupToken": setup}, cookies...)
	if res.StatusCode != 200 {
		t.Fatalf("GitHub options unavailable: %d", res.StatusCode)
	}
	var result struct {
		AuthorizationURL string `json:"authorizationUrl"`
	}
	if json.NewDecoder(res.Body).Decode(&result) != nil {
		t.Fatal("invalid options response")
	}
	target, err := url.Parse(result.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range res.Cookies() {
		if cookie.Name == "scratchpad_github_state" {
			return target, cookie
		}
	}
	t.Fatal("missing browser binding cookie")
	return nil, nil
}
func TestGithubSetupOptions(t *testing.T) {
	t.Setenv("SCRATCHPAD_GITHUB_CLIENT_ID", "synthetic-client")
	t.Setenv("SCRATCHPAD_GITHUB_CLIENT_SECRET", "synthetic-secret")
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	path := filepath.Join(dir, "owner.sqlite")
	token := githubSetup(t, path, "")
	origin := startServer(t, path, "")
	target, cookie := githubOptions(t, origin, "setup", token)
	q := target.Query()
	if target.Scheme != "https" || target.Host != "github.com" || target.Path != "/login/oauth/authorize" || q.Get("client_id") != "synthetic-client" || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || len(q.Get("state")) != 43 || q.Get("scope") != "" || q.Get("redirect_uri") != origin+"/api/v1/auth/github/callback" {
		t.Fatal("OAuth authorization contract is incorrect")
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/api/v1/auth/github" || cookie.MaxAge != 600 {
		t.Fatal("state cookie is not browser-bound")
	}
	res, err := http.Get(origin + "/api/v1/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var status struct{ GithubConfigured bool }
	json.NewDecoder(res.Body).Decode(&status)
	if !status.GithubConfigured {
		t.Fatal("configured OAuth is hidden")
	}
}

func TestGithubOAuthEstablishesBrowserOwner(t *testing.T) {
	var exchanges atomic.Int32
	githubService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login/oauth/access_token":
			exchanges.Add(1)
			r.ParseForm()
			if len(r.Form.Get("code_verifier")) != 43 || r.Form.Get("client_secret") != "synthetic-secret" {
				http.Error(w, "invalid exchange", 400)
				return
			}
			io.WriteString(w, `{"access_token":"synthetic-oauth-token","token_type":"bearer"}`)
		case "/user":
			if r.Header.Get("Authorization") != "Bearer synthetic-oauth-token" {
				http.Error(w, "missing authorization", 401)
				return
			}
			io.WriteString(w, `{"id":42,"login":"owner-example","type":"User"}`)
		default:
			http.Error(w, "not yet available", 503)
		}
	}))
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	path := filepath.Join(dir, "owner.sqlite")
	token := githubSetup(t, path, "")
	origin := startServer(t, path, "")
	target, stateCookie := githubOptions(t, origin, "setup", token)
	route := "/callback?code=synthetic-code&state=" + url.QueryEscape(target.Query().Get("state"))
	wrong := githubRequest(t, origin, "GET", route, nil, &http.Cookie{Name: stateCookie.Name, Value: "wrong-browser"})
	if wrong.StatusCode != 303 || exchanges.Load() != 0 {
		t.Fatal("unbound callback reached GitHub")
	}
	res := githubRequest(t, origin, "GET", route, nil, stateCookie)
	if res.StatusCode != 302 || res.Header.Get("Location") != "/" {
		t.Fatalf("OAuth callback failed: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	var session *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "scratchpad_session" {
			session = c
		}
	}
	if session == nil || !session.HttpOnly || session.SameSite != http.SameSiteStrictMode {
		t.Fatal("missing protected browser session")
	}
	req, _ := http.NewRequest("GET", origin+"/api/v1/auth/status", nil)
	req.AddCookie(session)
	status, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer status.Body.Close()
	var body struct{ Initialized, Authenticated bool }
	json.NewDecoder(status.Body).Decode(&body)
	if !body.Initialized || !body.Authenticated {
		t.Fatal("OAuth did not establish the owner")
	}
	replay := githubRequest(t, origin, "GET", route, nil, stateCookie)
	if replay.StatusCode != 303 || exchanges.Load() != 1 {
		t.Fatal("OAuth state was reused")
	}
}

func githubLogin(t *testing.T, origin, intent, setup string, cookies ...*http.Cookie) *http.Cookie {
	t.Helper()
	target, state := githubOptions(t, origin, intent, setup, cookies...)
	res := githubRequest(t, origin, "GET", "/callback?code=synthetic-code&state="+url.QueryEscape(target.Query().Get("state")), nil, state)
	if res.StatusCode != 302 {
		t.Fatalf("GitHub sign-in failed: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	for _, cookie := range res.Cookies() {
		if cookie.Name == "scratchpad_session" {
			return cookie
		}
	}
	t.Fatal("missing browser session")
	return nil
}
func TestGithubCompleteKeySyncAndSSHLogin(t *testing.T) {
	private := filepath.Join(t.TempDir(), "key")
	if err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", private).Run(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(private + ".pub")
	key := strings.Join(strings.Fields(string(raw))[:2], " ")
	var signingPages atomic.Int32
	githubService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login/oauth/access_token":
			io.WriteString(w, `{"access_token":"synthetic-oauth-token","token_type":"bearer"}`)
		case "/user", "/user/42", "/users/owner-example":
			io.WriteString(w, `{"id":42,"login":"owner-example","type":"User"}`)
		case "/users/owner-example/keys":
			json.NewEncoder(w).Encode([]any{map[string]any{"key": key + " comment"}})
		case "/users/owner-example/ssh_signing_keys":
			signingPages.Add(1)
			if r.URL.Query().Get("page") == "1" {
				w.Header().Set("Link", `<https://api.github.com/users/owner-example/ssh_signing_keys?per_page=100&page=2>; rel="next last"`)
				io.WriteString(w, `[]`)
			} else {
				w.Header().Set("Link", `<https://api.github.com/users/owner-example/ssh_signing_keys?per_page=100&page=2>; rel="last"`)
				json.NewEncoder(w).Encode([]any{map[string]any{"key": key}})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	path := filepath.Join(dir, "owner.sqlite")
	token := githubSetup(t, path, "")
	origin := startServer(t, path, "")
	session := githubLogin(t, origin, "setup", token)
	res := githubRequest(t, origin, "GET", "", nil, session)
	if res.StatusCode != 200 {
		t.Fatalf("GitHub status unavailable: %d", res.StatusCode)
	}
	var status struct {
		CacheValid bool
		Keys       []struct {
			PublicKey  string
			Categories []string
		}
		Binding struct{ AccountID string }
	}
	if json.NewDecoder(res.Body).Decode(&status) != nil || !status.CacheValid || status.Binding.AccountID != "42" || len(status.Keys) != 1 || status.Keys[0].PublicKey != key || !reflect.DeepEqual(status.Keys[0].Categories, []string{"authentication", "signing"}) || signingPages.Load() != 2 {
		t.Fatal("complete paginated key cache was not installed")
	}
	_, proof := signedChallenge(t, origin, private, key)
	code, body := postJSON(t, origin, "/api/v1/auth/mcp/verify", proof)
	if code != 200 || body["accessToken"] == nil {
		t.Fatal("published key could not prove possession")
	}
}

// All mutable service state is synthetic and protected because sync runs in
// parallel with HTTP requests. Responses can fail one category after the other
// has completed, which must never publish a partial authorization snapshot.
type githubFixture struct {
	mu               sync.Mutex
	key, mode, login string
	accountID        int
}

func (f *githubFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/login/oauth/access_token" {
		io.WriteString(w, `{"access_token":"synthetic-oauth-token","token_type":"bearer"}`)
		return
	}
	login := f.login
	if login == "" {
		login = "owner-example"
	}
	id := f.accountID
	if id == 0 {
		id = 42
	}
	if r.URL.Path == "/user" || r.URL.Path == "/user/42" || r.URL.Path == "/users/"+login {
		json.NewEncoder(w).Encode(map[string]any{"id": id, "login": login, "type": "User"})
		return
	}
	if r.URL.Path == "/users/"+login+"/ssh_signing_keys" && f.mode == "failure" {
		http.Error(w, "synthetic-secret-must-not-leak", 429)
		return
	}
	if r.URL.Path == "/users/"+login+"/keys" || r.URL.Path == "/users/"+login+"/ssh_signing_keys" {
		if f.mode == "unsafe" {
			w.Header().Set("Link", `<https://attacker.invalid/keys?per_page=100&page=2>; rel="next"`)
		}
		if f.mode == "incomplete" {
			w.Header().Set("Link", `<https://api.github.com/users/`+login+`/keys?per_page=100&page=2>; rel="last"`)
			io.WriteString(w, `[]`)
			return
		}
		if f.mode == "truncated" {
			if r.URL.Query().Get("page") == "1" {
				w.Header().Set("Link", `<https://api.github.com`+r.URL.Path+`?per_page=100&page=2>; rel="next", <https://api.github.com`+r.URL.Path+`?per_page=100&page=3>; rel="last"`)
			}
			io.WriteString(w, `[]`)
			return
		}
		if f.mode == "removed" || f.mode == "failure" {
			io.WriteString(w, `[]`)
		} else {
			json.NewEncoder(w).Encode([]any{map[string]any{"key": f.key}})
		}
		return
	}
	http.NotFound(w, r)
}
func (f *githubFixture) set(mode string) { f.mu.Lock(); f.mode = mode; f.mu.Unlock() }
func githubSyntheticKey(t *testing.T) (string, string) {
	t.Helper()
	private := filepath.Join(t.TempDir(), "key")
	if exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", private).Run() != nil {
		t.Fatal("key generation failed")
	}
	raw, _ := os.ReadFile(private + ".pub")
	return private, strings.Join(strings.Fields(string(raw))[:2], " ")
}
func githubStatus(t *testing.T, origin string, session *http.Cookie) map[string]any {
	t.Helper()
	res := githubRequest(t, origin, "GET", "", nil, session)
	if res.StatusCode != 200 {
		t.Fatalf("GitHub status failed: %d", res.StatusCode)
	}
	var value map[string]any
	if json.NewDecoder(res.Body).Decode(&value) != nil {
		t.Fatal("invalid status response")
	}
	return value
}
func TestGithubFailedSyncPreservesCompleteCache(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			private, key := githubSyntheticKey(t)
			fixture := &githubFixture{key: key}
			githubService(t, fixture)
			path, databaseURL := "", ""
			if engine == "sqlite" {
				dir := t.TempDir()
				os.Chmod(dir, 0700)
				path = filepath.Join(dir, "owner.sqlite")
			} else {
				databaseURL = legacyPostgres(t)
			}
			token := githubSetup(t, path, databaseURL)
			origin := startServer(t, path, databaseURL)
			session := githubLogin(t, origin, "setup", token)
			before := githubStatus(t, origin, session)
			fixture.set("failure")
			res := githubRequest(t, origin, "POST", "/sync", map[string]any{}, session)
			if res.StatusCode != 503 {
				t.Fatalf("failed category sync did not fail safely: %d", res.StatusCode)
			}
			var failure map[string]any
			json.NewDecoder(res.Body).Decode(&failure)
			if failure["error"].(map[string]any)["code"] != "GITHUB_RATE_LIMITED" {
				t.Fatal("rate-limit error contract regressed")
			}
			after := githubStatus(t, origin, session)
			if after["lastSuccessfulSyncAt"] != before["lastSuccessfulSyncAt"] || !reflect.DeepEqual(after["keys"], before["keys"]) || after["lastSyncError"] != "GITHUB_RATE_LIMITED" {
				t.Fatal("failed sync modified the last complete cache")
			}
			_, proof := signedChallenge(t, origin, private, key)
			status, body := postJSON(t, origin, "/api/v1/auth/mcp/verify", proof)
			if status != 200 {
				t.Fatal("preserved cache lost authorization")
			}
			bearer := body["accessToken"].(string)
			fixture.set("unsafe")
			res = githubRequest(t, origin, "POST", "/sync", map[string]any{}, session)
			if res.StatusCode != 503 {
				t.Fatal("unsafe pagination accepted")
			}
			if githubStatus(t, origin, session)["lastSuccessfulSyncAt"] != before["lastSuccessfulSyncAt"] {
				t.Fatal("unsafe pagination advanced freshness")
			}
			fixture.set("incomplete")
			res = githubRequest(t, origin, "POST", "/sync", map[string]any{}, session)
			if res.StatusCode != 503 {
				t.Fatalf("pagination advertising unfetched pages was accepted: %d", res.StatusCode)
			}
			after = githubStatus(t, origin, session)
			if after["lastSuccessfulSyncAt"] != before["lastSuccessfulSyncAt"] || !reflect.DeepEqual(after["keys"], before["keys"]) {
				t.Fatal("incomplete pagination replaced the complete cache")
			}
			fixture.set("truncated")
			res = githubRequest(t, origin, "POST", "/sync", map[string]any{}, session)
			if res.StatusCode != 503 {
				t.Fatalf("pagination stopped before its advertised last page: %d", res.StatusCode)
			}
			after = githubStatus(t, origin, session)
			if after["lastSuccessfulSyncAt"] != before["lastSuccessfulSyncAt"] || !reflect.DeepEqual(after["keys"], before["keys"]) {
				t.Fatal("truncated pagination replaced the complete cache")
			}
			fixture.set("removed")
			res = githubRequest(t, origin, "POST", "/sync", map[string]any{}, session)
			if res.StatusCode != 200 {
				t.Fatal("complete key removal failed")
			}
			status, _ = postJSON(t, origin, "/api/v1/auth/mcp/challenge", map[string]any{"publicKey": key})
			if status != 401 {
				t.Fatal("removed key remained eligible")
			}
			req, _ := http.NewRequest("GET", origin+"/api/v1/auth/status", nil)
			req.Header.Set("Authorization", "Bearer "+bearer)
			check, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer check.Body.Close()
			var auth struct{ Authenticated bool }
			json.NewDecoder(check.Body).Decode(&auth)
			if auth.Authenticated {
				t.Fatal("removed key session remained valid")
			}
		})
	}
}

func githubHistoricalBrowser(t *testing.T, engine string) (string, string, *http.Cookie) {
	t.Helper()
	path, databaseURL, driver := "", "", "sqlite"
	if engine == "sqlite" {
		path = legacyFixture(t)
	} else {
		databaseURL = legacyPostgres(t)
		driver = "pgx"
	}
	dsn := path
	if databaseURL != "" {
		dsn = databaseURL
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedRestoredHistory(t, db)
	restoredDigest := sha256.Sum256([]byte(restoredToken))
	restoredID := hex.EncodeToString(restoredDigest[:])
	restoredSession, _ := json.Marshal(map[string]any{"id": restoredID, "version": 1, "credentialId": "credential_restored", "browser": false, "expiresAt": "2099-01-01T00:00:00Z", "createdAt": "2026-10-01T00:00:00Z"})
	if _, err = db.Exec(`UPDATE entities SET data=$1 WHERE kind='session' AND id=$2`, string(restoredSession), restoredID); err != nil {
		t.Fatal(err)
	}
	token := "synthetic-independent-browser-session"
	digest := sha256.Sum256([]byte(token))
	id := hex.EncodeToString(digest[:])
	row, _ := json.Marshal(map[string]any{"id": id, "version": 1, "credentialId": "credential_restored", "browser": true, "authenticatedAt": time.Now().UTC().Format(time.RFC3339Nano), "expiresAt": "2099-01-01T00:00:00Z", "createdAt": time.Now().UTC().Format(time.RFC3339Nano)})
	if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('session',$1,$2,1,$3)`, id, string(row), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	return path, databaseURL, &http.Cookie{Name: "scratchpad_session", Value: token}
}
func TestGithubLocalBlocksAndIndependentUnlink(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			private, key := githubSyntheticKey(t)
			fixture := &githubFixture{key: key}
			githubService(t, fixture)
			path, databaseURL, local := githubHistoricalBrowser(t, engine)
			token := githubSetup(t, path, databaseURL)
			origin := startServer(t, path, databaseURL)
			session := githubLogin(t, origin, "setup", token)
			_, proof := signedChallenge(t, origin, private, key)
			status, body := postJSON(t, origin, "/api/v1/auth/mcp/verify", proof)
			if status != 200 {
				t.Fatal("published key login failed")
			}
			bearer := body["accessToken"].(string)
			res := githubRequest(t, origin, "POST", "/block", map[string]any{"publicKey": key, "blocked": true}, session)
			if res.StatusCode != 200 {
				t.Fatalf("key block unavailable: %d", res.StatusCode)
			}
			status, _ = fetchRecord(t, origin, bearer)
			if status != 401 {
				t.Fatal("blocked key retained its session")
			}
			fixture.set("removed")
			githubRequest(t, origin, "POST", "/sync", map[string]any{}, session)
			fixture.set("")
			githubRequest(t, origin, "POST", "/sync", map[string]any{}, session)
			status, _ = postJSON(t, origin, "/api/v1/auth/mcp/challenge", map[string]any{"publicKey": key})
			if status != 401 {
				t.Fatal("key reappearance bypassed its local block")
			}
			githubRequest(t, origin, "POST", "/block", map[string]any{"publicKey": key, "blocked": false}, session)
			_, proof = signedChallenge(t, origin, private, key)
			status, _ = postJSON(t, origin, "/api/v1/auth/mcp/verify", proof)
			if status != 200 {
				t.Fatal("explicit unblock did not restore fresh login")
			}
			res = githubRequest(t, origin, "DELETE", "", nil, session)
			if res.StatusCode != 403 {
				t.Fatal("GitHub identity could unlink its own only access")
			}
			res = githubRequest(t, origin, "DELETE", "", nil, local)
			if res.StatusCode != 200 {
				t.Fatal("independent local browser could not unlink")
			}
			if githubStatus(t, origin, local)["binding"] != nil {
				t.Fatal("unlink retained the binding")
			}
			res = githubRequest(t, origin, "GET", "", nil, session)
			if res.StatusCode != 401 {
				t.Fatal("unlink retained GitHub browser authorization")
			}
			status, _ = fetchRecord(t, origin, restoredToken)
			if status != 200 {
				t.Fatal("unlink damaged independent local access or owner history")
			}
		})
	}
}

func TestGithubStartupSyncUsesPersistedFreshness(t *testing.T) {
	_, key := githubSyntheticKey(t)
	fixture := &githubFixture{key: key, mode: "failure"}
	githubService(t, fixture)
	// Previously linked accounts still refresh public keys if OAuth is disabled.
	t.Setenv("SCRATCHPAD_GITHUB_CLIENT_ID", "")
	t.Setenv("SCRATCHPAD_GITHUB_CLIENT_SECRET", "")
	path, _, local := githubHistoricalBrowser(t, "sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	binding, _ := json.Marshal(map[string]any{"id": "owner", "version": 1, "accountId": "42", "username": "owner-example", "generation": "historical-generation", "keys": []any{map[string]any{"publicKey": key, "categories": []string{"authentication"}}}, "lastSuccessfulSyncAt": before, "lastAttemptAt": "2000-01-01T00:00:00Z"})
	if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('github_binding','owner',$1,1,'2026-10-01T00:00:00Z')`, string(binding)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for range 2 {
		t.Run("restart", func(t *testing.T) {
			origin := startServer(t, path, "")
			deadline := time.Now().Add(4 * time.Second)
			for {
				status := githubStatus(t, origin, local)
				if status["lastSyncError"] == "GITHUB_RATE_LIMITED" {
					if status["lastSuccessfulSyncAt"] != before || status["cacheValid"] != true {
						t.Fatal("startup failure extended or discarded persisted freshness")
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("due startup sync never ran")
				}
				time.Sleep(30 * time.Millisecond)
			}
		})
	}
}

func TestGithubStableAccountReplacementAndRecovery(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			_, key := githubSyntheticKey(t)
			fixture := &githubFixture{key: key}
			githubService(t, fixture)
			path, databaseURL, local := githubHistoricalBrowser(t, engine)
			token := githubSetup(t, path, databaseURL)
			origin := startServer(t, path, databaseURL)
			first := githubLogin(t, origin, "setup", token)
			// Renaming the same account must preserve access; a different stable ID must fail.
			fixture.mu.Lock()
			fixture.login = "renamed-owner"
			fixture.mu.Unlock()
			second := githubLogin(t, origin, "login", "")
			if githubStatus(t, origin, second)["binding"].(map[string]any)["username"] != "renamed-owner" {
				t.Fatal("stable account rename was not retained")
			}
			fixture.mu.Lock()
			fixture.accountID = 99
			fixture.mu.Unlock()
			target, state := githubOptions(t, origin, "login", "")
			res := githubRequest(t, origin, "GET", "/callback?code=synthetic-code&state="+target.Query().Get("state"), nil, state)
			if res.StatusCode != 303 || res.Header.Get("Location") != "/?authError=AUTH_INVALID" {
				t.Fatal("different account was accepted as owner")
			}
			replacement := githubLogin(t, origin, "replace", "", local)
			if githubStatus(t, origin, replacement)["binding"].(map[string]any)["accountId"] != "99" {
				t.Fatal("replacement did not change stable identity")
			}
			res = githubRequest(t, origin, "GET", "", nil, first)
			if res.StatusCode != 401 {
				t.Fatal("replacement retained previous GitHub sessions")
			}
			res = githubRequest(t, origin, "GET", "", nil, second)
			if res.StatusCode != 401 {
				t.Fatal("replacement retained renamed-account sessions")
			}
			status, _ := fetchRecord(t, origin, restoredToken)
			if status != 200 {
				t.Fatal("replacement revoked independent credentials")
			}
			cmd := exec.Command(binary, "recover")
			cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_PATH="+path, "SCRATCHPAD_DATABASE_URL="+databaseURL)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal("recovery command failed")
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			recovered := githubLogin(t, origin, "recover", lines[len(lines)-1])
			if githubStatus(t, origin, recovered)["binding"] == nil {
				t.Fatal("recovery failed to establish owner access")
			}
			status, _ = fetchRecord(t, origin, restoredToken)
			if status != 401 {
				t.Fatal("recovery retained a pre-recovery session")
			}
			res = githubRequest(t, origin, "GET", "", nil, local)
			if res.StatusCode != 401 {
				t.Fatal("recovery retained independent browser session")
			}
		})
	}
}

func TestGithubCallbackRechecksRevokedAuthorization(t *testing.T) {
	_, key := githubSyntheticKey(t)
	fixture := &githubFixture{key: key}
	entered, release := make(chan struct{}), make(chan struct{})
	var pause atomic.Bool
	githubService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" && pause.Load() {
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
		}
		fixture.ServeHTTP(w, r)
	}))
	path, _, local := githubHistoricalBrowser(t, "sqlite")
	token := githubSetup(t, path, "")
	origin := startServer(t, path, "")
	session := githubLogin(t, origin, "setup", token)
	target, state := githubOptions(t, origin, "replace", "", local)
	pause.Store(true)
	req, _ := http.NewRequest("GET", origin+"/api/v1/auth/github/callback?code=synthetic-code&state="+target.Query().Get("state"), nil)
	req.AddCookie(state)
	result := make(chan *http.Response, 1)
	errs := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := client.Do(req)
		if err != nil {
			errs <- err
		} else {
			result <- res
		}
	}()
	select {
	case <-entered:
	case <-time.After(6 * time.Second):
		t.Fatal("OAuth exchange did not reach the external boundary")
	}
	logout, _ := http.NewRequest("POST", origin+"/api/v1/auth/logout", nil)
	logout.Header.Set("Origin", origin)
	logout.AddCookie(local)
	res, err := http.DefaultClient.Do(logout)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		close(release)
		t.Fatal("logout was blocked by the OAuth network request")
	}
	close(release)
	select {
	case res := <-result:
		defer res.Body.Close()
		if res.StatusCode != 303 || res.Header.Get("Location") != "/?authError=AUTH_INVALID" {
			t.Fatal("revoked initiating browser still replaced the binding")
		}
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(6 * time.Second):
		t.Fatal("callback did not finish")
	}
	if githubStatus(t, origin, session)["binding"].(map[string]any)["accountId"] != "42" {
		t.Fatal("failed replacement changed the binding")
	}
}

func TestGithubRejectsMalformedAccountAuthority(t *testing.T) {
	for _, account := range []string{`{"id":"42","login":"owner-example","type":"User"}`, `{"id":42,"login":"bad--name","type":"User"}`, `{"id":42,"login":"owner-example","type":"Organization"}`} {
		t.Run("invalid-account", func(t *testing.T) {
			githubService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/login/oauth/access_token" {
					io.WriteString(w, `{"access_token":"synthetic-oauth-token","token_type":"bearer"}`)
				} else {
					io.WriteString(w, account)
				}
			}))
			dir := t.TempDir()
			os.Chmod(dir, 0700)
			path := filepath.Join(dir, "owner.sqlite")
			token := githubSetup(t, path, "")
			origin := startServer(t, path, "")
			target, state := githubOptions(t, origin, "setup", token)
			res := githubRequest(t, origin, "GET", "/callback?code=synthetic-code&state="+target.Query().Get("state"), nil, state)
			if res.StatusCode != 303 || res.Header.Get("Location") != "/?authError=GITHUB_UNAVAILABLE" {
				t.Fatalf("malformed account became owner authority: %d", res.StatusCode)
			}
		})
	}
}
