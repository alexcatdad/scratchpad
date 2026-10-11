package backend_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func credentialRequest(t *testing.T, origin, method, route string, body any, cookie *http.Cookie) (int, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, origin+"/api/v1/auth/credentials"+route, bytes.NewReader(data))
	req.Header.Set("Origin", origin)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var result map[string]any
	if json.NewDecoder(res.Body).Decode(&result) != nil {
		t.Fatalf("non-JSON credential response: %d", res.StatusCode)
	}
	return res.StatusCode, result
}
func enrollmentProof(t *testing.T, origin, private, key string, challenge map[string]any, purpose string) map[string]any {
	t.Helper()
	payload, _ := json.Marshal([]any{"scratchpad-ssh-proof", 2, origin, purpose, challenge["challengeId"], challenge["nonce"], key, challenge["expiresAt"]})
	signer := exec.Command("ssh-keygen", "-Y", "sign", "-f", private, "-n", "scratchpad-auth-v2")
	signer.Stdin = bytes.NewReader(payload)
	signature, err := signer.Output()
	if err != nil {
		t.Fatal("synthetic enrollment signing failed")
	}
	return map[string]any{"challengeId": challenge["challengeId"], "publicKey": key, "signature": string(signature)}
}
func enrollLocalSSH(t *testing.T, origin, private, key string, local *http.Cookie) string {
	t.Helper()
	status, challenge := credentialRequest(t, origin, "POST", "/challenge", map[string]any{"publicKey": key, "label": "Synthetic agent key"}, local)
	if status != 200 {
		t.Fatalf("SSH enrollment challenge unavailable: %d", status)
	}
	if challenge["purpose"] != "ssh_enroll" || challenge["recipient"] != origin || challenge["version"] != float64(2) {
		t.Fatal("enrollment proof context is incorrect")
	}
	wrong := enrollmentProof(t, origin, private, key, challenge, "ssh_login")
	status, _ = credentialRequest(t, origin, "POST", "/verify", wrong, local)
	if status != 401 {
		t.Fatal("login proof was accepted for enrollment")
	}
	proof := enrollmentProof(t, origin, private, key, challenge, "ssh_enroll")
	status, result := credentialRequest(t, origin, "POST", "/verify", proof, local)
	if status != 200 || result["credentialId"] == nil || result["accessToken"] != nil {
		t.Fatalf("enrollment did not create a credential: %d", status)
	}
	status, _ = credentialRequest(t, origin, "POST", "/verify", proof, local)
	if status != 401 {
		t.Fatal("enrollment challenge was reused")
	}
	return result["credentialId"].(string)
}
func TestLocalSSHEnrollmentAllowsRealAgentRead(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, local := githubHistoricalBrowser(t, engine)
			private, key := githubSyntheticKey(t)
			origin := startServer(t, path, databaseURL)
			enrollLocalSSH(t, origin, private, key, local)
			cmd := exec.Command(integrationBinary, "-url", origin, "-public-key", private+".pub", "-signing-key", private, "-workspace", filepath.Dir(private), "-binary", mcpBinary, "-phase", "detail", "-record-id", "rec_restored")
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "PASS detail") {
				t.Fatal("enrolled SSH credential did not allow real stdio MCP retrieval", err)
			}
		})
	}
}

func TestCredentialRevocationStopsAgentAccess(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, local := githubHistoricalBrowser(t, engine)
			private, key := githubSyntheticKey(t)
			origin := startServer(t, path, databaseURL)
			id := enrollLocalSSH(t, origin, private, key, local)
			_, proof := signedChallenge(t, origin, private, key)
			status, session := postJSON(t, origin, "/api/v1/auth/mcp/verify", proof)
			if status != 200 {
				t.Fatal("agent login failed")
			}
			status, _ = credentialRequest(t, origin, "DELETE", "/"+id, nil, local)
			if status != 200 {
				t.Fatalf("credential revocation unavailable: %d", status)
			}
			status, _ = fetchRecord(t, origin, session["accessToken"].(string))
			if status != 401 {
				t.Fatal("revoked credential retained a live agent session")
			}
			status, _ = postJSON(t, origin, "/api/v1/auth/mcp/challenge", map[string]any{"publicKey": key})
			if status != 401 {
				t.Fatal("revoked credential could log in again")
			}
			status, body := credentialRequest(t, origin, "GET", "", nil, local)
			if status != 200 {
				t.Fatal("credential history disappeared")
			}
			found := false
			for _, entry := range body["credentials"].([]any) {
				credential := entry.(map[string]any)
				if credential["id"] == id {
					found = credential["revokedAt"] != nil && credential["source"] == "local" && credential["label"] == "Synthetic agent key"
					if credential["publicKey"] != nil {
						t.Fatal("public list leaked credential material")
					}
				}
			}
			if !found {
				t.Fatal("revocation discarded credential provenance")
			}
		})
	}
}

