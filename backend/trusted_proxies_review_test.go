package backend_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func proxyAuthRequest(t *testing.T, origin, method, path, forwarded string, body any, cookie *http.Cookie) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	request, _ := http.NewRequest(method, origin+"/api/v1"+path, bytes.NewReader(raw))
	request.Header.Set("Origin", origin)
	request.Header.Set("X-Forwarded-For", forwarded)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	json.NewDecoder(response.Body).Decode(&result)
	return response.StatusCode, result
}

func TestTrustedProxyKeepsIndependentAuthRoutesEnrollmentAndRecoveryAvailable(t *testing.T) {
	for _, mode := range []string{"setup", "recovery"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("SCRATCHPAD_TRUSTED_PROXIES", "127.0.0.1")
			t.Setenv("SCRATCHPAD_GITHUB_CLIENT_ID", "synthetic-client")
			t.Setenv("SCRATCHPAD_GITHUB_CLIENT_SECRET", "synthetic-secret")
			path, _, local := githubHistoricalBrowser(t, "sqlite")
			command := "setup"
			if mode == "recovery" {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('owner','owner','{"id":"owner","userId":"scratchpad-owner","version":1}',1,'2026-10-01T00:00:00Z')`)
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
				command = "recover"
			}
			capability := exec.Command(binary, command)
			capability.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL=", "SCRATCHPAD_DATABASE_PATH="+path)
			out, err := capability.CombinedOutput()
			if err != nil {
				t.Fatal("owner capability fixture failed")
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			token := lines[len(lines)-1]
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			listener.Close()
			public := "http://localhost:" + strings.Split(address, ":")[1]
			startServer(t, path, "", address, public)
			origin := public
			for i := 0; i < 120; i++ {
				status, _ := proxyAuthRequest(t, origin, "POST", "/auth/mcp/challenge", "192.0.2.2", map[string]any{}, nil)
				if status != 400 {
					t.Fatal("anonymous exhaustion fixture failed")
				}
			}
			for _, route := range []struct{ method, path string }{
				{"POST", "/auth/mcp/challenge"}, {"POST", "/auth/mcp/verify"},
				{"POST", "/auth/login/options"}, {"POST", "/auth/login/verify"},
				{"POST", "/auth/register/options"}, {"POST", "/auth/register/verify"},
				{"POST", "/auth/credentials/challenge"}, {"POST", "/auth/credentials/verify"},
				{"DELETE", "/auth/credentials/credential_missing"},
				{"POST", "/auth/github/options"}, {"POST", "/auth/github/sync"},
				{"POST", "/auth/github/block"}, {"DELETE", "/auth/github"},
				{"GET", "/auth/github/callback?state=invalid-synthetic-state&code=bad"},
			} {
				status, _ := proxyAuthRequest(t, origin, route.method, route.path, "192.0.2.2", map[string]any{}, local)
				if status != 429 {
					t.Fatalf("exhausted client bypassed %s admission: HTTP %d", route.path, status)
				}
				status, _ = proxyAuthRequest(t, origin, route.method, route.path, "192.0.2.3", map[string]any{}, local)
				if status == 429 {
					t.Fatalf("independent client shared %s admission: HTTP %d", route.path, status)
				}
			}
			status, options := proxyAuthRequest(t, origin, "POST", "/auth/register/options", "192.0.2.3", map[string]any{"setupToken": token}, nil)
			if status != 200 || options["challengeId"] == nil {
				t.Fatalf("independent %s capability unavailable: HTTP %d", mode, status)
			}
			private, key := githubSyntheticKey(t)
			status, challenge := proxyAuthRequest(t, origin, "POST", "/auth/credentials/challenge", "192.0.2.3", map[string]any{"publicKey": key}, local)
			if status != 200 {
				t.Fatalf("independent credential enrollment unavailable: HTTP %d", status)
			}
			proof := enrollmentProof(t, origin, private, key, challenge, "ssh_enroll")
			status, enrolled := proxyAuthRequest(t, origin, "POST", "/auth/credentials/verify", "192.0.2.3", proof, local)
			if status != 200 || enrolled["credentialId"] == nil {
				t.Fatalf("independent enrollment proof rejected: HTTP %d", status)
			}
		})
	}
}

func TestAuthTrustedProxySelectsNearestUntrustedClientAndRejectsSpoofing(t *testing.T) {
	for _, check := range []struct {
		name, proxies, suffix, blocked, independent string
		isolated                                    bool
	}{
		{"unconfigured", "", ", 192.0.2.2", "192.0.2.3", "192.0.2.4", false},
		{"untrusted-peer", "192.0.2.254", ", 192.0.2.2", "192.0.2.3", "192.0.2.4", false},
		{"trusted-peer", "127.0.0.1", ", 192.0.2.2", "forged, 192.0.2.2", "192.0.2.3", true},
		{"trusted-chain", "127.0.0.1,192.0.2.254", ", 192.0.2.2, 192.0.2.254", "forged, 192.0.2.2, 192.0.2.254", "192.0.2.3, 192.0.2.254", true},
		{"canonical-mapped-addresses", "invalid, ::ffff:127.0.0.1, ::ffff:c000:2fe", ", ::ffff:192.0.2.2, 192.0.2.254", "forged, ::ffff:c000:202, ::ffff:192.0.2.254", "::ffff:192.0.2.3, 192.0.2.254", true},
		{"malformed-nearest-hop", "127.0.0.1", ", invalid", "different, invalid", "192.0.2.3", true},
		{"malformed-inner-hop", "127.0.0.1,192.0.2.254", ", malformed, 192.0.2.254", "another-invalid, 192.0.2.254", "192.0.2.3, 192.0.2.254", true},
		{"too-many-hops", "127.0.0.1", strings.Repeat(", 192.0.2.2", 32), "192.0.2.3" + strings.Repeat(", 192.0.2.2", 32), "192.0.2.3", true},
		{"oversized-chain", "127.0.0.1", strings.Repeat("x", 8193), strings.Repeat("y", 8193), "192.0.2.3", true},
	} {
		t.Run(check.name, func(t *testing.T) {
			t.Setenv("SCRATCHPAD_TRUSTED_PROXIES", check.proxies)
			path := legacyFixture(t)
			origin := startServer(t, path, "")
			for i := 0; i < 120; i++ {
				status, _ := proxyAuthRequest(t, origin, "POST", "/auth/mcp/challenge", fmt.Sprintf("198.51.100.%d", i)+check.suffix, map[string]any{}, nil)
				if status != 400 {
					t.Fatalf("allowance exhausted before 120 attempts: HTTP %d at %d", status, i)
				}
			}
			status, _ := proxyAuthRequest(t, origin, "POST", "/auth/mcp/challenge", check.blocked, map[string]any{}, nil)
			if status != 429 {
				t.Fatalf("forged/malformed header changed exhausted client identity: HTTP %d", status)
			}
			status, _ = proxyAuthRequest(t, origin, "POST", "/auth/mcp/challenge", check.independent, map[string]any{}, nil)
			want := 429
			if check.isolated {
				want = 400
			}
			if status != want {
				t.Fatalf("independent forwarded client admission: HTTP %d, want %d", status, want)
			}
		})
	}
}
