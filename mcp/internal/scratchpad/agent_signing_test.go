package scratchpad

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSSHChallengeUsesConfiguredAgentWithoutPrivateKeyFile(t *testing.T) {
	// This agent and key are disposable fixtures, independent of the owner's agent.
	dir, err := os.MkdirTemp("/tmp", "scratchpad-agent-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	key := filepath.Join(dir, "key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("generate fixture: %v %s", err, out)
	}
	socket := filepath.Join(dir, "agent.sock")
	agent := exec.Command("ssh-agent", "-D", "-a", socket)
	if err := agent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = agent.Process.Kill(); _ = agent.Wait() }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture agent did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	add := exec.Command("ssh-add", key)
	add.Env = append(add.Environ(), "SSH_AUTH_SOCK="+socket)
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("load fixture: %v %s", err, out)
	}
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(dir, "allowed")
	if err := os.WriteFile(allowed, append([]byte("owner "), pub...), 0600); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339Nano)
	var origin string
	verified := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/mcp/challenge":
			_ = json.NewEncoder(w).Encode(map[string]any{"challengeId": "fixture", "nonce": "fixture-nonce", "namespace": "scratchpad-auth-v2", "version": 2, "recipient": origin, "purpose": "ssh_login", "expiresAt": expires})
		case "/api/v1/auth/mcp/verify":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			sig := filepath.Join(dir, "proof.sig")
			if err := os.WriteFile(sig, []byte(body["signature"]), 0600); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			proof, _ := json.Marshal([]any{"scratchpad-ssh-proof", 2, origin, "ssh_login", "fixture", "fixture-nonce", strings.Join(strings.Fields(string(pub))[:2], " "), expires})
			verify := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowed, "-I", "owner", "-n", "scratchpad-auth-v2", "-s", sig)
			verify.Stdin = strings.NewReader(string(proof))
			if out, err := verify.CombinedOutput(); err != nil {
				t.Errorf("verify possession: %v %s", err, out)
				w.WriteHeader(401)
				return
			}
			verified = true
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "fixture-session", "expiresAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	origin = server.URL
	// An unusable inherited agent proves the configured socket overrides it.
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(dir, "missing.sock"))
	client, err := NewClient(Config{URL: origin, PublicKeyPath: key + ".pub", SSHAgentSocket: socket})
	if err != nil {
		t.Fatal(err)
	}
	if token, err := client.authenticate(context.Background()); err != nil || token != "fixture-session" || !verified {
		t.Fatalf("configured agent possession: token=%q verified=%v err=%v", token, verified, err)
	}
	if os.Getenv("SSH_AUTH_SOCK") != filepath.Join(dir, "missing.sock") {
		t.Fatal("signing changed the process environment")
	}
}

func TestClientRejectsInvalidAgentSocket(t *testing.T) {
	for _, socket := range []string{"relative.sock", "/tmp/agent\n.sock", "/tmp/agent\r.sock", "/tmp/agent\x00.sock", "/" + strings.Repeat("x", 4096)} {
		if _, err := NewClient(Config{URL: "https://scratchpad.invalid", SSHAgentSocket: socket}); err == nil {
			t.Fatalf("accepted invalid socket %q", socket)
		}
	}
}
