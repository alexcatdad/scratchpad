package backend_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedClientsCallAuthenticatedGoBackend(t *testing.T) {
	engines := []string{"sqlite"}
	if os.Getenv("TEST_POSTGRES_URL") != "" {
		engines = append(engines, "postgres")
	}
	for _, engine := range engines {
		t.Run(engine, func(t *testing.T) {
			version, err := exec.Command("node", "--version").Output()
			if err != nil || !strings.HasPrefix(string(version), "v24.") {
				t.Fatal("generated SDK integration requires project Node 24 and npm ci")
			}
			path, databaseURL, local := githubHistoricalBrowser(t, engine)
			private, key := githubSyntheticKey(t)
			origin := startServer(t, path, databaseURL)
			enrollLocalSSH(t, origin, private, key, local)
			root, err := filepath.Abs("..")
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("node", filepath.Join(root, "node_modules/tsx/dist/cli.mjs"), filepath.Join(root, "packages/clients/test.ts"))
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "SCRATCHPAD_SDK_TEST_URL="+origin, "SCRATCHPAD_SDK_SIGNING_KEY="+private)
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "Generated TypeScript and Go clients passed") {
				t.Fatalf("actual generated SDK integration failed: %v %s", err, out)
			}
		})
	}
}
