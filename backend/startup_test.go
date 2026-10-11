package backend_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

	_ "modernc.org/sqlite"
)

var binary, mcpBinary, integrationBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "scratchpad-go-tests-")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "scratchpad-server")
	build := exec.Command("go", "build", "-race", "-o", binary, "./cmd/scratchpad-server")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		os.RemoveAll(dir)
		os.Exit(1)
	}
	mcpBinary, integrationBinary = filepath.Join(dir, "scratchpad-mcp"), filepath.Join(dir, "integration")
	for _, target := range []struct{ output, pkg string }{{mcpBinary, "./cmd/scratchpad-mcp"}, {integrationBinary, "./integration"}} {
		build := exec.Command("go", "build", "-race", "-o", target.output, target.pkg)
		build.Dir = "../mcp"
		build.Stdout, build.Stderr = os.Stdout, os.Stderr
		if err := build.Run(); err != nil {
			os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestFreshSQLiteMigrationIsRepeatable(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "memory.sqlite")
	for range 2 {
		cmd := exec.Command(binary, "migrate")
		cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL=", "SCRATCHPAD_DATABASE_PATH="+path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("migration failed: %v %s", err, out)
		}
		var report struct {
			Database      string `json:"database"`
			SchemaVersion int    `json:"schemaVersion"`
		}
		if err := json.Unmarshal(out, &report); err != nil {
			t.Fatal(err)
		}
		if report.Database != "sqlite" || report.SchemaVersion != 2 {
			t.Fatalf("unexpected report: %s", out)
		}
	}
}

func TestSchemaOneUpgrade(t *testing.T) {
	path := legacyFixture(t)
	cmd := migrationCommand(path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("upgrade failed: %v %s", err, out)
	}
	var report struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 2 {
		t.Fatalf("supported schema 1 was not upgraded: %s", out)
	}
}

func migrationCommand(path string) *exec.Cmd {
	cmd := exec.Command(binary, "migrate")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL=", "SCRATCHPAD_DATABASE_PATH="+path)
	return cmd
}

