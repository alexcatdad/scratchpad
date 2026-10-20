package backend_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func retainedTypeScriptAPI(t *testing.T, databaseURL string, readyStatus int) string {
	t.Helper()
	version, err := exec.Command("node", "--version").Output()
	if err != nil || !strings.HasPrefix(string(version), "v24.") {
		t.Fatal("the retained API restore fixture requires project Node 24 and npm ci")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	apiURL := historicalAPIURL(t)
	loader := filepath.Join(root, "node_modules/tsx/dist/cli.mjs")
	if _, err = os.Stat(loader); err != nil {
		t.Fatal("npm ci is required for retained TypeScript rollback verification")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	origin := "http://" + address
	cmd := exec.Command("node", "--import", "tsx", filepath.Join(root, "tests/typescript-api-fixture.ts"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL="+databaseURL, "SCRATCHPAD_PUBLIC_URL="+origin, "SCRATCHPAD_LEGACY_API_MODULE="+apiURL, "SCRATCHPAD_GITHUB_CLIENT_ID=", "SCRATCHPAD_GITHUB_CLIENT_SECRET=")
	if err = cmd.Start(); err != nil {
		t.Fatal("retained TypeScript API process did not start")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Error("retained TypeScript API did not stop cleanly")
			}
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
			<-done
			t.Error("retained TypeScript API shutdown timed out")
		}
	})
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(origin + "/ready")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == readyStatus {
				return origin
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("retained TypeScript readiness did not match the schema boundary")
	return ""
}

func TestPostgresPreUpgradeSnapshotRestoresRetainedTypeScriptAPI(t *testing.T) {
	source := legacyPostgres(t)
	db, err := sql.Open("pgx", source)
	if err != nil {
		t.Fatal(err)
	}
	seedRestoredHistory(t, db)
	db.Close()
	archive, err := postgresArchiveCommand(t, "pg_dump", source, "--format=custom", "--no-owner", "--no-acl").Output()
	if err != nil {
		t.Fatal("pre-upgrade PostgreSQL snapshot failed")
	}
	var postUpgradeID string
	t.Run("go-upgrade", func(t *testing.T) {
		origin := startServer(t, "", source)
		if status, _ := fetchRecord(t, origin, restoredToken); status != 200 {
			t.Fatal("Go upgrade lost pre-upgrade history")
		}
		projectID := jobProject(t, origin, "After upgrade only", false, false)
		postUpgradeID = curationCapture(t, origin, projectID, "AFTER_GO_UPGRADE_ONLY")["id"].(string)
	})
	t.Run("retained-api-refuses-upgraded-schema", func(t *testing.T) {
		origin := retainedTypeScriptAPI(t, source, 503)
		if status, _ := fetchRecord(t, origin, restoredToken); status != 503 {
			t.Fatal("retained API read an unsupported upgraded schema")
		}
	})
	t.Run("refusal-preserves-upgraded-database", func(t *testing.T) {
		origin := startServer(t, "", source)
		status, detail := curationRequest(t, origin, "GET", "/records/"+postUpgradeID, nil)
		if status != 200 || detail["record"].(map[string]any)["id"] != postUpgradeID {
			t.Fatal("retained-runtime refusal damaged the upgraded installation")
		}
	})
	destination := isolatedPostgres(t)
	restore := postgresArchiveCommand(t, "pg_restore", destination, "--exit-on-error", "--no-owner", "--no-acl")
	restore.Stdin = bytes.NewReader(archive)
	if err = restore.Run(); err != nil {
		t.Fatal("pre-upgrade snapshot restoration failed")
	}
	t.Run("retained-api-reads-restored-snapshot", func(t *testing.T) {
		origin := retainedTypeScriptAPI(t, destination, 200)
		status, detail := fetchRecord(t, origin, restoredToken)
		var expected map[string]any
		if err := json.Unmarshal([]byte(restoredRecord), &expected); err != nil {
			t.Fatal(err)
		}
		if status != 200 || !reflect.DeepEqual(detail["record"], expected) {
			t.Fatal("retained runtime did not recover authenticated original history")
		}
		status, result := searchRecords(t, origin, "q=AFTER_GO_UPGRADE_ONLY")
		if status != 200 || len(result["records"].([]any)) != 0 {
			t.Fatal("rollback retained writes outside its pre-upgrade snapshot")
		}
	})
}
