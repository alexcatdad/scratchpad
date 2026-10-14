package backend_test

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Startup failures name a sanitized class so operators can tell a privilege
// problem from an unsupported schema without the database URL in the log.
func TestStartupFailureNamesSQLiteClass(t *testing.T) {
	unsafe := legacyFixture(t)
	if err := os.Chmod(unsafe, 0644); err != nil {
		t.Fatal(err)
	}
	out, err := migrationCommand(unsafe).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY (storage)") {
		t.Fatalf("unsafe storage class missing: %v %s", err, out)
	}
	newer := legacyFixture(t)
	db, err := sql.Open("sqlite", newer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations VALUES(999,'2026-10-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	out, err = migrationCommand(newer).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY (schema)") {
		t.Fatalf("schema class missing: %v %s", err, out)
	}
}

func TestStartupFailureNamesConnectionClassWithoutCredentials(t *testing.T) {
	cmd := exec.Command(binary, "migrate")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL=postgres://synthetic_owner:synthetic_secret_must_not_leak@127.0.0.1:1/synthetic_database")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY (connection)") || strings.Contains(string(out), "synthetic_secret_must_not_leak") || strings.Contains(string(out), "synthetic_owner") {
		t.Fatalf("unsafe or unclassified connection failure: %v %s", err, out)
	}
}

// Production separates the table-owning migrator from the application role.
// An application role that may read and write but not own the tables cannot
// apply DDL migrations; startup must say so and leave the schema unchanged.
func TestPostgresNonOwnerMigrationReportsPermission(t *testing.T) {
	databaseURL := legacyPostgres(t)
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	role, password := "scratchpad_app_test_"+hex.EncodeToString(suffix[:]), hex.EncodeToString(suffix[:])+"-synthetic"
	if _, err := db.Exec(`CREATE ROLE "` + role + `" LOGIN PASSWORD '` + password + `'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(`DROP OWNED BY "` + role + `"`)
		db.Exec(`DROP ROLE "` + role + `"`)
	})
	for _, statement := range []string{
		`GRANT USAGE, CREATE ON SCHEMA public TO "` + role + `"`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "` + role + `"`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	u, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(role, password)
	out, err := postgresCommand(u.String()).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY (permission, SQLSTATE 42501)") || strings.Contains(string(out), password) {
		t.Fatalf("non-owner migration not reported as permission: %v %s", err, out)
	}
	var versions int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&versions); err != nil || versions != 1 {
		t.Fatalf("refused migration changed the schema: %d %v", versions, err)
	}
}

// libpq-style options carry spaces ("-c name=value"); they must reach the
// server as written so an operator can select a role or search path.
func TestPostgresURLOptionsKeepSpaces(t *testing.T) {
	u, err := url.Parse(isolatedPostgres(t))
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("options", "-c application_name=scratchpad_options_test")
	u.RawQuery = query.Encode()
	if out, err := postgresCommand(u.String()).CombinedOutput(); err != nil {
		t.Fatalf("spaced options rejected: %v %s", err, out)
	}
}