// A supported installation written in the existing TypeScript schema. Opening
// SQL here creates historical input; assertions use the operational CLI/HTTP.
func legacyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "memory.sqlite")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
 CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL);
 INSERT INTO schema_migrations VALUES(1,'2026-10-01T00:00:00.000Z');
 CREATE TABLE entities(kind TEXT NOT NULL,id TEXT NOT NULL,data TEXT NOT NULL,version INTEGER NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(kind,id));
 CREATE TABLE identities(identity TEXT PRIMARY KEY,project_id TEXT NOT NULL);
 CREATE TABLE retries(scope TEXT NOT NULL,key TEXT NOT NULL,request TEXT NOT NULL,response TEXT NOT NULL,PRIMARY KEY(scope,key));
 CREATE VIRTUAL TABLE record_search USING fts5(record_id UNINDEXED,title,content);`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUnsupportedSchemaIsRefusedWithoutChangingBackup(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations VALUES(999,'2026-10-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := migrationCommand(path).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
		t.Fatalf("newer schema accepted: %v %s", err, out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("refused database was modified")
	}
}

func TestMalformedSchemaIsNotRepaired(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE entities; CREATE TABLE entities(kind TEXT,id TEXT,data TEXT,version INTEGER,created_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, _ := os.ReadFile(path)
	out, err := migrationCommand(path).CombinedOutput()
	if err == nil {
		t.Fatalf("malformed installation accepted: %s", out)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("malformed installation was repaired")
	}
}

func startServer(t *testing.T, path, databaseURL string, listenAddress ...string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	if len(listenAddress) > 0 {
		address = listenAddress[0]
	}
	publicOrigin := "http://" + address
	if len(listenAddress) > 1 {
		publicOrigin = listenAddress[1]
	}
	cmd := exec.Command(binary, "serve")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_PATH="+path, "SCRATCHPAD_DATABASE_URL="+databaseURL, "SCRATCHPAD_LISTEN_ADDR="+address, "SCRATCHPAD_PUBLIC_URL="+publicOrigin)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server exited unsuccessfully: %v", err)
			}
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
			t.Error("server did not shut down")
		}
	})
	line := make(chan []byte, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			line <- scanner.Bytes()
		} else {
			line <- nil
		}
	}()
	select {
	case data := <-line:
		var report struct {
			Address string `json:"address"`
		}
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatalf("startup report unavailable: %s", data)
		}
		return "http://" + report.Address
	case <-time.After(10 * time.Second):
		t.Fatal("server startup timed out")
	}
	return ""
}

func TestGoServerReadiness(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	origin := startServer(t, filepath.Join(dir, "memory.sqlite"), "")
	response, err := http.Get(origin + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var report struct {
		Status   string `json:"status"`
		Database string `json:"database"`
	}
	if err := json.NewDecoder(response.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || report.Status != "ready" || report.Database != "sqlite" {
		t.Fatalf("unexpected readiness: %d %+v", response.StatusCode, report)
	}
}

const restoredRecord = `{"id":"rec_restored","createdAt":"2026-10-01T00:00:00.000Z","version":1,"projectId":"proj_restored","type":"finding","title":"Preserve original evidence","authority":"observed","confidence":"high","payload":{"finding":"A synthetic restored capture.","custom":[1,true,"evidence"]},"sourceContext":{"branch":"main","commit":"abc123"}}`
const restoredToken = "synthetic_existing_agent_token"

func seedRestoredHistory(t *testing.T, db *sql.DB) {
	t.Helper()
	digest := sha256.Sum256([]byte(restoredToken))
	inputs := []struct{ kind, id, data string }{
		{"record", "rec_restored", restoredRecord},
		{"credential", "credential_restored", `{"id":"credential_restored","kind":"ssh","source":"local","createdAt":"2026-10-01T00:00:00Z","version":1}`},
		{"session", hex.EncodeToString(digest[:]), `{"credentialId":"credential_restored","expiresAt":"2099-01-01T00:00:00Z","browser":false}`},
		{"metadata", "rec_restored", `{"id":"rec_restored","tags":["migration"],"archived":false,"version":1}`},
		{"revision", "revision_restored", `{"id":"revision_restored","recordId":"rec_restored","changes":{"tags":["migration"]}}`},
		{"evidence", "evidence_restored", `{"id":"evidence_restored","recordId":"rec_restored","url":"https://example.com/evidence"}`},
		{"audit", "audit_restored", `{"id":"audit_restored","entityId":"rec_restored","action":"record.created"}`},
		// Schema migration must work independently of legacy provider-secret conversion.
		{"settings", "global", `{"apiKey":"synthetic_legacy_secret"}`},
	}
	for _, input := range inputs {
		if _, err := db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES($1,$2,$3,1,'2026-10-01T00:00:00Z')`, input.kind, input.id, input.data); err != nil {
			t.Fatal(err)
		}
	}
}
func fetchRecord(t *testing.T, origin, token string) (int, map[string]any) {
	t.Helper()
	request, err := http.NewRequest("GET", origin+"/api/v1/records/rec_restored", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var value map[string]any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatalf("invalid JSON response (status %d)", response.StatusCode)
	}
	return response.StatusCode, value
}
func TestRestoredSQLiteRecordSurvivesUpgradeAndRestart(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	seedRestoredHistory(t, db)
	db.Close()
	for range 2 {
		t.Run("startup", func(t *testing.T) {
			origin := startServer(t, path, "")
			status, value := fetchRecord(t, origin, restoredToken)
			if status != 200 {
				t.Fatalf("restored session could not read history: %d %+v", status, value)
			}
			var expected map[string]any
			json.Unmarshal([]byte(restoredRecord), &expected)
			if !reflect.DeepEqual(value["record"], expected) {
				t.Fatal("immutable record changed")
			}
			if len(value["revisions"].([]any)) != 1 || len(value["evidence"].([]any)) != 1 || len(value["audit"].([]any)) != 1 {
				t.Fatal("related history was lost")
			}
		})
	}
}

func TestRecordRequiresAuthentication(t *testing.T) {
	path := legacyFixture(t)
	origin := startServer(t, path, "")
	status, value := fetchRecord(t, origin, "")
	if status != 401 || value["error"].(map[string]any)["code"] != "AUTH_REQUIRED" {
		t.Fatalf("wrong authentication failure: %d %+v", status, value)
	}
}

func TestCompetingSQLiteStartups(t *testing.T) {
	path := legacyFixture(t)
	commands := []*exec.Cmd{migrationCommand(path), migrationCommand(path), migrationCommand(path)}
	outputs := make([]bytes.Buffer, len(commands))
	for i, cmd := range commands {
		cmd.Stdout = &outputs[i]
		cmd.Stderr = &outputs[i]
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Errorf("competing startup failed: %v %s", err, outputs[i].Bytes())
		}
	}
	out, err := migrationCommand(path).CombinedOutput()
	if err != nil {
		t.Fatalf("repeat after competing startups failed: %v %s", err, out)
	}
}

