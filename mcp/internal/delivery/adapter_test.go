package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
)

func TestOldVerifyResponseBlocksBeforeCapturePost(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "synthetic-key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", keyPath).CombinedOutput(); err != nil {
		t.Fatalf("create synthetic SSH key: %v (%s)", err, out)
	}
	var origin string
	recordPosts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/mcp/challenge":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"challengeId": "synthetic", "nonce": "synthetic-nonce",
				"namespace": "scratchpad-auth-v2", "version": 2,
				"recipient": origin, "purpose": "ssh_login",
				"expiresAt": time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano),
			})
		case "/api/v1/auth/mcp/verify":
			// Model an older server: it issues a usable session but omits both
			// credential identity and the durable capture capability.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accessToken": "synthetic-token",
				"expiresAt":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
			})
		case "/api/v1/records":
			recordPosts++
			http.Error(w, "unexpected capture", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	origin = server.URL
	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := publicKeyFingerprint(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewAdapter()
	_, err = adapter.Deliver(context.Background(), localcapture.DeliveryItem{
		RequestID:     "stable-synthetic-request",
		ClientSavedAt: "2026-10-15T08:09:10Z",
		Record:        localcapture.Record{Type: "finding", Title: "Synthetic", Authority: "observed", Confidence: "high", Payload: json.RawMessage(`{"finding":"synthetic"}`)},
		Scope:         localcapture.Scope{ProjectID: "proj_synthetic"},
		Binding:       localcapture.Binding{ServerURL: origin, CredentialFingerprint: fingerprint, PublicKeyPath: keyPath + ".pub", SigningKeyPath: keyPath},
	})
	if failure, ok := err.(*localcapture.DeliveryFailure); !ok || failure.Code != localcapture.DeliveryServerIncompatible {
		t.Fatalf("old server should be blocked as incompatible, got %T %v (pub bytes %d)", err, err, len(pub))
	}
	if recordPosts != 0 {
		t.Fatal("capture was posted before server capability was confirmed")
	}
}

func TestVerifyConnectionRequiresAuthenticatedProjectsRead(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "synthetic-key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", keyPath).CombinedOutput(); err != nil {
		t.Fatalf("create synthetic SSH key: %v (%s)", err, out)
	}
	var origin string
	verifyCalls := 0
	projectReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/mcp/challenge":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"challengeId": "synthetic", "nonce": "synthetic-nonce",
				"namespace": "scratchpad-auth-v2", "version": 2,
				"recipient": origin, "purpose": "ssh_login",
				"expiresAt": time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano),
			})
		case "/api/v1/auth/mcp/verify":
			verifyCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accessToken":         "synthetic-token",
				"expiresAt":           time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
				"credentialId":        "credential-synthetic",
				"captureCapabilities": []string{localcapture.CapabilityClientSavedAtV1},
			})
		case "/api/v1/projects":
			projectReads++
			if r.Header.Get("Authorization") != "Bearer synthetic-token" {
				t.Errorf("projects read did not use the authenticated session: authorization=%q", r.Header.Get("Authorization"))
			}
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "FORBIDDEN", "message": "synthetic read denied"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	origin = server.URL
	fingerprint, err := publicKeyFingerprint(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := NewAdapter().VerifyConnection(context.Background(), localcapture.Binding{
		ServerURL: origin, CredentialFingerprint: fingerprint,
		PublicKeyPath: keyPath + ".pub", SigningKeyPath: keyPath,
	})
	if failure, ok := err.(*localcapture.DeliveryFailure); !ok || failure.Code != localcapture.DeliveryServerRejected {
		t.Fatalf("resource-read rejection should fail verification with a safe category, identity=%+v err=%T %v verifyCalls=%d projectReads=%d", identity, err, err, verifyCalls, projectReads)
	}
	if identity.CredentialID != "" || verifyCalls != 1 || projectReads != 1 {
		t.Fatalf("verification did not require one authenticated resource read: identity=%+v verifyCalls=%d projectReads=%d", identity, verifyCalls, projectReads)
	}
}
