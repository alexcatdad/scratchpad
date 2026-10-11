package backend_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func enrolledSSHKey(t *testing.T, path string) (string, string) {
	t.Helper()
	private := filepath.Join(t.TempDir(), "identity")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", private).CombinedOutput(); err != nil {
		t.Fatalf("synthetic key generation failed: %v %s", err, out)
	}
	public, err := os.ReadFile(private + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Join(strings.Fields(string(public))[:2], " ")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedRestoredHistory(t, db)
	if _, err := db.Exec(`UPDATE entities SET data=json_set(data,'$.publicKey',$1) WHERE kind='credential'`, key); err != nil {
		t.Fatal(err)
	}
	return private, key
}
func postJSON(t *testing.T, origin, path string, body any) (int, map[string]any) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(origin+path, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var value map[string]any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatalf("non-JSON HTTP response (status %d)", response.StatusCode)
	}
	return response.StatusCode, value
}
func signedChallenge(t *testing.T, origin, private, key string) (map[string]any, map[string]any) {
	t.Helper()
	status, challenge := postJSON(t, origin, "/api/v1/auth/mcp/challenge", map[string]string{"publicKey": key})
	if status != 200 {
		t.Fatalf("challenge failed: %d %+v", status, challenge)
	}
	if challenge["version"] != float64(2) || challenge["namespace"] != "scratchpad-auth-v2" || challenge["purpose"] != "ssh_login" || challenge["recipient"] != origin {
		t.Fatalf("untrusted proof context: %+v", challenge)
	}
	proof, err := json.Marshal([]any{"scratchpad-ssh-proof", 2, origin, "ssh_login", challenge["challengeId"], challenge["nonce"], key, challenge["expiresAt"]})
	if err != nil {
		t.Fatal(err)
	}
	signer := exec.Command("ssh-keygen", "-Y", "sign", "-f", private, "-n", "scratchpad-auth-v2")
	signer.Stdin = bytes.NewReader(proof)
	signature, err := signer.Output()
	if err != nil {
		t.Fatal("synthetic SSH signing failed")
	}
	return challenge, map[string]any{"challengeId": challenge["challengeId"], "publicKey": key, "signature": string(signature)}
}
func TestSSHLoginAndSingleUseProof(t *testing.T) {
	path := legacyFixture(t)
	private, key := enrolledSSHKey(t, path)
	origin := startServer(t, path, "")
	_, proof := signedChallenge(t, origin, private, key)
	status, response := postJSON(t, origin, "/api/v1/auth/mcp/verify", proof)
	if status != 200 {
		t.Fatalf("SSH proof rejected: %d %+v", status, response)
	}
	token, ok := response["accessToken"].(string)
	if !ok || token == "" {
		t.Fatal("SSH session missing")
	}
	status, record := fetchRecord(t, origin, token)
	if status != 200 || record["record"].(map[string]any)["id"] != "rec_restored" {
		t.Fatal("new SSH session cannot retrieve restored capture")
	}
	status, response = postJSON(t, origin, "/api/v1/auth/mcp/verify", proof)
	if status != 401 || response["error"].(map[string]any)["code"] != "AUTH_CHALLENGE_USED" {
		t.Fatalf("SSH proof reused: %d %+v", status, response)
	}
	// Session responses and error bodies must not expose locally held key material.
	if strings.Contains(token, "PRIVATE KEY") {
		t.Fatal("private key appeared in response")
	}
}

func TestSSHSignatureIsBoundToThisInstance(t *testing.T) {
	path := legacyFixture(t)
	private, key := enrolledSSHKey(t, path)
	origin := startServer(t, path, "")
	challenge, valid := signedChallenge(t, origin, private, key)
	forged, err := json.Marshal([]any{"scratchpad-ssh-proof", 2, "https://other.example.test", "ssh_login", challenge["challengeId"], challenge["nonce"], key, challenge["expiresAt"]})
	if err != nil {
		t.Fatal(err)
	}
	signer := exec.Command("ssh-keygen", "-Y", "sign", "-f", private, "-n", "scratchpad-auth-v2")
	signer.Stdin = bytes.NewReader(forged)
	signature, err := signer.Output()
	if err != nil {
		t.Fatal(err)
	}
	wrong := map[string]any{"challengeId": challenge["challengeId"], "publicKey": key, "signature": string(signature)}
	status, response := postJSON(t, origin, "/api/v1/auth/mcp/verify", wrong)
	if status != 401 || response["error"].(map[string]any)["code"] != "AUTH_INVALID" {
		t.Fatal("transplanted signature accepted")
	}
	status, response = postJSON(t, origin, "/api/v1/auth/mcp/verify", valid)
	if status != 200 {
		t.Fatalf("failed signature consumed the valid challenge: %d %+v", status, response)
	}
}

func TestRealStdioMCPReadsThroughGoSSHLogin(t *testing.T) {
	path := legacyFixture(t)
	private, _ := enrolledSSHKey(t, path)
	origin := startServer(t, path, "")
	cmd := exec.Command(integrationBinary, "-url", origin, "-public-key", private+".pub", "-signing-key", private, "-workspace", filepath.Dir(private), "-binary", mcpBinary, "-phase", "detail", "-record-id", "rec_restored")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real stdio MCP failed: %v %s", err, out)
	}
	if !strings.Contains(string(out), "PASS detail") {
		t.Fatalf("detail check did not complete: %s", out)
	}
}

