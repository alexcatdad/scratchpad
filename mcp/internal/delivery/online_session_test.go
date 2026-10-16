package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
)

func TestOnlineReadsReuseSessionWithinAuthenticationAdmissionBudget(t *testing.T) {
	key := filepath.Join(t.TempDir(), "synthetic-key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("synthetic key: %v %s", err, out)
	}
	var admissions, challenges, reads atomic.Int32
	var revoked atomic.Bool
	var shortSession atomic.Bool
	var shortExpiry atomic.Int64
	var origin string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/v1/auth/mcp/") && admissions.Add(1) > 120 {
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "RATE_LIMITED", "message": "Synthetic authentication admission budget exhausted"}})
			return
		}
		switch r.URL.Path {
		case "/api/v1/auth/mcp/challenge":
			challenges.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"challengeId": "synthetic", "nonce": "synthetic", "namespace": "scratchpad-auth-v2", "version": 2, "recipient": origin, "purpose": "ssh_login", "expiresAt": time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)})
		case "/api/v1/auth/mcp/verify":
			expires := time.Now().Add(time.Hour)
			if shortSession.CompareAndSwap(true, false) {
				expires = time.Now().Add(time.Minute + 100*time.Millisecond)
				shortExpiry.Store(expires.Add(-time.Minute).UnixNano())
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "synthetic-token", "expiresAt": expires.UTC().Format(time.RFC3339Nano), "credentialId": "synthetic-credential", "captureCapabilities": []string{localcapture.CapabilityClientSavedAtV1}})
		case "/api/v1/records/rec_synthetic":
			if r.ContentLength != 0 {
				t.Errorf("GET read forwarded a body of %d bytes", r.ContentLength)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if r.Header.Get("Authorization") != "Bearer synthetic-token" {
				t.Error("missing authenticated read")
				w.WriteHeader(401)
				return
			}
			if revoked.Load() {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "AUTH_INVALID", "message": "Synthetic revoked session"}})
				return
			}
			reads.Add(1)
			_, _ = w.Write([]byte(`{"record":{"id":"rec_synthetic"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	origin = server.URL
	fingerprint, err := publicKeyFingerprint(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	binding := localcapture.Binding{ServerURL: origin, CredentialFingerprint: fingerprint, PublicKeyPath: key + ".pub", SigningKeyPath: key}
	adapter := NewAdapter()
	for i := 0; i < 61; i++ {
		out, err := adapter.OnlineRequest(context.Background(), binding, localcapture.OnlineRequest{Method: "GET", Path: "/records/rec_synthetic"})
		if err != nil || out.ErrorCode != "" {
			t.Fatalf("online read %d failed: %v code=%q; auth admissions=%d challenges=%d successfulReads=%d", i+1, err, out.ErrorCode, admissions.Load(), challenges.Load(), reads.Load())
		}
	}
	if challenges.Load() != 1 || reads.Load() != 61 {
		t.Fatalf("expected one in-memory session for 61 authenticated reads, challenges=%d reads=%d", challenges.Load(), reads.Load())
	}
	// Concurrent first reads share session initialization as well as the token.
	concurrent := NewAdapter()
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			out, err := concurrent.OnlineRequest(context.Background(), binding, localcapture.OnlineRequest{Method: "GET", Path: "/records/rec_synthetic"})
			if err != nil || out.ErrorCode != "" {
				t.Errorf("concurrent read: %v code=%q", err, out.ErrorCode)
			}
		})
	}
	wg.Wait()
	if challenges.Load() != 2 {
		t.Fatalf("concurrent reads did not share one session: challenges=%d", challenges.Load())
	}
	// Health/authentication probes must retain fresh-proof semantics.
	for range 2 {
		if _, err := adapter.Authenticate(context.Background(), binding); err != nil {
			t.Fatal(err)
		}
	}
	if challenges.Load() != 4 {
		t.Fatalf("explicit probes reused an online session: challenges=%d", challenges.Load())
	}
	// A signing-reference change cannot reuse the old binding's session.
	copiedKey := key + "-copy"
	privateBytes, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(copiedKey, privateBytes, 0600); err != nil {
		t.Fatal(err)
	}
	clear(privateBytes)
	changed := binding
	changed.SigningKeyPath = copiedKey
	if out, err := adapter.OnlineRequest(context.Background(), changed, localcapture.OnlineRequest{Method: "GET", Path: "/records/rec_synthetic"}); err != nil || out.ErrorCode != "" {
		t.Fatalf("binding change: %v code=%q", err, out.ErrorCode)
	}
	if challenges.Load() != 5 {
		t.Fatalf("changed binding reused old session: challenges=%d", challenges.Load())
	}
	// Server revocation remains authoritative even while a session is cached.
	revoked.Store(true)
	out, err := adapter.OnlineRequest(context.Background(), changed, localcapture.OnlineRequest{Method: "GET", Path: "/records/rec_synthetic"})
	if err != nil || out.ErrorCode != "AUTH_INVALID" || len(out.Body) != 0 {
		t.Fatalf("revoked session returned data or wrong error: %+v %v", out, err)
	}
	revoked.Store(false)
	if out, err := adapter.OnlineRequest(context.Background(), changed, localcapture.OnlineRequest{Method: "GET", Path: "/records/rec_synthetic"}); err != nil || out.ErrorCode != "" {
		t.Fatalf("reauthenticate invalidated session: %v code=%q", err, out.ErrorCode)
	}
	if challenges.Load() != 6 {
		t.Fatalf("invalidated session did not reauthenticate: challenges=%d", challenges.Load())
	}
	// Refresh an expiring session through the existing HTTP client's token rules.
	shortSession.Store(true)
	if out, err := adapter.OnlineRequest(context.Background(), binding, localcapture.OnlineRequest{Method: "GET", Path: "/records/rec_synthetic"}); err != nil || out.ErrorCode != "" {
		t.Fatalf("short session: %v code=%q", err, out.ErrorCode)
	}
	if wait := time.Until(time.Unix(0, shortExpiry.Load()).Add(time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	for range 2 {
		out, err := adapter.OnlineRequest(context.Background(), binding, localcapture.OnlineRequest{Method: "GET", Path: "/records/rec_synthetic"})
		if err != nil || out.ErrorCode != "" {
			t.Fatalf("expiry refresh: %v code=%q", err, out.ErrorCode)
		}
	}
	if challenges.Load() != 8 {
		t.Fatalf("expected one expired-session refresh, challenges=%d", challenges.Load())
	}
	// A changed public-key file must fail closed even with a cached session.
	beforeReads := reads.Load()
	if err := os.WriteFile(binding.PublicKeyPath, []byte("invalid synthetic public key"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err = adapter.OnlineRequest(context.Background(), changed, localcapture.OnlineRequest{Method: "GET", Path: "/records/rec_synthetic"})
	if failure, ok := err.(*localcapture.DeliveryFailure); !ok || failure.Code != localcapture.DeliveryCredentialChanged || len(out.Body) != 0 {
		t.Fatalf("changed public key reused cached credentials: %+v %v", out, err)
	}
	if reads.Load() != beforeReads || challenges.Load() != 8 {
		t.Fatal("changed public key made an upstream request")
	}
}