func TestFailedSQLiteDDLCanBeRetried(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE entities_created(id TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if out, err := migrationCommand(path).CombinedOutput(); err == nil {
		t.Fatalf("conflicting migration unexpectedly succeeded: %s", out)
	}
	// Operator removes the known synthetic conflict; the migration must have
	// rolled back both its DDL and marker, so an ordinary retry can succeed.
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE entities_created`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if out, err := migrationCommand(path).CombinedOutput(); err != nil {
		t.Fatalf("retry after failed migration failed: %v %s", err, out)
	}
}

func TestSQLiteRejectsUnsafeFiles(t *testing.T) {
	for _, variant := range []string{"permissions", "symlink", "hardlink", "sidecar"} {
		t.Run(variant, func(t *testing.T) {
			path := legacyFixture(t)
			switch variant {
			case "permissions":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				link := filepath.Join(filepath.Dir(path), "linked.sqlite")
				if err := os.Symlink(path, link); err != nil {
					t.Fatal(err)
				}
				path = link
			case "hardlink":
				if err := os.Link(path, filepath.Join(filepath.Dir(path), "linked.sqlite")); err != nil {
					t.Fatal(err)
				}
			case "sidecar":
				if err := os.WriteFile(path+"-wal", []byte("synthetic unsafe sidecar"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			out, err := migrationCommand(path).CombinedOutput()
			if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
				t.Fatalf("unsafe storage accepted: %v %s", err, out)
			}
		})
	}
}

func TestRestoredAuthenticationIsRechecked(t *testing.T) {
	inputs := []struct{ name, statement string }{
		{"revoked credential", `UPDATE entities SET data=json_set(data,'$.revokedAt','2026-10-01T00:00:00Z') WHERE kind='credential'`},
		{"revoked session", `UPDATE entities SET data=json_set(data,'$.revokedAt','2026-10-01T00:00:00Z') WHERE kind='session'`},
		{"expired session", `UPDATE entities SET data=json_set(data,'$.expiresAt','2000-01-01T00:00:00Z') WHERE kind='session'`},
		{"browser session on bearer", `UPDATE entities SET data=json_set(data,'$.browser',json('true')) WHERE kind='session'`},
		{"unlinked GitHub credential", `UPDATE entities SET data=json_set(data,'$.source','github') WHERE kind='credential'`},
	}
	for _, input := range inputs {
		t.Run(input.name, func(t *testing.T) {
			path := legacyFixture(t)
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			seedRestoredHistory(t, db)
			if _, err := db.Exec(input.statement); err != nil {
				t.Fatal(err)
			}
			db.Close()
			origin := startServer(t, path, "")
			status, value := fetchRecord(t, origin, restoredToken)
			if status != 401 || value["error"].(map[string]any)["code"] != "AUTH_INVALID" {
				t.Fatalf("invalid restored credential accepted: %d %+v", status, value)
			}
		})
	}
}

func TestSQLiteBackupRestorePreservesHistory(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	seedRestoredHistory(t, db)
	db.Close()
	// This fixture is closed, so a single-file copy is an independent schema-1
	// backup. Live installations must use the online backup command instead.
	snapshot, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := migrationCommand(path).CombinedOutput(); err != nil {
		t.Fatalf("upgrade failed: %v %s", err, out)
	}
	restored := filepath.Join(filepath.Dir(path), "restored.sqlite")
	if err := os.WriteFile(restored, snapshot, 0600); err != nil {
		t.Fatal(err)
	}
	origin := startServer(t, restored, "")
	status, value := fetchRecord(t, origin, restoredToken)
	var expected map[string]any
	json.Unmarshal([]byte(restoredRecord), &expected)
	if status != 200 || !reflect.DeepEqual(value["record"], expected) {
		t.Fatal("restored backup did not preserve authenticated history")
	}
}

func TestInterruptedSQLiteStartupCanBeRetried(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	cmd := migrationCommand(path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	cmd.Process.Kill()
	if err := cmd.Wait(); err == nil {
		t.Fatal("startup did not wait for the migration lock")
	}
	if _, err := db.Exec(`ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if out, err := migrationCommand(path).CombinedOutput(); err != nil {
		t.Fatalf("restart after interruption failed: %v %s", err, out)
	}
}

func TestStartupErrorsDoNotDiscloseDatabaseCredentials(t *testing.T) {
	cmd := exec.Command(binary, "migrate")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL=postgres://synthetic_owner:synthetic_secret_must_not_leak@127.0.0.1:1/synthetic_database")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") || strings.Contains(string(out), "synthetic_secret_must_not_leak") || strings.Contains(string(out), "postgres://") {
		t.Fatalf("unsafe startup failure: %v %s", err, out)
	}
}
