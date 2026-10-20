package backend_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Operator-supplied history stays in a disposable SQLite database. The Go
// public process, rather than an in-memory TypeScript implementation, is the
// acceptance boundary.
func TestPrivateHistoryAcceptance(t *testing.T) {
	encoded := os.Getenv("SCRATCHPAD_HISTORY_ARGS_JSON")
	if encoded == "" {
		t.Skip("private history inputs were not supplied")
	}
	var args []string
	if err := json.Unmarshal([]byte(encoded), &args); err != nil || len(args) == 0 {
		t.Fatal("invalid private history test arguments")
	}
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "node", append([]string{
		"--import", filepath.Join(root, "node_modules/tsx/dist/loader.mjs"),
		filepath.Join(root, "scripts/validate-history.ts"),
	}, args...)...)
	command.Dir = root
	command.Env = append(os.Environ(), "SCRATCHPAD_HISTORY_FIXTURE_URL="+origin, "SCRATCHPAD_HISTORY_FIXTURE_TOKEN="+restoredToken, "SCRATCHPAD_HISTORY_ARGS_JSON=")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Logf("validator diagnostic: %s", strings.TrimSpace(string(output)))
		t.Fatal("private history checks failed; source data was not printed")
	}
	result := strings.TrimSpace(string(output))
	if !strings.HasPrefix(result, `{"privacy":`) {
		t.Fatal("private history validation returned no sanitized result")
	}
	t.Logf("result=%s", result)
}