func TestConcurrentSSHProofCreatesOneSession(t *testing.T) {
	path := legacyFixture(t)
	private, key := enrolledSSHKey(t, path)
	origin := startServer(t, path, "")
	_, proof := signedChallenge(t, origin, private, key)
	data, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	statuses := make(chan int, 4)
	for range 4 {
		go func() {
			response, err := http.Post(origin+"/api/v1/auth/mcp/verify", "application/json", bytes.NewReader(data))
			if err != nil {
				statuses <- 0
				return
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			statuses <- response.StatusCode
		}()
	}
	succeeded := 0
	for range 4 {
		status := <-statuses
		if status == 200 {
			succeeded++
		} else if status != 401 && status != 429 {
			t.Errorf("unexpected concurrent proof response: %d", status)
		}
	}
	if succeeded != 1 {
		t.Fatalf("single-use proof created %d successful sessions", succeeded)
	}
}

func TestGitHubSSHAuthorizationCacheAndBlocking(t *testing.T) {
	for _, variant := range []string{"fresh", "expired", "future", "removed", "generation", "blocked", "unregistered published key"} {
		t.Run(variant, func(t *testing.T) {
			path := legacyFixture(t)
			private, key := enrolledSSHKey(t, path)
			lastSync := time.Now().Add(-time.Minute)
			if variant == "expired" {
				lastSync = time.Now().Add(-25 * time.Hour)
			}
			if variant == "future" {
				lastSync = time.Now().Add(time.Hour)
			}
			keys := []map[string]string{{"publicKey": key}}
			if variant == "removed" {
				keys = nil
			}
			binding := map[string]any{"id": "owner", "generation": 1, "lastSuccessfulSyncAt": lastSync.UTC().Format(time.RFC3339Nano), "keys": keys}
			raw, err := json.Marshal(binding)
			if err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO entities VALUES('github_binding','owner',$1,1,'2026-10-01T00:00:00Z')`, string(raw)); err != nil {
				t.Fatal(err)
			}
			generation := 1
			if variant == "generation" {
				generation = 2
			}
			if _, err := db.Exec(`UPDATE entities SET data=json_set(data,'$.source','github','$.generation',$1) WHERE kind='credential'`, generation); err != nil {
				t.Fatal(err)
			}
			if variant == "blocked" {
				fingerprintOut, err := exec.Command("ssh-keygen", "-l", "-f", private+".pub").Output()
				if err != nil {
					t.Fatal(err)
				}
				fingerprint := strings.Fields(string(fingerprintOut))[1]
				if _, err := db.Exec(`INSERT INTO entities VALUES('github_block',$1,'{}',1,'2026-10-01T00:00:00Z')`, fingerprint); err != nil {
					t.Fatal(err)
				}
			}
			if variant == "unregistered published key" {
				if _, err := db.Exec(`DELETE FROM entities WHERE kind='credential'`); err != nil {
					t.Fatal(err)
				}
			}
			db.Close()
			origin := startServer(t, path, "")
			if variant == "fresh" || variant == "unregistered published key" {
				_, proof := signedChallenge(t, origin, private, key)
				status, response := postJSON(t, origin, "/api/v1/auth/mcp/verify", proof)
				if status != 200 {
					t.Fatalf("eligible GitHub proof rejected: %d %+v", status, response)
				}
				token, _ := response["accessToken"].(string)
				status, _ = fetchRecord(t, origin, token)
				if status != 200 {
					t.Fatal("GitHub session cannot read restored capture")
				}
			} else {
				status, response := postJSON(t, origin, "/api/v1/auth/mcp/challenge", map[string]string{"publicKey": key})
				if status != 401 || response["error"].(map[string]any)["code"] != "AUTH_INVALID" {
					t.Fatalf("invalid GitHub cache accepted: %d %+v", status, response)
				}
			}
		})
	}
}

func TestSSHChallengeSurvivesServerRestart(t *testing.T) {
	path := legacyFixture(t)
	private, key := enrolledSSHKey(t, path)
	var origin string
	var proof map[string]any
	t.Run("challenge", func(t *testing.T) {
		origin = startServer(t, path, "")
		_, proof = signedChallenge(t, origin, private, key)
	})
	t.Run("verify after restart", func(t *testing.T) {
		restarted := startServer(t, path, "", strings.TrimPrefix(origin, "http://"))
		status, response := postJSON(t, restarted, "/api/v1/auth/mcp/verify", proof)
		if status != 200 {
			t.Fatalf("persisted challenge was lost: %d %+v", status, response)
		}
	})
}

func TestSSHRecipientUsesCanonicalPublicOrigin(t *testing.T) {
	path := legacyFixture(t)
	_, key := enrolledSSHKey(t, path)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	origin := startServer(t, path, "", address, "https://SCRATCHPAD.example:443/")
	status, response := postJSON(t, origin, "/api/v1/auth/mcp/challenge", map[string]string{"publicKey": key})
	if status != 200 || response["recipient"] != "https://scratchpad.example" {
		t.Fatalf("noncanonical trusted recipient: %d %+v", status, response)
	}
}
