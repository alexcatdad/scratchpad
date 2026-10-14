package backend_test

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func syntheticEncryptionKey(t *testing.T, value byte) string {
	t.Helper()
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	path := filepath.Join(dir, "installation-key")
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProviderConfigurationChangeStopsFutureRequestsWithoutHoldingWrites(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", syntheticEncryptionKey(t, 44))
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var completions atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/embeddings" {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
			return
		}
		completions.Add(1)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
	}))
	defer func() { unblock(); provider.Close() }()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "apiKey": "synthetic-original-dispatch", "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("provider fixture failed")
	}
	done := make(chan *http.Response, 1)
	errors := make(chan error, 1)
	go func() {
		request, _ := http.NewRequest("POST", origin+"/api/v1/ai/test", strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer "+restoredToken)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			errors <- err
			return
		}
		done <- response
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider was not called")
	}
	request, _ := http.NewRequest("PATCH", origin+"/api/v1/ai/settings", strings.NewReader(`{"expectedVersion":1,"apiKey":"synthetic-new-dispatch"}`))
	request.Header.Set("Authorization", "Bearer "+restoredToken)
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		unblock()
		t.Fatal("provider wait held configuration writes")
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		unblock()
		t.Fatal("credential-only update failed")
	}
	unblock()
	select {
	case response = <-done:
		defer response.Body.Close()
		var result map[string]any
		json.NewDecoder(response.Body).Decode(&result)
		if response.StatusCode != 503 || result["error"].(map[string]any)["code"] != "AI_CONFIGURATION_CHANGED" || completions.Load() != 0 {
			t.Fatal("changed generation permitted a later provider request")
		}
	case err := <-errors:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("outdated provider test did not complete")
	}
}

func TestLegacyProviderSecretConversionIsPendingUntilProtectedKeyAvailable(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			row := map[string]any{"id": "global", "version": 1, "createdAt": "2026-10-01T00:00:00Z", "enabled": false, "apiKey": "synthetic-legacy-provider-token-to-convert"}
			encoded, _ := json.Marshal(row)
			if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_settings','global',$1,1,'2026-10-01T00:00:00Z')`, string(encoded)); err != nil {
				t.Fatal(err)
			}
			db.Close()
			key := syntheticEncryptionKey(t, 77)
			for _, phase := range []string{"pending", "converted", "restart"} {
				t.Run(phase, func(t *testing.T) {
					file := ""
					if phase != "pending" {
						file = key
					}
					t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", file)
					origin := startServer(t, path, databaseURL)
					status, config := curationRequest(t, origin, "GET", "/ai/settings", nil)
					if status != 200 {
						t.Fatal("legacy configuration unreadable")
					}
					want, version := "pending_conversion", float64(1)
					if phase != "pending" {
						want, version = "protected", 2
					}
					if config["secretProtection"].(map[string]any)["status"] != want || config["version"] != version {
						t.Fatal("legacy conversion was neither tracked nor completed once")
					}
					status, archive := curationRequest(t, origin, "POST", "/export", map[string]any{})
					if status != 200 {
						t.Fatal("memory unavailable during conversion")
					}
					encoded, _ := json.Marshal(archive)
					if strings.Contains(string(encoded), "synthetic-legacy-provider-token-to-convert") {
						t.Fatal("legacy conversion exposed plaintext in audits or export")
					}
				})
			}
		})
	}
}

func TestEncryptedProviderSecretSupportsRestartAndBlocksWrongKeys(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			secret := "synthetic-token-for-provider-dispatch-only"
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer "+secret {
					http.Error(w, "synthetic secret must not leak", 401)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/embeddings":
					w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
				case "/v1/chat/completions":
					w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer provider.Close()
			path, databaseURL := portableInstance(t, engine)
			key := syntheticEncryptionKey(t, 12)
			for _, phase := range []string{"configured", "restart", "wrong-key", "missing-key"} {
				t.Run(phase, func(t *testing.T) {
					file := key
					if phase == "wrong-key" {
						file = syntheticEncryptionKey(t, 99)
					}
					if phase == "missing-key" {
						file = ""
					}
					t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", file)
					origin := startServer(t, path, databaseURL)
					if phase == "configured" {
						status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "apiKey": secret, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
						if status != 200 {
							t.Fatal("provider configuration failed")
						}
					}
					before := calls.Load()
					status, result := curationRequest(t, origin, "POST", "/ai/test", map[string]any{})
					if phase == "wrong-key" || phase == "missing-key" {
						if status != 503 || result["error"].(map[string]any)["code"] != "SECRET_UNAVAILABLE" || calls.Load() != before {
							t.Fatal("unavailable encryption key permitted provider dispatch")
						}
						status, _ = projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Memory survives " + phase}, restoredToken)
						if status != 200 {
							t.Fatal("secret failure disabled ordinary memory")
						}
					} else if status != 200 || result["ok"] != true || result["dimensions"] != float64(2) || calls.Load() != before+2 {
						t.Fatalf("encrypted provider test failed: %d", status)
					}
				})
			}
		})
	}
}