func TestEnrollmentRequiresLiveBrowserAndPreservesGithubAuthority(t *testing.T) {
	path, _, local := githubHistoricalBrowser(t, "sqlite")
	private, key := githubSyntheticKey(t)
	fixture := &githubFixture{key: key}
	githubService(t, fixture)
	token := githubSetup(t, path, "")
	origin := startServer(t, path, "")
	githubLogin(t, origin, "setup", token)
	status, challenge := credentialRequest(t, origin, "POST", "/challenge", map[string]any{"publicKey": key}, local)
	if status != 200 {
		t.Fatal("enrollment challenge unavailable")
	}
	proof := enrollmentProof(t, origin, private, key, challenge, "ssh_enroll")
	status, _ = credentialRequest(t, origin, "POST", "/verify", proof, local)
	if status != 409 {
		t.Fatal("published GitHub key became an independent credential")
	}
	_, loginProof := signedChallenge(t, origin, private, key)
	status, _ = postJSON(t, origin, "/api/v1/auth/mcp/verify", loginProof)
	if status != 200 {
		t.Fatal("GitHub key login failed")
	}
	status, list := credentialRequest(t, origin, "GET", "", nil, local)
	if status != 200 {
		t.Fatal("credential list unavailable")
	}
	for _, entry := range list["credentials"].([]any) {
		credential := entry.(map[string]any)
		if credential["source"] == "github" {
			status, _ = credentialRequest(t, origin, "DELETE", "/"+credential["id"].(string), nil, local)
			if status != 409 {
				t.Fatal("managed credential could bypass GitHub blocking/unlinking")
			}
		}
	}
	otherPrivate, otherKey := githubSyntheticKey(t)
	status, challenge = credentialRequest(t, origin, "POST", "/challenge", map[string]any{"publicKey": otherKey}, local)
	if status != 200 {
		t.Fatal("independent challenge unavailable")
	}
	proof = enrollmentProof(t, origin, otherPrivate, otherKey, challenge, "ssh_enroll")
	logout, _ := http.NewRequest("POST", origin+"/api/v1/auth/logout", nil)
	logout.Header.Set("Origin", origin)
	logout.AddCookie(local)
	res, err := http.DefaultClient.Do(logout)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("logout failed")
	}
	status, _ = credentialRequest(t, origin, "POST", "/verify", proof, local)
	if status != 401 {
		t.Fatal("revoked browser authorization still enrolled an SSH key")
	}
	status, _ = postJSON(t, origin, "/api/v1/auth/mcp/challenge", map[string]any{"publicKey": otherKey})
	if status != 401 {
		t.Fatal("unauthorized enrollment created a credential")
	}
}
func TestConcurrentRevocationRetainsLastIndependentPasskey(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, local := githubHistoricalBrowser(t, engine)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"historical_passkey_a", "historical_passkey_b"} {
				row, _ := json.Marshal(map[string]any{"id": id, "version": 1, "kind": "webauthn", "source": "local", "createdAt": "2026-10-01T00:00:00Z"})
				if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('credential',$1,$2,1,'2026-10-01T00:00:00Z')`, id, string(row)); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			db.Close()
			origin := startServer(t, path, databaseURL)
			codes := make(chan int, 2)
			errors := make(chan error, 2)
			for _, id := range []string{"historical_passkey_a", "historical_passkey_b"} {
				go func() {
					req, _ := http.NewRequest("DELETE", origin+"/api/v1/auth/credentials/"+id, nil)
					req.Header.Set("Origin", origin)
					req.AddCookie(local)
					res, err := http.DefaultClient.Do(req)
					if err != nil {
						errors <- err
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
				case err := <-errors:
					t.Fatal(err)
				}
			}
			if counts[200] != 1 || counts[409] != 1 {
				t.Fatal("concurrent revocation removed every independent passkey", counts)
			}
		})
	}
}
