package backend_test

import (
	"database/sql"
	"strings"
	"testing"
)

// SQL creates restored installation input; migration acceptance is asserted
// through the public administrator CLI, without reading implementation state.
func TestPostgresStartupRejectsAlteredSearchDocument(t *testing.T) {
	databaseURL := legacyPostgres(t)
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP INDEX record_search_document; ALTER TABLE record_search DROP COLUMN document; ALTER TABLE record_search ADD COLUMN document TSVECTOR GENERATED ALWAYS AS(to_tsvector('simple',title)) STORED; CREATE INDEX record_search_document ON record_search USING GIN(document)`); err != nil {
		t.Fatal(err)
	}
	out, err := postgresCommand(databaseURL).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
		t.Fatalf("altered search document accepted: %v %s", err, out)
	}
}

func TestPostgresStartupRejectsSearchIndexOnAnotherTable(t *testing.T) {
	databaseURL := legacyPostgres(t)
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP INDEX record_search_document; CREATE TABLE unrelated_search(document TSVECTOR); CREATE INDEX record_search_document ON unrelated_search USING GIN(document)`); err != nil {
		t.Fatal(err)
	}
	out, err := postgresCommand(databaseURL).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
		t.Fatalf("misdirected search index accepted: %v %s", err, out)
	}
}

func TestPostgresStartupRejectsMalformedSearchColumns(t *testing.T) {
	for _, input := range []struct{ name, statement string }{
		{"missing primary key", `ALTER TABLE record_search DROP CONSTRAINT record_search_pkey`},
		{"wrong column type", `ALTER TABLE record_search ALTER COLUMN record_id TYPE INTEGER USING record_id::integer`},
		{"nullable title", `ALTER TABLE record_search ALTER COLUMN title DROP NOT NULL`},
		{"extra column", `ALTER TABLE record_search ADD COLUMN extra TEXT`},
		{"ordinary document", `DROP INDEX record_search_document; ALTER TABLE record_search DROP COLUMN document; ALTER TABLE record_search ADD COLUMN document TSVECTOR; CREATE INDEX record_search_document ON record_search USING GIN(document)`},
	} {
		t.Run(input.name, func(t *testing.T) {
			databaseURL := legacyPostgres(t)
			db, err := sql.Open("pgx", databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(input.statement); err != nil {
				t.Fatal(err)
			}
			out, err := postgresCommand(databaseURL).CombinedOutput()
			if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
				t.Fatalf("malformed search installation accepted: %v %s", err, out)
			}
		})
	}
}
