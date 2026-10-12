package backend_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPublicHostnameMatchesBrowserOriginAndTrustedSSHRecipient(t *testing.T) {
	for _, config := range []struct{ input, canonical, hostname string }{
		{"https://[::ffff:127.0.0.1]", "https://[::ffff:7f00:1]", "::ffff:7f00:1"},
		{"https://[::FFFF:192.0.2.128]:08443/", "https://[::ffff:c000:280]:8443", "::ffff:c000:280"},
		{"https://[2001:0db8:0000:0000:0000:0000:0000:0001]:0443", "https://[2001:db8::1]", "2001:db8::1"},
		{"https://İ.example", "https://xn--i-9bb.example", "xn--i-9bb.example"},
		{"https://MÉMORY.example:0443/", "https://xn--mmory-bsa.example", "xn--mmory-bsa.example"},
	} {
		t.Run(config.input, func(t *testing.T) {
			path := legacyFixture(t)
			private, key := enrolledSSHKey(t, path)
			setup := githubSetup(t, path, "")
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			listener.Close()
			origin := startServer(t, path, "", address, config.input)
			status, challenge := postJSON(t, origin, "/api/v1/auth/mcp/challenge", map[string]string{"publicKey": key})
			if status != 200 || challenge["recipient"] != config.canonical {
				t.Fatalf("server disagrees with browser/signer origin: HTTP %d %+v, want %s", status, challenge, config.canonical)
			}
			proof := enrollmentProof(t, config.canonical, private, key, challenge, "ssh_login")
			status, session := postJSON(t, origin, "/api/v1/auth/mcp/verify", proof)
			if status != 200 {
				t.Fatalf("canonical trusted recipient proof rejected: HTTP %d %+v", status, session)
			}
			if status, _ = fetchRecord(t, origin, session["accessToken"].(string)); status != 200 {
				t.Fatal("canonical SSH login did not authorize restored source retrieval")
			}
			body, _ := json.Marshal(map[string]any{"setupToken": setup})
			request, _ := http.NewRequest("POST", origin+"/api/v1/auth/register/options", bytes.NewReader(body))
			request.Header.Set("Origin", "https://untrusted.example")
			denied, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			denied.Body.Close()
			if denied.StatusCode != 403 {
				t.Fatal("hostname canonicalization weakened the browser origin gate")
			}
			request, _ = http.NewRequest("POST", origin+"/api/v1/auth/register/options", bytes.NewReader(body))
			request.Header.Set("Origin", config.canonical)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var result map[string]any
			if json.NewDecoder(response.Body).Decode(&result) != nil {
				t.Fatal("browser options response was not JSON")
			}
			if net.ParseIP(config.hostname) != nil && response.StatusCode == 503 && result["error"].(map[string]any)["code"] == "CONFIG_INVALID" {
				return
			} // IP-host passkeys remain unsupported.
			if response.StatusCode != 200 || result["options"].(map[string]any)["rp"].(map[string]any)["id"] != config.hostname {
				t.Fatalf("canonical browser origin/RP mismatch: HTTP %d %+v", response.StatusCode, result)
			}
		})
	}
}

func TestPublicHostnameCanonicalizationDoesNotAllowMappedPlaintextHTTP(t *testing.T) {
	for _, public := range []string{"http://[::ffff:127.0.0.1]", "http://[::ffff:7f00:1]"} {
		t.Run(public, func(t *testing.T) {
			path := legacyFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "serve")
			command.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL=", "SCRATCHPAD_DATABASE_PATH="+path, "SCRATCHPAD_LISTEN_ADDR=127.0.0.1:0", "SCRATCHPAD_PUBLIC_URL="+public)
			output, err := command.CombinedOutput()
			if err == nil || ctx.Err() != nil || !strings.Contains(string(output), "CONFIG_INVALID") {
				t.Fatal("mapped IPv6 hostname gained plaintext HTTP permission")
			}
		})
	}
}
