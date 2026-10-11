package backend_test

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func isolatedPostgres(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("TEST_POSTGRES_URL is not configured")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal("invalid test PostgreSQL URL")
	}
	admin, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	name := "scratchpad_go_test_" + hex.EncodeToString(suffix[:])
	if _, err := admin.Exec(`CREATE DATABASE "` + name + `"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP DATABASE "` + name + `" WITH (FORCE)`); err != nil {
			t.Error(err)
		}
	})
	u.Path = "/" + name
	return u.String()
}

func postgresCommand(databaseURL string) *exec.Cmd {
	cmd := exec.Command(binary, "migrate")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL="+databaseURL)
	return cmd
}

func TestFreshPostgresMigrationIsRepeatable(t *testing.T) {
	databaseURL := isolatedPostgres(t)
	for range 2 {
		out, err := postgresCommand(databaseURL).CombinedOutput()
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
		if report.Database != "postgres" || report.SchemaVersion != 2 {
			t.Fatalf("unexpected report: %s", out)
		}
	}
}

func legacyPostgres(t *testing.T) string {
	t.Helper()
	databaseURL := isolatedPostgres(t)
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
 CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL);
 INSERT INTO schema_migrations VALUES(1,'2026-10-01T00:00:00.000Z');
 CREATE TABLE entities(kind TEXT NOT NULL,id TEXT NOT NULL,data JSONB NOT NULL,version INTEGER NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(kind,id));
 CREATE INDEX entities_kind ON entities(kind);
 CREATE TABLE identities(identity TEXT PRIMARY KEY,project_id TEXT NOT NULL);
 CREATE TABLE retries(scope TEXT NOT NULL,key TEXT NOT NULL,request TEXT NOT NULL,response TEXT NOT NULL,PRIMARY KEY(scope,key));
 CREATE TABLE record_search(record_id TEXT PRIMARY KEY,title TEXT NOT NULL,content TEXT NOT NULL,document TSVECTOR GENERATED ALWAYS AS(to_tsvector('simple',title || ' ' || content)) STORED);
 CREATE INDEX record_search_document ON record_search USING GIN(document);`)
	if err != nil {
		t.Fatal(err)
	}
	return databaseURL
}

func TestRestoredPostgresSurvivesCompetingUpgradeAndRestart(t *testing.T) {
	databaseURL := legacyPostgres(t)
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	seedRestoredHistory(t, db)
	db.Close()
	commands := []*exec.Cmd{postgresCommand(databaseURL), postgresCommand(databaseURL), postgresCommand(databaseURL)}
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
	for range 2 {
		t.Run("startup", func(t *testing.T) {
			origin := startServer(t, "", databaseURL)
			status, value := fetchRecord(t, origin, restoredToken)
			if status != 200 {
				t.Fatalf("restored credential failed: %d %+v", status, value)
			}
			var expected map[string]any
			json.Unmarshal([]byte(restoredRecord), &expected)
			if !reflect.DeepEqual(value["record"], expected) {
				t.Fatal("immutable PostgreSQL capture changed")
			}
		})
	}
}

func TestPostgresFailedMigrationIsRetryable(t *testing.T) {
	databaseURL := legacyPostgres(t)
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE entities_created(id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if out, err := postgresCommand(databaseURL).CombinedOutput(); err == nil {
		t.Fatalf("conflicting migration accepted: %s", out)
	}
	if _, err := db.Exec(`DROP TABLE entities_created`); err != nil {
		t.Fatal(err)
	}
	if out, err := postgresCommand(databaseURL).CombinedOutput(); err != nil {
		t.Fatalf("retry failed: %v %s", err, out)
	}
}

func TestPostgresRejectsNewerAndMalformedSchema(t *testing.T) {
	for _, statement := range []string{`INSERT INTO schema_migrations VALUES(999,'2026-10-01T00:00:00Z')`, `ALTER TABLE entities DROP CONSTRAINT entities_pkey`, `DROP INDEX record_search_document`} {
		t.Run(statement, func(t *testing.T) {
			databaseURL := legacyPostgres(t)
			db, err := sql.Open("pgx", databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if out, err := postgresCommand(databaseURL).CombinedOutput(); err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
				t.Fatalf("unsupported schema accepted: %v %s", err, out)
			}
		})
	}
}

func TestInterruptedPostgresStartupCanBeRetried(t *testing.T) {
	databaseURL := legacyPostgres(t)
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(1397772880)`); err != nil {
		t.Fatal(err)
	}
	cmd := postgresCommand(databaseURL)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	cmd.Process.Kill()
	if err := cmd.Wait(); err == nil {
		t.Fatal("startup did not wait for the migration lock")
	}
	tx.Rollback()
	if out, err := postgresCommand(databaseURL).CombinedOutput(); err != nil {
		t.Fatalf("restart after interruption failed: %v %s", err, out)
	}
}

func TestPostgresSSHLoginThroughRealStdioMCP(t *testing.T) {
	databaseURL := legacyPostgres(t)
	private := filepath.Join(t.TempDir(), "identity")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", private).CombinedOutput(); err != nil {
		t.Fatalf("synthetic key generation failed: %v %s", err, out)
	}
	public, err := os.ReadFile(private + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Join(strings.Fields(string(public))[:2], " ")
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	seedRestoredHistory(t, db)
	if _, err := db.Exec(`UPDATE entities SET data=jsonb_set(data,'{publicKey}',to_jsonb($1::text)) WHERE kind='credential'`, key); err != nil {
		t.Fatal(err)
	}
	db.Close()
	origin := startServer(t, "", databaseURL)
	_, proof := signedChallenge(t, origin, private, key)
	status, response := postJSON(t, origin, "/api/v1/auth/mcp/verify", proof)
	if status != 200 {
		t.Fatalf("PostgreSQL SSH proof failed: %d %+v", status, response)
	}
	status, _ = fetchRecord(t, origin, response["accessToken"].(string))
	if status != 200 {
		t.Fatal("PostgreSQL SSH session cannot read history")
	}
	cmd := exec.Command(integrationBinary, "-url", origin, "-public-key", private+".pub", "-signing-key", private, "-workspace", filepath.Dir(private), "-binary", mcpBinary, "-phase", "detail", "-record-id", "rec_restored")
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "PASS detail") {
		t.Fatalf("PostgreSQL stdio MCP failed: %v %s", err, out)
	}
}
