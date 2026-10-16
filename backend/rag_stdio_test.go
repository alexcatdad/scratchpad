package backend_test

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRAGRealStdioDiscoversProjectAndPreservesCitationsPrivacyAndBudget(t *testing.T) {
	if os.Getenv("TEST_PGVECTOR_URL") == "" {
		t.Skip("TEST_PGVECTOR_URL required")
	}
	t.Setenv("TEST_POSTGRES_URL", os.Getenv("TEST_PGVECTOR_URL"))
	t.Setenv("SCRATCHPAD_RAG_ENABLED", "true")
	path, databaseURL, local := githubHistoricalBrowser(t, "postgres")
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE EXTENSION vector`); err != nil {
		t.Fatal(err)
	}
	origin := startServer(t, path, databaseURL)
	private, key := githubSyntheticKey(t)
	enrollLocalSSH(t, origin, private, key, local)
	workspace := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main", workspace}, {"-C", workspace, "remote", "add", "origin", "https://github.com/synthetic/rag-stdio.git"}, {"-C", workspace, "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "commit", "-q", "--allow-empty", "-m", "Synthetic"}} {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if cmd.Run() != nil {
			t.Fatal("Git fixture failed")
		}
	}
	status, response := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": "https://github.com/synthetic/rag-stdio.git"}}}, restoredToken)
	if status != 200 {
		t.Fatal("project fixture failed")
	}
	project := response["project"].(map[string]any)
	settings := project["settings"].(map[string]any)
	settings["aiProcessing"], settings["crossProjectAnalysis"] = true, true
	status, _ = projectRequest(t, origin, "PATCH", "/"+project["id"].(string)+"/settings", map[string]any{"expectedVersion": 1, "settings": settings}, restoredToken)
	if status != 200 {
		t.Fatal("consent failed")
	}
	other := jobProject(t, origin, "Other RAG stdio", true, true)
	denied := jobProject(t, origin, "Denied RAG stdio", false, false)
	for _, id := range []string{project["id"].(string), other, denied} {
		status, _ = captureRequest(t, origin, id, "", map[string]any{"type": "finding", "title": "Source evidence", "authority": "observed", "payload": map[string]any{"finding": "STDIO_RAG_EVIDENCE remains immutable."}})
		if status != 201 {
			t.Fatal("source capture failed")
		}
	}
	integration := filepath.Join(t.TempDir(), "rag-integration")
	build := exec.Command("go", "build", "-race", "-o", integration, "./rag-integration")
	build.Dir = "../mcp"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("integration build failed: %v %s", err, out)
	}
	serviceSocket, _ := startConfiguredDurableService(t, origin, t.TempDir(), workspace, private)
	cmd := exec.Command(integration, "-url", origin, "-public-key", private+".pub", "-signing-key", private, "-workspace", workspace, "-binary", mcpBinary, "-other-project", other, "-denied-project", denied)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "SCRATCHPAD_SERVICE_SOCKET="+serviceSocket)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Verified real stdio RAG") {
		t.Fatalf("stdio RAG failed: %v %s", err, out)
	}
}