func TestProjectedKeyMountAndRecoveryByReplacingLostToken(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	key := syntheticEncryptionKey(t, 11)
	os.Chmod(key, 0440)
	mount := filepath.Join(filepath.Dir(key), "projected-key")
	if err := os.Symlink(key, mount); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"projected", "lost", "replace"} {
		t.Run(phase, func(t *testing.T) {
			file := mount
			if phase != "projected" {
				file = syntheticEncryptionKey(t, 22)
			}
			t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", file)
			origin := startServer(t, path, databaseURL)
			if phase == "projected" {
				status, result := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "apiKey": "synthetic-original-token"})
				if status != 200 || result["secretProtection"].(map[string]any)["status"] != "protected" {
					t.Fatal("projected read-only group key was not supported")
				}
			}
			status, config := curationRequest(t, origin, "GET", "/ai/settings", nil)
			if status != 200 {
				t.Fatal("configuration unavailable")
			}
			if phase == "lost" && config["secretProtection"].(map[string]any)["status"] != "unavailable" {
				t.Fatal("wrong retained key was accepted")
			}
			if phase == "replace" {
				status, result := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": config["version"], "apiKey": "synthetic-reentered-token"})
				if status != 200 || result["secretProtection"].(map[string]any)["status"] != "protected" {
					t.Fatal("owner could not replace lost provider credentials")
				}
			}
		})
	}
}

func TestTamperedProviderCiphertextCannotBeUsed(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", syntheticEncryptionKey(t, 33))
			t.Run("configure", func(t *testing.T) {
				origin := startServer(t, path, databaseURL)
				status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "apiKey": "synthetic-tampering-target"})
				if status != 200 {
					t.Fatal("secret fixture failed")
				}
			})
			driver, dsn := "sqlite", path
			statement := `UPDATE entities SET data=json_set(data,'$.apiKey.ciphertext','tampered') WHERE kind='ai_settings' AND id='global'`
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
				statement = `UPDATE entities SET data=jsonb_set(data,'{apiKey,ciphertext}','"tampered"'::jsonb) WHERE kind='ai_settings' AND id='global'`
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			db.Close()
			origin := startServer(t, path, databaseURL)
			status, config := curationRequest(t, origin, "GET", "/ai/settings", nil)
			if status != 200 || config["secretProtection"].(map[string]any)["status"] != "unavailable" {
				t.Fatal("tampered ciphertext was accepted")
			}
			status, result := curationRequest(t, origin, "POST", "/ai/test", map[string]any{})
			if status != 503 || result["error"].(map[string]any)["code"] != "SECRET_UNAVAILABLE" {
				t.Fatal("tampered ciphertext reached a provider")
			}
		})
	}
}

func TestAIConfigurationSupportsIfMatchAndRejectsInvalidChanges(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	request, _ := http.NewRequest("PATCH", origin+"/api/v1/ai/settings", strings.NewReader(`{"model":"header-version-model"}`))
	request.Header.Set("Authorization", "Bearer "+restoredToken)
	request.Header.Set("If-Match", `"0"`)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("If-Match configuration unsupported: %d", response.StatusCode)
	}
	for _, patch := range []map[string]any{{"baseUrl": "https://user:synthetic-secret@example.invalid/v1"}, {"baseUrl": "https://example.invalid/v1?token=synthetic-secret"}, {"embeddingDimensions": 1.5}, {"maxOutputTokens": 255}, {"analysisModes": []any{"unknown"}}, {"apiKeyConfigured": true}} {
		patch["expectedVersion"] = 1
		status, result := curationRequest(t, origin, "PATCH", "/ai/settings", patch)
		if status != 400 {
			t.Fatal("invalid configuration accepted")
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), "synthetic-secret") {
			t.Fatal("configuration validation exposed URL credentials")
		}
	}
	status, result := curationRequest(t, origin, "GET", "/ai/settings", nil)
	if status != 200 || result["version"] != float64(1) || result["model"] != "header-version-model" {
		t.Fatal("invalid changes modified configuration")
	}
}

func TestProviderWithoutTokenWorksWhenKeyConfigurationIsInvalid(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	key := syntheticEncryptionKey(t, 55)
	os.Chmod(key, 0644)
	t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", key)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "unexpected secret", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/embeddings" {
			w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
		} else {
			w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
		}
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "apiKey": "synthetic-refused-token"})
	if status != 503 {
		t.Fatal("unsafe installation-key permissions allowed secret persistence")
	}
	status, result := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "apiKey": "", "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 || result["apiKeyConfigured"] != false {
		t.Fatal("invalid key blocked a provider needing no token")
	}
	status, result = curationRequest(t, origin, "POST", "/ai/test", map[string]any{})
	if status != 200 || result["ok"] != true {
		t.Fatal("secretless provider depended on an installation key")
	}
}

