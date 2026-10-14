package backend_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestAISettingsRejectBooleanVersionPreconditions(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		for _, version := range []bool{false, true} {
			t.Run(engine+"/"+map[bool]string{false: "false", true: "true"}[version], func(t *testing.T) {
				path, databaseURL := portableInstance(t, engine)
				origin := startServer(t, path, databaseURL)
				current := float64(0)
				if version {
					status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "model": "retained-model"})
					if status != 200 {
						t.Fatal("configuration fixture failed")
					}
					current = 1
				}
				status, before := curationRequest(t, origin, "GET", "/ai/settings", nil)
				if status != 200 {
					t.Fatal("configuration unavailable")
				}
				status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": version, "model": "rejected-boolean-model"})
				if status != 400 {
					t.Fatalf("boolean version precondition was coerced: HTTP %d", status)
				}
				status, after := curationRequest(t, origin, "GET", "/ai/settings", nil)
				if status != 200 || after["version"] != current || after["model"] != before["model"] {
					t.Fatal("invalid precondition changed provider configuration")
				}
				status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": current, "model": "numeric-precondition-model"})
				if status != 200 {
					t.Fatal("numeric operational version precondition stopped working")
				}
			})
		}
	}
}

func TestPublicOriginRefusesInvalidExplicitPorts(t *testing.T) {
	for _, origin := range []string{"http://localhost:0", "http://127.0.0.1:65536", "http://[::1]:", "https://scratchpad.example:999999", "https://scratchpad.example:"} {
		t.Run(origin, func(t *testing.T) {
			path := legacyFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "serve")
			command.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL=", "SCRATCHPAD_DATABASE_PATH="+path, "SCRATCHPAD_LISTEN_ADDR=127.0.0.1:0", "SCRATCHPAD_PUBLIC_URL="+origin)
			output, err := command.CombinedOutput()
			if err == nil || ctx.Err() != nil || !strings.Contains(string(output), "CONFIG_INVALID") {
				t.Fatalf("invalid explicit public port was not refused: %s", origin)
			}
		})
	}
}

func TestPublicOriginKeepsValidPortsAndCanonicalDefaults(t *testing.T) {
	for _, config := range []struct{ input, canonical string }{
		{"http://localhost:00080/", "http://localhost"},
		{"https://scratchpad.example:0443/", "https://scratchpad.example"},
		{"http://localhost:65535", "http://localhost:65535"},
		{"https://scratchpad.example:1", "https://scratchpad.example:1"},
	} {
		t.Run(config.input, func(t *testing.T) {
			path := legacyFixture(t)
			_, key := enrolledSSHKey(t, path)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			listener.Close()
			origin := startServer(t, path, "", address, config.input)
			status, response := postJSON(t, origin, "/api/v1/auth/mcp/challenge", map[string]string{"publicKey": key})
			if status != http.StatusOK || response["recipient"] != config.canonical {
				t.Fatal("valid public port was rejected or not canonicalized")
			}
		})
	}
}

func TestAISettingsRetainNumericStringAndHeaderVersions(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": "0", "model": "string-version-model"})
	if status != 200 {
		t.Fatal("compatible numeric string precondition rejected")
	}
	request, _ := http.NewRequest("PATCH", origin+"/api/v1/ai/settings", bytes.NewBufferString(`{"model":"header-version-model"}`))
	request.Header.Set("Authorization", "Bearer "+restoredToken)
	request.Header.Set("If-Match", `"1"`)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("quoted If-Match version rejected")
	}
}
