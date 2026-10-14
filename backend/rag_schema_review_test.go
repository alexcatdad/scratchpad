package backend_test

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Restored SQL is input; acceptance is observed through the public server CLI.
func TestRAGStartupRejectsAlteredProjectionSchema(t *testing.T) {
	for _, fixture := range []struct{ name, sql string }{
		{"constrained vector", `ALTER TABLE rag_passages ALTER COLUMN embedding TYPE vector(2)`},
		{"wrong scalar type", `ALTER TABLE rag_passages ALTER COLUMN source_start TYPE BIGINT`},
		{"nullable content", `ALTER TABLE rag_passages ALTER COLUMN text DROP NOT NULL`},
		{"identity source column", `ALTER TABLE rag_passages ALTER COLUMN source_start ADD GENERATED ALWAYS AS IDENTITY`},
		{"extra column", `ALTER TABLE rag_passages ADD COLUMN extra TEXT`},
		{"missing primary key", `ALTER TABLE rag_passages DROP CONSTRAINT rag_passages_pkey`},
		{"reversed primary key", `ALTER TABLE rag_passages DROP CONSTRAINT rag_passages_pkey; ALTER TABLE rag_passages ADD PRIMARY KEY(fingerprint,id)`},
		{"missing source check", `ALTER TABLE rag_passages DROP CONSTRAINT rag_passages_check`},
		{"weakened dimensions check", `ALTER TABLE rag_passages DROP CONSTRAINT rag_passages_check1; ALTER TABLE rag_passages ADD CHECK(dimensions BETWEEN 1 AND 16000)`},
		{"unvalidated source check", `ALTER TABLE rag_passages DROP CONSTRAINT rag_passages_check; ALTER TABLE rag_passages ADD CHECK(source_start>=0 AND source_end>=source_start) NOT VALID`},
		{"unenforced source check", `ALTER TABLE rag_passages DROP CONSTRAINT rag_passages_check; ALTER TABLE rag_passages ADD CHECK(source_start>=0 AND source_end>=source_start) NOT ENFORCED`},
		{"missing scope index", `DROP INDEX rag_passages_scope`},
		{"reordered scope index", `DROP INDEX rag_passages_scope; CREATE INDEX rag_passages_scope ON rag_passages(fingerprint,project_id,record_id)`},
		{"partial scope index", `DROP INDEX rag_passages_scope; CREATE INDEX rag_passages_scope ON rag_passages(project_id,fingerprint,record_id) WHERE dimensions=2`},
		{"scope index on another table", `DROP INDEX rag_passages_scope; CREATE TABLE unrelated_rag(project_id TEXT,fingerprint TEXT,record_id TEXT); CREATE INDEX rag_passages_scope ON unrelated_rag(project_id,fingerprint,record_id)`},
		{"marker without primary key", `ALTER TABLE rag_projection_migrations DROP CONSTRAINT rag_projection_migrations_pkey`},
		{"nullable marker", `ALTER TABLE rag_projection_migrations ALTER COLUMN applied_at DROP NOT NULL`},
		{"wrong marker type", `ALTER TABLE rag_projection_migrations ALTER COLUMN version TYPE BIGINT`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			path, databaseURL := ragInstance(t)
			t.Run("supported migration", func(t *testing.T) { startServer(t, path, databaseURL) })
			db, err := sql.Open("pgx", databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if fixture.name == "unenforced source check" {
				var version int
				if err := db.QueryRow(`SHOW server_version_num`).Scan(&version); err != nil {
					t.Fatal(err)
				}
				if version < 180000 {
					t.Skip("NOT ENFORCED requires PostgreSQL 18")
				}
			}
			if _, err := db.Exec(fixture.sql); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "serve")
			cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL="+databaseURL, "SCRATCHPAD_DATABASE_PATH="+path, "SCRATCHPAD_LISTEN_ADDR=127.0.0.1:0", "SCRATCHPAD_PUBLIC_URL=http://localhost:3000")
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
				t.Fatalf("altered projection accepted or failed unsafely: %v %s", err, out)
			}
		})
	}
}
