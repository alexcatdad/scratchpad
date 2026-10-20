package backend_test

import (
	"bytes"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// A restored SQLite projection is input; malformed definitions must be
// refused without repair or modification of the database.
func TestSQLiteStartupRejectsAlteredSearchProjection(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE record_search; CREATE VIRTUAL TABLE record_search USING fts5(record_id UNINDEXED,title,content,extra)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := migrationCommand(path).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
		t.Fatalf("altered search projection accepted: %v %s", err, out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("refused projection schema was modified")
	}
}
