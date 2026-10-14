package backend_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TestPostgresRecordDetailMetadataAndCurationHistoryShareSnapshot(t *testing.T) {
	path, databaseURL := portableInstance(t, "postgres")
	origin := startServer(t, path, databaseURL)
	project := jobProject(t, origin, "Consistent record detail", false, false)
	record := curationCapture(t, origin, project, "Immutable snapshot source")
	id := record["id"].(string)
	status, before := curationRequest(t, origin, "GET", "/records/"+id, nil)
	if status != 200 {
		t.Fatal("initial detail unavailable")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Pause the read-only metadata statement after PostgreSQL establishes its
	// snapshot. The writable view forwards the real HTTP curation transaction;
	// neither its data nor the returned history is supplied by the barrier.
	_, err = db.Exec(`ALTER TABLE entities RENAME TO detail_snapshot_entities;
CREATE FUNCTION detail_read_gate(kind TEXT,value JSONB) RETURNS JSONB LANGUAGE plpgsql VOLATILE AS $$
BEGIN IF kind='metadata' AND current_setting('transaction_read_only')='on' THEN PERFORM pg_advisory_xact_lock(77821); END IF; RETURN value; END $$;
CREATE VIEW entities AS SELECT kind,id,detail_read_gate(kind,data) AS data,version,created_at FROM detail_snapshot_entities;
CREATE FUNCTION detail_forward_write() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN INSERT INTO detail_snapshot_entities(kind,id,data,version,created_at) VALUES(NEW.kind,NEW.id,NEW.data,NEW.version,NEW.created_at);
 ELSE UPDATE detail_snapshot_entities SET data=NEW.data,version=NEW.version,created_at=NEW.created_at WHERE kind=OLD.kind AND id=OLD.id;
 END IF; RETURN NEW;
END $$;
CREATE TRIGGER detail_write INSTEAD OF INSERT OR UPDATE ON entities FOR EACH ROW EXECUTE FUNCTION detail_forward_write()`)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err = lock.ExecContext(context.Background(), `SELECT pg_advisory_lock(77821)`); err != nil {
		t.Fatal(err)
	}
	defer lock.ExecContext(context.Background(), `SELECT pg_advisory_unlock(77821)`)
	type response struct {
		status int
		body   map[string]any
	}
	done := make(chan response, 1)
	go func() {
		status, body := curationRequest(t, origin, "GET", "/records/"+id, nil)
		done <- response{status, body}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=77821 AND NOT granted)`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detail did not reach the metadata snapshot barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, curated := curationRequest(t, origin, "PATCH", "/records/"+id+"/metadata", map[string]any{"expectedVersion": 1, "tags": []any{"reviewed"}})
	if status != 200 {
		t.Fatalf("independent public curation failed: HTTP%d", status)
	}
	if _, err = lock.ExecContext(context.Background(), `SELECT pg_advisory_unlock(77821)`); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.status != 200 {
		t.Fatalf("snapshot detail failed: HTTP%d", result.status)
	}
	for _, field := range []string{"record", "metadata", "revisions", "audit"} {
		if !reflect.DeepEqual(result.body[field], before[field]) {
			t.Fatalf("detail mixed curation snapshots in %s", field)
		}
	}
	status, fresh := curationRequest(t, origin, "GET", "/records/"+id, nil)
	if status != 200 || !reflect.DeepEqual(fresh["metadata"], curated["metadata"]) || len(fresh["revisions"].([]any)) != len(before["revisions"].([]any))+1 || len(fresh["audit"].([]any)) != len(before["audit"].([]any))+1 {
		t.Fatal("later detail did not observe complete committed curation")
	}
}
