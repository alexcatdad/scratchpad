package backend_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func TestPostgresNativeImportCompletesPastOrdinaryRequestDeadline(t *testing.T) {
	path, databaseURL := portableInstance(t, "postgres")
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Database latency is input to this public import, not a mocked private
	// collaborator. One source insert crosses the former 25-second budget.
	if _, err = db.Exec(`CREATE FUNCTION import_latency_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='record' AND NEW.id='rec:portable' THEN PERFORM pg_sleep(26); END IF; RETURN NEW; END $$; CREATE TRIGGER import_latency_fixture BEFORE INSERT ON entities FOR EACH ROW EXECUTE FUNCTION import_latency_fixture()`); err != nil {
		t.Fatal(err)
	}
	origin := startServer(t, path, databaseURL)
	archive := portableArchive()
	status, result := curationRequest(t, origin, "POST", "/import", archive)
	if status != 200 || result["imported"] != float64(9) || result["skipped"] != float64(0) {
		t.Fatalf("valid import exceeded the ordinary request budget: HTTP %d %+v", status, result)
	}
	status, detail := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
	if status != 200 || !reflect.DeepEqual(detail["record"], archive["data"].(map[string]any)["record"].([]any)[0]) {
		t.Fatal("delayed import lost or rewrote immutable source evidence")
	}
	status, result = curationRequest(t, origin, "POST", "/import", archive)
	if status != 200 || result["imported"] != float64(0) || result["skipped"] != float64(9) {
		t.Fatalf("delayed import did not commit an idempotent archive: HTTP %d %+v", status, result)
	}
}
