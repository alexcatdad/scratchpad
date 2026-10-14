package backend_test

import (
	"bytes"
	"database/sql"
	"os"
	"strings"
	"testing"
)

func TestSQLiteStartupRejectsUnindexedSearchContent(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE record_search; CREATE VIRTUAL TABLE record_search USING fts5(record_id UNINDEXED,title,content UNINDEXED)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := migrationCommand(path).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
		t.Fatalf("unindexed search content accepted: %v %s", err, out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("refused search installation was modified")
	}
}

func TestSQLiteStartupRejectsOtherAlteredSearchDefinitions(t *testing.T) {
	for _, definition := range []string{
		`fts5(record_id UNINDEXED,title UNINDEXED,content)`,
		`fts5(record_id,title,content)`,
		`fts5(record_id UNINDEXED,title,content,tokenize='porter')`,
		`fts5(record_id UNINDEXED,title,content,extra)`,
	} {
		t.Run(definition, func(t *testing.T) {
			path := legacyFixture(t)
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`DROP TABLE record_search; CREATE VIRTUAL TABLE record_search USING ` + definition); err != nil {
				t.Fatal(err)
			}
			db.Close()
			out, err := migrationCommand(path).CombinedOutput()
			if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
				t.Fatalf("altered search definition accepted: %v %s", err, out)
			}
		})
	}
}

func TestSQLiteRetainedSearchDefinitionSurvivesUpgradeAndRestart(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// This is the retained TypeScript initializer's definition, including its
	// existence guard and formatting; it remains supported schema-one input.
	if _, err := db.Exec(`DROP TABLE record_search;
 CREATE VIRTUAL TABLE IF NOT EXISTS record_search USING fts5(record_id UNINDEXED,title,content);`); err != nil {
		t.Fatal(err)
	}
	seedRestoredHistory(t, db)
	db.Close()
	for range 2 {
		out, err := migrationCommand(path).CombinedOutput()
		if err != nil || !strings.Contains(string(out), `"schemaVersion":3`) {
			t.Fatalf("valid search installation rejected: %v %s", err, out)
		}
	}
	for range 2 {
		t.Run("restart", func(t *testing.T) {
			origin := startServer(t, path, "")
			status, detail := fetchRecord(t, origin, restoredToken)
			if status != 200 || detail["record"].(map[string]any)["title"] != "Preserve original evidence" {
				t.Fatalf("restored history unavailable: %d %v", status, detail)
			}
		})
	}
}