func TestProviderRejectsMissingOrNullEmbeddingFields(t *testing.T) {
	for _, embedding := range []string{`{"data":[{"embedding":[1,0]}]}`, `{"data":[{"index":null,"embedding":[1,0]}]}`, `{"data":[{"index":0,"embedding":[1,null]}]}`} {
		t.Run("invalid-embedding", func(t *testing.T) {
			path, databaseURL := portableInstance(t, "sqlite")
			var completions atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/embeddings" {
					w.Write([]byte(embedding))
				} else {
					completions.Add(1)
					w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
				}
			}))
			defer provider.Close()
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
			if status != 200 {
				t.Fatal("provider fixture failed")
			}
			status, result := curationRequest(t, origin, "POST", "/ai/test", map[string]any{})
			if status != 503 || result["error"].(map[string]any)["code"] != "AI_UNAVAILABLE" || completions.Load() != 0 {
				t.Fatal("malformed embeddings passed provider validation")
			}
		})
	}
}

func TestFailedLegacyConversionRetainsPendingStateAndOrdinaryMemory(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", syntheticEncryptionKey(t, 66))
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			row := `{"id":"global","version":1,"createdAt":"2026-10-01T00:00:00Z","apiKey":"synthetic-conversion-fault-token"}`
			if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_settings','global',$1,1,'2026-10-01T00:00:00Z')`, row); err != nil {
				t.Fatal(err)
			}
			fault := `CREATE TRIGGER conversion_fault BEFORE INSERT ON entities WHEN NEW.kind='audit' AND json_extract(NEW.data,'$.action')='secrets.converted' BEGIN SELECT RAISE(ABORT,'synthetic-private-fault'); END`
			if engine == "postgres" {
				fault = `CREATE FUNCTION conversion_fault_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='audit' AND NEW.data->>'action'='secrets.converted' THEN RAISE EXCEPTION 'synthetic-private-fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER conversion_fault BEFORE INSERT ON entities FOR EACH ROW EXECUTE FUNCTION conversion_fault_fn()`
			}
			if _, err = db.Exec(fault); err != nil {
				t.Fatal(err)
			}
			t.Run("pending", func(t *testing.T) {
				origin := startServer(t, path, databaseURL)
				status, config := curationRequest(t, origin, "GET", "/ai/settings", nil)
				if status != 200 || config["version"] != float64(1) || config["secretProtection"].(map[string]any)["status"] != "pending_conversion" {
					t.Fatal("failed conversion partially committed")
				}
				status, _ = projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Conversion failure leaves memory available"}, restoredToken)
				if status != 200 {
					t.Fatal("conversion failure blocked ordinary memory")
				}
			})
			drop := `DROP TRIGGER conversion_fault`
			if engine == "postgres" {
				drop = `DROP TRIGGER conversion_fault ON entities`
			}
			if _, err = db.Exec(drop); err != nil {
				t.Fatal(err)
			}
			origin := startServer(t, path, databaseURL)
			status, config := curationRequest(t, origin, "GET", "/ai/settings", nil)
			if status != 200 || config["version"] != float64(2) || config["secretProtection"].(map[string]any)["status"] != "protected" {
				t.Fatal("conversion could not recover after a late failure")
			}
		})
	}
}

func TestAISettingsProtectWriteOnlySecretAcrossRestart(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", syntheticEncryptionKey(t, 42))
			secret := "synthetic-provider-token-do-not-store-in-cleartext-123456"
			for _, phase := range []string{"initial", "restart"} {
				t.Run(phase, func(t *testing.T) {
					origin := startServer(t, path, databaseURL)
					status, config := curationRequest(t, origin, "GET", "/ai/settings", nil)
					if status != 200 {
						t.Fatalf("AI settings unavailable: %d", status)
					}
					if phase == "initial" {
						if config["version"] != float64(0) || config["enabled"] != false || config["apiKeyConfigured"] != false {
							t.Fatal("AI defaults changed")
						}
						status, config = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "apiKey": secret, "model": "synthetic-model", "embeddingDimensions": 2})
					}
					if status != 200 || config["version"] != float64(1) || config["apiKeyConfigured"] != true || config["model"] != "synthetic-model" || config["secretProtection"].(map[string]any)["status"] != "protected" {
						t.Fatal("encrypted configuration did not persist")
					}
					encoded, _ := json.Marshal(config)
					if strings.Contains(string(encoded), secret) || config["apiKey"] != nil {
						t.Fatal("public settings exposed a write-only secret")
					}
					status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "apiKey": "synthetic-stale-token"})
					if status != 409 {
						t.Fatal("stale secret update overwrote configuration")
					}
					status, archive := curationRequest(t, origin, "POST", "/export", map[string]any{})
					if status != 200 {
						t.Fatal("knowledge export failed")
					}
					encoded, _ = json.Marshal(archive)
					if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "synthetic-stale-token") {
						t.Fatal("knowledge export or audit exposed provider tokens")
					}
					if engine == "sqlite" {
						dir := t.TempDir()
						os.Chmod(dir, 0700)
						snapshot := filepath.Join(dir, "settings.sqlite")
						if out, err := backupCommand(path, "", snapshot).CombinedOutput(); err != nil {
							t.Fatalf("secret backup failed: %v %s", err, out)
						}
						data, err := os.ReadFile(snapshot)
						if err != nil || bytes.Contains(data, []byte(secret)) {
							t.Fatal("operational snapshot contains plaintext provider token")
						}
					}
				})
			}
		})
	}
}
