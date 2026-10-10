package scratchpad

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNormalizeRemote(t *testing.T) {
	for _, remote := range []string{"git@github.com:alexcatdad/scratchpad.git", "https://github.com/alexcatdad/scratchpad.git", "ssh://git@github.com/alexcatdad/scratchpad.git"} {
		got, err := NormalizeRemote(remote)
		if err != nil || got != "github.com/alexcatdad/scratchpad" {
			t.Fatalf("%s: %s %v", remote, got, err)
		}
	}
	if _, err := NormalizeRemote("/local/repo"); err == nil {
		t.Fatal("accepted local identity")
	}
}
func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s %v", args, out, err)
	}
}
func TestGitWorktreeAndAmbiguity(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init")
	run(t, dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "initial")
	run(t, dir, "remote", "add", "origin", "git@github.com:owner/repo.git")
	wt := filepath.Join(t.TempDir(), "worktree")
	run(t, dir, "worktree", "add", "-b", "feature", wt)
	g, err := Discover(context.Background(), wt)
	if err != nil || g.RepositoryIdentity != "github.com/owner/repo" || !g.Worktree.Detected || g.Branch != "feature" {
		t.Fatalf("%+v %v", g, err)
	}
	run(t, dir, "remote", "rename", "origin", "upstream")
	run(t, dir, "remote", "add", "fork", "git@github.com:other/repo.git")
	if _, err = Discover(context.Background(), dir); err == nil {
		t.Fatal("expected ambiguous remotes")
	}
}
func TestMirrorConcurrentRetry(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Mirror(ctx, root, "scratchpad/decisions.jsonl", map[string]any{"id": "rec_one", "content": "hello"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(filepath.Join(root, "scratchpad/decisions.jsonl"))
	if err != nil || strings.Count(string(data), "\n") != 1 {
		t.Fatalf("%s %v", data, err)
	}
	if err = Mirror(ctx, root, "../escape", map[string]any{"id": "bad"}); err == nil {
		t.Fatal("accepted escape")
	}
}
func TestProtocolToolsAndCapture(t *testing.T) {
	requests := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Error("missing auth")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/records":
			requests++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			record := body["record"].(map[string]any)
			if body["projectId"] != "proj_one" || r.Header.Get("Idempotency-Key") == "" {
				t.Error("missing capture identity")
			}
			if _, ok := record["payload"]; !ok {
				t.Error("missing payload")
			}
			fmt.Fprint(w, `{"record":{"id":"rec_one"},"mirror":{"eligible":false}}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer api.Close()
	c, _ := NewClient(Config{URL: api.URL, WorkingDirectory: t.TempDir()})
	c.token = "test"
	c.expires = time.Now().Add(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := NewServer(c).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	listed, err := cs.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) != 19 {
		t.Fatalf("tools %v %v", listed, err)
	}
	fields := map[string]map[string]any{"decision": {"decision": "Use SQLite"}, "adr": {"decision": "Use SQLite"}, "business_decision": {"decision": "Self hosted"}, "finding": {"finding": "Works"}, "qa": {"question": "Why?", "answer": "Because"}, "failure": {"observed": "Failed"}, "constraint": {"constraint": "English only"}, "project_state": {"state": "active"}}
	for kind, payload := range fields {
		payload["projectId"] = "proj_one"
		payload["requestId"] = kind
		payload["title"] = "Test"
		payload["authority"] = "explicit"
		payload["confidence"] = "high"
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "record_" + kind, Arguments: payload})
		if err != nil || result.IsError {
			t.Fatalf("%s: %+v %v", kind, result, err)
		}
	}
	if requests != 8 {
		t.Fatalf("requests %d", requests)
	}
}

type sshFixtureTransport func(*http.Request) (*http.Response, error)

func (f sshFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSSHChallengeAuthentication(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	pub, _ := os.ReadFile(key + ".pub")
	expiresAt := time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339Nano)
	trustedRecipient := ""
	recipient := func(r *http.Request) string {
		if trustedRecipient != "" {
			return trustedRecipient
		}
		return "http://" + r.Host
	}
	allowed := filepath.Join(dir, "allowed")
	if err := os.WriteFile(allowed, append([]byte("owner "), pub...), 0600); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/mcp/challenge":
			json.NewEncoder(w).Encode(map[string]any{"challengeId": "challenge", "nonce": "random-nonce", "namespace": "scratchpad-auth-v2", "version": 2, "purpose": "ssh_login", "recipient": recipient(r), "expiresAt": expiresAt})
		case "/api/v1/auth/mcp/verify":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			sig := filepath.Join(dir, "sig")
			if err := os.WriteFile(sig, []byte(body["signature"]), 0600); err != nil {
				t.Error(err)
			}
			proof, _ := json.Marshal([]any{"scratchpad-ssh-proof", 2, recipient(r), "ssh_login", "challenge", "random-nonce", strings.Join(strings.Fields(string(pub))[:2], " "), expiresAt})
			cmd := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowed, "-I", "owner", "-n", "scratchpad-auth-v2", "-s", sig)
			cmd.Stdin = strings.NewReader(string(proof))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("signature %s %v", out, err)
			}
			fmt.Fprintf(w, `{"accessToken":"session","expiresAt":%q}`, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
		default:
			t.Error("unexpected path")
		}
	}))
	defer api.Close()
	c, err := NewClient(Config{URL: api.URL, PublicKeyPath: key + ".pub", SigningKeyPath: key})
	if err != nil {
		t.Fatal(err)
	}
	token, err := c.authenticate(context.Background())
	if err != nil || token != "session" {
		t.Fatalf("%q %v", token, err)
	}

	for _, entry := range []struct{ configured, recipient string }{
		{"https://scratch_pad.example", "https://scratch_pad.example"},
		{"https://SCRATCH_PAD.example:0443", "https://scratch_pad.example"},
		{"https://mémory.example:443", "https://xn--mmory-bsa.example"},
		{"https://memory.example.com:0443", "https://memory.example.com"},
		{"https://memory.example.com:08443", "https://memory.example.com:8443"},
		{"https://İ.example", "https://xn--i-9bb.example"},
		{"https://MÉMORY.example:0443", "https://xn--mmory-bsa.example"},
		{"https://[::ffff:127.0.0.1]", "https://[::ffff:7f00:1]"},
		{"https://[::ffff:192.0.2.128]:0443", "https://[::ffff:c000:280]"},
		{"https://[::ffff:0.0.0.0]:08443", "https://[::ffff:0:0]:8443"},
		{"https://[::127.0.0.1]", "https://[::7f00:1]"},
		{"https://[2001:0:0:1:0:0:1:1]", "https://[2001::1:0:0:1:1]"},
		{"https://[2001:0db8:0000:0000:0000:0000:0000:0001]", "https://[2001:db8::1]"},
		{"https://[::FFFF:127.0.0.1]", "https://[::ffff:7f00:1]"},
	} {
		t.Run(entry.configured, func(t *testing.T) {
			trustedRecipient = entry.recipient
			c, err = NewClient(Config{URL: entry.configured, PublicKeyPath: key + ".pub", SigningKeyPath: key})
			if err != nil {
				t.Fatal(err)
			}
			target, _ := url.Parse(api.URL)
			c.HTTP.Transport = sshFixtureTransport(func(r *http.Request) (*http.Response, error) {
				local := r.Clone(r.Context())
				local.URL.Scheme, local.URL.Host = target.Scheme, target.Host
				return http.DefaultTransport.RoundTrip(local)
			})
			if token, err = c.authenticate(context.Background()); err != nil || token != "session" {
				t.Fatalf("recipient challenge/signature: %q %v", token, err)
			}
		})
	}

}

func TestSSHChallengeRejectsUntrustedContextBeforeSigning(t *testing.T) {
	for _, invalid := range []string{"legacy", "recipient", "purpose", "expired"} {
		t.Run(invalid, func(t *testing.T) {
			dir := t.TempDir()
			pub := filepath.Join(dir, "public.pub")
			if err := os.WriteFile(pub, []byte("ssh-ed25519 synthetic-test-key"), 0600); err != nil {
				t.Fatal(err)
			}
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/auth/mcp/challenge" {
					t.Error("invalid context reached verification")
				}
				challenge := map[string]any{"challengeId": "challenge", "nonce": "nonce", "namespace": "scratchpad-auth-v2", "version": 2, "purpose": "ssh_login", "recipient": "http://" + r.Host, "expiresAt": time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)}
				switch invalid {
				case "legacy":
					delete(challenge, "version")
					challenge["namespace"] = "scratchpad-auth"
				case "recipient":
					challenge["recipient"] = "https://other.example.test"
				case "purpose":
					challenge["purpose"] = "ssh_enroll"
				case "expired":
					challenge["expiresAt"] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
				}
				json.NewEncoder(w).Encode(challenge)
			}))
			defer api.Close()
			c, err := NewClient(Config{URL: api.URL, PublicKeyPath: pub, SigningKeyPath: filepath.Join(dir, "nonexistent-private-key")})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.authenticate(context.Background()); err == nil || err.Error() != "invalid signing challenge" {
				t.Fatalf("expected rejection before signing, got %v", err)
			}
		})
	}
}

func TestCaptureMirrorGatesAndPartialSuccess(t *testing.T) {
	for _, tc := range []struct {
		name            string
		local, eligible bool
		path            string
		wantAttempt     bool
		wantSuccess     bool
	}{{"local disabled", false, true, "mirror.jsonl", false, false}, {"project disabled", true, false, "mirror.jsonl", false, false}, {"success", true, true, "mirror.jsonl", true, true}, {"failure preserves record", true, true, "../escape", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			run(t, dir, "init")
			reports := 0
			creates := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/records":
					creates++
					fmt.Fprintf(w, `{"record":{"id":"rec_mirror","type":"decision"},"mirror":{"eligible":%t}}`, tc.eligible)
				case "/api/v1/records/rec_mirror/mirror":
					reports++
					fmt.Fprint(w, `{}`)
				default:
					t.Error(r.URL.Path)
				}
			}))
			defer api.Close()
			c, _ := NewClient(Config{URL: api.URL, WorkingDirectory: dir, Mirror: tc.local, MirrorPath: tc.path})
			c.token = "test"
			c.expires = time.Now().Add(time.Hour)
			for range 2 {
				_, out, err := c.capture(context.Background(), "decision", Common{Scope: Scope{ProjectID: "project"}, RequestID: "stable", Title: "title", Authority: "explicit", Confidence: "high"}, DecisionPayload{Decision: "test"})
				if err != nil {
					t.Fatal(err)
				}
				if out.Record["id"] != "rec_mirror" {
					t.Fatal("central record lost")
				}
				if tc.wantAttempt && (out.Mirror["attempted"] != true || out.Mirror["succeeded"] != tc.wantSuccess) {
					t.Fatalf("%+v", out)
				}
			}
			if creates != 2 {
				t.Fatal(creates)
			}
			if tc.wantAttempt && reports != 2 {
				t.Fatal(reports)
			}
			if !tc.wantAttempt && reports != 0 {
				t.Fatal(reports)
			}
			if tc.wantSuccess {
				data, err := os.ReadFile(filepath.Join(dir, tc.path))
				if err != nil || strings.Count(string(data), "\n") != 1 {
					t.Fatalf("%s %v", data, err)
				}
			}
		})
	}
}

func TestStdioProtocol(t *testing.T) {
	if os.Getenv("SCRATCHPAD_TEST_HELPER") == "1" {
		c, err := NewClient(Config{URL: "http://localhost:1"})
		if err != nil {
			os.Exit(2)
		}
		if err = NewServer(c).Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestStdioProtocol$")
	command.Env = append(os.Environ(), "SCRATCHPAD_TEST_HELPER=1")
	session, err := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 19 {
		t.Fatalf("%v %v", tools, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "record_qa", Arguments: map[string]any{"question": "incomplete"}})
	if err == nil && (result == nil || !result.IsError) {
		t.Fatal("invalid typed input unexpectedly accepted")
	}
}

func TestMirrorRejectsSymlinkEscapeBeforeCreatingDirectories(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := Mirror(context.Background(), root, "link/new/file.jsonl", map[string]any{"id": "rec"}); err == nil {
		t.Fatal("accepted symlink escape")
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(err) {
		t.Fatal("created directory outside root")
	}
}
