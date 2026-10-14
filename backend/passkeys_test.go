package backend_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	binaryencoding "encoding/binary"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

func TestOwnerSetupAndRecoveryCLI(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "owner.sqlite")
	run := func(command string) (string, error) {
		cmd := exec.Command(binary, command)
		cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL=", "SCRATCHPAD_DATABASE_PATH="+path)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := run("recover"); err == nil {
		t.Fatal("uninitialized recovery was accepted")
	}
	first, err := run("setup")
	if err != nil {
		t.Fatalf("setup command unavailable: %v", err)
	}
	second, err := run("setup")
	if err != nil || first == second {
		t.Fatal("setup did not rotate its one-use capability")
	}
	origin := startServer(t, path, "")
	res, err := http.Get(origin + "/api/v1/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var status struct{ Initialized, Authenticated, GithubConfigured bool }
	if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&status) != nil || status.Initialized || status.Authenticated {
		t.Fatal("fresh installation status is incorrect")
	}
}

// Historical input uses the TypeScript COSE-key row shape. Signatures are made
// with standard crypto primitives, independently of the server's WebAuthn code.
func TestHistoricalPasskeyLoginAndProofFailures(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, driver := "", "", "sqlite"
			if engine == "sqlite" {
				path = legacyFixture(t)
			} else {
				databaseURL = legacyPostgres(t)
				driver = "pgx"
			}
			private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			// COSE EC2 / ES256 / P-256, with fixed-width affine coordinates.
			key, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: private.X.FillBytes(make([]byte, 32)), -3: private.Y.FillBytes(make([]byte, 32))})
			if err != nil {
				t.Fatal(err)
			}
			credentialID := base64.RawURLEncoding.EncodeToString([]byte("synthetic-historical-passkey"))
			dsn := path
			if databaseURL != "" {
				dsn = databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			for _, fixture := range []struct {
				kind, id string
				value    map[string]any
			}{
				{"owner", "owner", map[string]any{"userId": "scratchpad-owner", "displayName": "Owner"}},
				{"credential", "cred_legacy", map[string]any{"kind": "webauthn", "label": "Historical passkey", "webauthnId": credentialID, "publicKey": base64.RawURLEncoding.EncodeToString(key), "counter": 5}},
				{"challenge", "challenge_expired", map[string]any{"kind": "login", "nonce": "expired", "expiresAt": "2026-01-01T00:00:00.000Z"}},
			} {
				fixture.value["id"], fixture.value["version"], fixture.value["createdAt"] = fixture.id, 1, "2026-10-01T00:00:00.000Z"
				data, _ := json.Marshal(fixture.value)
				if _, err := db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES($1,$2,$3,1,$4)`, fixture.kind, fixture.id, string(data), "2026-10-01T00:00:00.000Z"); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			db.Close()
			serverOrigin := startServer(t, path, databaseURL, "127.0.0.1:0", "http://localhost:3000")
			for _, body := range []string{"", "{invalid"} {
				request, _ := http.NewRequest("POST", serverOrigin+"/api/v1/auth/login/options", strings.NewReader(body))
				request.Header.Set("Origin", "http://localhost:3000")
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				var result map[string]any
				json.NewDecoder(response.Body).Decode(&result)
				response.Body.Close()
				if body == "" && response.StatusCode != 200 {
					t.Fatal("bodyless login options regressed")
				}
				if body != "" && (response.StatusCode != 400 || result["error"].(map[string]any)["code"] != "VALIDATION_FAILED") {
					t.Fatal("invalid JSON error contract regressed")
				}
			}
			post := func(route string, body any, origin string) (int, map[string]any, *http.Response) {
				data, _ := json.Marshal(body)
				request, _ := http.NewRequest("POST", serverOrigin+"/api/v1/auth/"+route, bytes.NewReader(data))
				request.Header.Set("Origin", origin)
				request.Header.Set("Content-Type", "application/json")
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				var result map[string]any
				if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
					t.Fatal("non-JSON passkey response")
				}
				return response.StatusCode, result, response
			}
			status, rejected, _ := post("login/options", map[string]any{}, "http://attacker.invalid")
			if status != 403 || rejected["error"].(map[string]any)["code"] != "AUTH_INVALID" {
				t.Fatal("wrong request Origin accepted")
			}
			status, options, _ := post("login/options", map[string]any{}, "http://localhost:3000")
			if status != 200 {
				t.Fatalf("historical owner login options: %d", status)
			}
			challenge := options["options"].(map[string]any)["challenge"].(string)
			sign := func(count uint32, flags byte, origin, rpID, nonce string) map[string]any {
				clientData, _ := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": nonce, "origin": origin, "crossOrigin": false})
				rpHash := sha256.Sum256([]byte(rpID))
				clientHash := sha256.Sum256(clientData)
				authData := append(append([]byte{}, rpHash[:]...), flags)
				authData = binaryencoding.BigEndian.AppendUint32(authData, count)
				payload := append(append([]byte{}, authData...), clientHash[:]...)
				digest := sha256.Sum256(payload)
				signature, err := ecdsa.SignASN1(rand.Reader, private, digest[:])
				if err != nil {
					t.Fatal(err)
				}
				return map[string]any{"id": credentialID, "rawId": credentialID, "type": "public-key", "clientExtensionResults": map[string]any{}, "response": map[string]any{"clientDataJSON": base64.RawURLEncoding.EncodeToString(clientData), "authenticatorData": base64.RawURLEncoding.EncodeToString(authData), "signature": base64.RawURLEncoding.EncodeToString(signature), "userHandle": base64.RawURLEncoding.EncodeToString([]byte("scratchpad-owner"))}}
			}
			status, expired, _ := post("login/verify", map[string]any{"challengeId": "challenge_expired", "response": sign(6, 5, "http://localhost:3000", "localhost", challenge)}, "http://localhost:3000")
			if status != 401 || expired["error"].(map[string]any)["code"] != "AUTH_INVALID" {
				t.Fatal("expired challenge was not retired when issuing a new challenge")
			}
			for _, proof := range []map[string]any{
				sign(6, 1, "http://localhost:3000", "localhost", challenge), // No user verification.
				sign(6, 5, "https://attacker.invalid", "localhost", challenge),
				sign(6, 5, "http://localhost:3000", "attacker.invalid", challenge),
				sign(6, 5, "http://localhost:3000", "localhost", "wrong-challenge"),
				sign(5, 5, "http://localhost:3000", "localhost", challenge), // Counter rollback.
			} {
				status, _, _ := post("login/verify", map[string]any{"challengeId": options["challengeId"], "response": proof}, "http://localhost:3000")
				if status != 401 {
					t.Fatalf("invalid passkey proof accepted: %d", status)
				}
			}
			proof := map[string]any{"challengeId": options["challengeId"], "response": sign(6, 5, "http://localhost:3000", "localhost", challenge)}
			status, result, response := post("login/verify", proof, "http://localhost:3000")
			if status != 200 || result["authenticated"] != true {
				t.Fatalf("valid historical passkey rejected: %d", status)
			}
			if _, ok := result["accessToken"]; ok {
				t.Fatal("browser token leaked in JSON")
			}
			cookies := response.Cookies()
			if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].MaxAge != 86400 {
				t.Fatal("browser cookie protection lost")
			}
			request, _ := http.NewRequest("GET", serverOrigin+"/api/v1/auth/status", nil)
			request.AddCookie(cookies[0])
			res, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			var current map[string]any
			json.NewDecoder(res.Body).Decode(&current)
			res.Body.Close()
			if current["authenticated"] != true {
				t.Fatal("historical passkey session cannot authenticate")
			}
			request, _ = http.NewRequest("GET", serverOrigin+"/api/v1/auth/status", nil)
			request.Header.Set("Authorization", "Bearer "+cookies[0].Value)
			res, err = http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			json.NewDecoder(res.Body).Decode(&current)
			res.Body.Close()
			if current["authenticated"] != false {
				t.Fatal("browser session accepted as bearer")
			}
			status, result, _ = post("login/verify", proof, "http://localhost:3000")
			if status != 401 || result["error"].(map[string]any)["code"] != "AUTH_CHALLENGE_USED" {
				t.Fatal("passkey assertion replayed")
			}
			_, next, _ := post("login/options", map[string]any{}, "http://localhost:3000")
			nextProof := map[string]any{"challengeId": next["challengeId"], "response": sign(7, 5, "http://localhost:3000", "localhost", next["options"].(map[string]any)["challenge"].(string))}
			results := make(chan int, 2)
			payload, _ := json.Marshal(nextProof)
			for range 2 {
				go func() {
					request, err := http.NewRequest("POST", serverOrigin+"/api/v1/auth/login/verify", bytes.NewReader(payload))
					if err != nil {
						results <- -1
						return
					}
					request.Header.Set("Origin", "http://localhost:3000")
					response, err := http.DefaultClient.Do(request)
					if err != nil {
						results <- -1
						return
					}
					response.Body.Close()
					results <- response.StatusCode
				}()
			}
			first, second := <-results, <-results
			if !((first == 200 && second == 401) || (first == 401 && second == 200)) {
				t.Fatalf("concurrent passkey consumption: %d %d", first, second)
			}
		})
	}
}

func TestPasskeySupportPreservesLocalKeyBlocking(t *testing.T) {
	path := legacyFixture(t)
	private, _ := enrolledSSHKey(t, path)
	fingerprint, err := exec.Command("ssh-keygen", "-l", "-f", private+".pub").Output()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE entities SET data=json_set(data,'$.kind','local_key') WHERE kind='credential'`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO entities VALUES('github_block',$1,'{}',1,'2026-10-01T00:00:00Z')`, strings.Fields(string(fingerprint))[1]); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	origin := startServer(t, path, "")
	status, _ := fetchRecord(t, origin, restoredToken)
	if status != 401 {
		t.Fatal("adding passkey support bypassed restored local-key blocking")
	}
}
