package backend_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGithubPublishedKeyAuthorizesRealStdioUntilRemoved(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			private, key := githubSyntheticKey(t)
			fixture := &githubFixture{key: key}
			githubService(t, fixture)
			path, databaseURL, local := githubHistoricalBrowser(t, engine)
			token := githubSetup(t, path, databaseURL)
			origin := startServer(t, path, databaseURL)
			session := githubLogin(t, origin, "setup", token)

			serviceSocket, _ := startConfiguredDurableService(t, origin, t.TempDir(), filepath.Dir(private), private)

			read := func(allowed bool) {
				t.Helper()
				cmd := exec.Command(integrationBinary, "-url", origin, "-public-key", private+".pub", "-signing-key", private, "-workspace", filepath.Dir(private), "-binary", mcpBinary, "-service-socket", serviceSocket, "-phase", "detail", "-record-id", "rec_restored")
				out, err := cmd.CombinedOutput()
				if allowed {
					if err != nil || !strings.Contains(string(out), "PASS detail:") {
						t.Fatalf("published key did not authorize real stdio record read: %v %s", err, out)
					}
				} else if err == nil || !strings.Contains(string(out), "local Scratchpad service: authentication") {
					t.Fatalf("revoked published key did not fail with collector authentication error: %v %s", err, out)
				}
			}

			read(true)
			fixture.set("partial")
			if res := githubRequest(t, origin, "POST", "/sync", map[string]any{}, session); res.StatusCode != 503 {
				t.Fatalf("partial synchronization did not fail safely: %d", res.StatusCode)
			}
			read(true)
			fixture.set("removed")
			if res := githubRequest(t, origin, "POST", "/sync", map[string]any{}, session); res.StatusCode != 200 {
				t.Fatalf("complete removal failed: %d", res.StatusCode)
			}
			read(false)
			if status, _ := fetchRecord(t, origin, restoredToken); status != 200 {
				t.Fatal("published-key removal damaged independent access")
			}
			fixture.set("")
			if res := githubRequest(t, origin, "POST", "/sync", map[string]any{}, session); res.StatusCode != 200 {
				t.Fatalf("complete reappearance failed: %d", res.StatusCode)
			}
			read(true)
			if res := githubRequest(t, origin, "DELETE", "", nil, local); res.StatusCode != 200 {
				t.Fatalf("independent unlink failed: %d", res.StatusCode)
			}
			read(false)
		})
	}
}
