package backend_test

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdmittedProviderTLSWaitAllowsWritesAndFencesLaterRequests(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", syntheticEncryptionKey(t, 57))
			started, release := make(chan struct{}), make(chan struct{})
			var startedOnce, releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			var embeddings, completions atomic.Int32
			var oldCredential atomic.Bool
			provider := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/embeddings" {
					embeddings.Add(1)
					oldCredential.Store(r.Header.Get("Authorization") == "Bearer synthetic-admitted-token")
					w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
					return
				}
				completions.Add(1)
				w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
			}))
			provider.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				startedOnce.Do(func() { close(started) })
				select {
				case <-release:
					return nil, nil
				case <-hello.Context().Done():
					return nil, hello.Context().Err()
				}
			}}
			provider.StartTLS()
			defer func() { unblock(); provider.Close() }()
			certificate := filepath.Join(t.TempDir(), "synthetic-provider-ca.pem")
			if err := os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: provider.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SSL_CERT_FILE", certificate)
			origin := startServer(t, path, databaseURL)
			projectID := jobProject(t, origin, "Independent capture during TLS", false, false)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "apiKey": "synthetic-admitted-token", "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2, "requestTimeoutSeconds": 5})
			if status != 200 {
				t.Fatal("provider fixture configuration failed")
			}
			request := func(method, path string, body any, timeout time.Duration) (int, map[string]any, error) {
				data, _ := json.Marshal(body)
				req, _ := http.NewRequest(method, origin+"/api/v1"+path, bytes.NewReader(data))
				req.Header.Set("Authorization", "Bearer "+restoredToken)
				response, err := (&http.Client{Timeout: timeout}).Do(req)
				if err != nil {
					return 0, nil, err
				}
				defer response.Body.Close()
				var result map[string]any
				err = json.NewDecoder(response.Body).Decode(&result)
				return response.StatusCode, result, err
			}
			pending := make(chan struct {
				status int
				body   map[string]any
				err    error
			}, 1)
			go func() {
				status, body, err := request("POST", "/ai/test", map[string]any{}, 8*time.Second)
				pending <- struct {
					status int
					body   map[string]any
					err    error
				}{status, body, err}
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("provider connection did not reach delayed TLS handshake")
			}
			status, _, err := request("PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "apiKey": ""}, 2*time.Second)
			if err != nil || status != 200 {
				t.Fatal("TLS wait held configuration writes")
			}
			status, _, err = request("POST", "/records", map[string]any{"projectId": projectID, "record": map[string]any{"type": "finding", "title": "Capture while TLS waits", "authority": "observed", "payload": map[string]any{"finding": "Provider connection waits do not hold ordinary memory writes."}}}, 2*time.Second)
			if err != nil || status != 201 {
				t.Fatal("TLS wait held independent capture")
			}
			if embeddings.Load() != 0 {
				t.Fatal("fixture did not pause before provider request headers")
			}
			unblock()
			select {
			case result := <-pending:
				if result.err != nil || result.status != 503 || result.body["error"].(map[string]any)["code"] != "AI_CONFIGURATION_CHANGED" || embeddings.Load() != 1 || !oldCredential.Load() || completions.Load() != 0 {
					t.Fatal("admitted request or later-batch fencing violated the lifecycle contract")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("admitted provider call did not finish")
			}
		})
	}
}
