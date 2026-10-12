package backend_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPassageCompletionConfigChangeStopsAdditionalProviderBatches(t *testing.T) {
	path, databaseURL := ragInstance(t)
	first, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var calls atomic.Int32
	var blocked atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if blocked.CompareAndSwap(false, true) {
			close(first)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer func() { unblock(); provider.Close() }()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("provider configuration failed")
	}
	project := jobProject(t, origin, "Dispatch revocation across passages", true, false)
	status, captured := captureRequest(t, origin, project, "", map[string]any{"type": "finding", "title": "Multi-passage review evidence", "authority": "observed", "payload": map[string]any{"finding": strings.Repeat("Long original synthetic evidence. ", 420)}})
	if status != 201 {
		t.Fatal("multi-passage capture failed")
	}
	id := captured["record"].(map[string]any)["id"].(string)
	status, queued := curationRequest(t, origin, "POST", "/rag/index", map[string]any{"projectId": project, "recordId": id})
	if status != 202 {
		t.Fatal("queue lookup failed")
	}
	job := queued["job"].(map[string]any)["id"].(string)
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("first passage did not reach provider")
	}
	status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "model": "new-completion-model"})
	if status != 200 {
		t.Fatal("provider response wait held settings write")
	}
	unblock()
	terminal := waitProviderReviewJob(t, origin, job)
	if calls.Load() != 1 || terminal["status"] != "failed" {
		t.Fatalf("completion-only update allowed new old-generation passage batches: calls=%d status=%v", calls.Load(), terminal["status"])
	}
	status, coverage := curationRequest(t, origin, "GET", "/rag/coverage?projectId="+project, nil)
	if status != 200 || coverage["indexedSources"] != float64(0) {
		t.Fatal("interrupted batch activated a partial passage projection")
	}
}

func waitProviderReviewJob(t *testing.T, origin, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for {
		status, response := curationRequest(t, origin, "GET", "/ai/jobs", nil)
		if status != 200 {
			t.Fatal("job status unavailable")
		}
		for _, value := range response["jobs"].([]any) {
			job := value.(map[string]any)
			if job["id"] == id && (job["status"] == "completed" || job["status"] == "failed") {
				return job
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("passage job did not reach a terminal state")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// A server that accepts TCP but never completes TLS must not retain the
// configuration writer lock. The already initiated attempt may fail afterwards.
func TestProviderTransportInitiationReleasesWritesBeforeTLSHandshake(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			go func() {
				defer close(finished)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				if _, err = conn.Read(make([]byte, 1)); err != nil {
					return
				}
				close(started)
				<-release
			}()
			defer func() {
				unblock()
				listener.Close()
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					t.Error("owned TLS fixture did not stop")
				}
			}()
			var newCalls atomic.Int32
			replacement := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				newCalls.Add(1)
				http.Error(w, "unexpected later request", 503)
			}))
			defer replacement.Close()
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": "https://" + listener.Addr().String() + "/v1", "embeddingDimensions": 2, "requestTimeoutSeconds": 5})
			if status != 200 {
				t.Fatal("TLS provider fixture failed")
			}
			type providerReply struct {
				status int
				err    error
			}
			done := make(chan providerReply, 1)
			go func() {
				req, _ := http.NewRequest("POST", origin+"/api/v1/ai/test", strings.NewReader(`{}`))
				req.Header.Set("Authorization", "Bearer "+restoredToken)
				res, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
				status := 0
				if res != nil {
					status = res.StatusCode
					res.Body.Close()
				}
				done <- providerReply{status, err}
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("provider transport did not initiate TLS")
			}
			req, _ := http.NewRequest("PATCH", origin+"/api/v1/ai/settings", strings.NewReader(fmt.Sprintf(`{"expectedVersion":1,"baseUrl":%q}`, replacement.URL+"/v1")))
			req.Header.Set("Authorization", "Bearer "+restoredToken)
			res, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
			if err != nil {
				unblock()
				t.Fatal("TLS provider wait retained configuration lock")
			}
			res.Body.Close()
			if res.StatusCode != 200 {
				t.Fatal("settings update failed during TLS wait")
			}
			// Let request-context cancellation finish the blocked transport, proving that
			// the admission waiter and request goroutine both terminate without TLS data.
			select {
			case reply := <-done:
				if reply.err != nil {
					t.Fatal(reply.err)
				}
				if reply.status != 503 {
					t.Fatalf("cancelled transport returned HTTP %d", reply.status)
				}
			case <-time.After(7 * time.Second):
				unblock()
				t.Fatal("cancelled TLS transport did not finish")
			}
			if newCalls.Load() != 0 {
				t.Fatal("provider test continued after failed prior transport")
			}
		})
	}
}

func TestProviderPreTransportFailureReleasesConfigurationWrites(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", syntheticEncryptionKey(t, 97))
			path, databaseURL := portableInstance(t, engine)
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "unexpected dispatch", 503) }))
			defer provider.Close()
			origin := startServer(t, path, databaseURL)
			// A retained user-supplied token containing a newline fails HTTP header
			// validation before GetConn; admission must observe transport completion.
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "apiKey": "synthetic-invalid-token\n", "embeddingDimensions": 2, "requestTimeoutSeconds": 5})
			if status != 200 {
				t.Fatal("pre-transport fixture failed")
			}
			req, _ := http.NewRequest("POST", origin+"/api/v1/ai/test", strings.NewReader("{}"))
			req.Header.Set("Authorization", "Bearer "+restoredToken)
			response, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
			if err != nil {
				t.Fatal("pre-transport failure waited for the provider deadline")
			}
			var result map[string]any
			err = json.NewDecoder(response.Body).Decode(&result)
			response.Body.Close()
			status = response.StatusCode
			if err != nil {
				t.Fatal("pre-transport failure returned invalid HTTP response")
			}
			if status != 503 || result["error"].(map[string]any)["code"] != "AI_UNAVAILABLE" || calls.Load() != 0 {
				t.Fatal("invalid header reached provider or was not reported as unavailable")
			}
			status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "apiKey": ""})
			if status != 200 {
				t.Fatal("pre-transport failure retained the configuration lock")
			}
		})
	}
}
