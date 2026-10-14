package backend_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"testing"
	"time"
)

func TestSearchOrdersImportedTimestampPrecisionChronologically(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			var archive map[string]any
			json.Unmarshal([]byte(exactNumberArchive), &archive)
			data := archive["data"].(map[string]any)
			base := data["record"].([]any)[0].(map[string]any)
			records, metadata := []any{}, []any{}
			for _, fixture := range []struct{ id, at string }{{"rec_zero_a", "2026-10-01T00:00Z"}, {"rec_zero_b", "2026-10-01T00:00:00Z"}, {"rec_fraction_low", "2026-10-01T00:00:00.0000000001Z"}, {"rec_fraction_high", "2026-10-01T00:00:00.0000000002Z"}, {"rec_second", "2026-10-01T00:00:01Z"}} {
				record := map[string]any{}
				for key, value := range base {
					record[key] = value
				}
				record["id"], record["createdAt"] = fixture.id, fixture.at
				records = append(records, record)
				metadata = append(metadata, map[string]any{"id": fixture.id, "recordId": fixture.id, "version": 1, "createdAt": fixture.at, "tags": []any{}, "archived": false})
			}
			data["record"], data["metadata"] = records, metadata
			status, result := curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 {
				t.Fatalf("timestamp archive rejected: %d %v", status, result)
			}
			query := "projectId=" + base["projectId"].(string)
			status, full := searchRecords(t, origin, query)
			if status != 200 || len(full["records"].([]any)) != 5 {
				t.Fatal("full history unavailable")
			}
			cursor := ""
			for i, id := range []string{"rec_second", "rec_fraction_high", "rec_fraction_low", "rec_zero_b", "rec_zero_a"} {
				if full["records"].([]any)[i].(map[string]any)["id"] != id {
					t.Fatalf("history chronology changed: %v", full)
				}
				status, page := searchRecords(t, origin, query+"&limit=1&cursor="+url.QueryEscape(cursor))
				if status != 200 || len(page["records"].([]any)) != 1 || page["records"].([]any)[0].(map[string]any)["id"] != id {
					t.Fatalf("cursor lost chronological evidence: %d %v", status, page)
				}
				cursor, _ = page["nextCursor"].(string)
			}
		})
	}
}

func TestPostgresSearchMetadataFilterAndReturnedMetadataShareSnapshot(t *testing.T) {
	path, databaseURL := portableInstance(t, "postgres")
	origin := startServer(t, path, databaseURL)
	projectID := jobProject(t, origin, "Snapshot metadata", false, false)
	record := curationCapture(t, origin, projectID, "Consistent metadata")
	id := record["id"].(string)
	status, _ := curationRequest(t, origin, "PATCH", "/records/"+id+"/metadata", map[string]any{"expectedVersion": 1, "tags": []any{"selected"}})
	if status != 200 {
		t.Fatal("metadata fixture failed")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Pause the metadata read inside PostgreSQL after its statement snapshot is
	// established, then commit an external edit before the request continues.
	_, err = db.Exec(`ALTER TABLE entities RENAME TO snapshot_entities;
CREATE FUNCTION metadata_read_gate(kind TEXT,value JSONB) RETURNS JSONB LANGUAGE plpgsql VOLATILE AS $$ BEGIN IF kind='metadata' THEN PERFORM pg_advisory_xact_lock(77493); END IF; RETURN value; END $$;
CREATE VIEW entities AS SELECT kind,id,metadata_read_gate(kind,data) AS data,version,created_at FROM snapshot_entities`)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err = lock.ExecContext(context.Background(), `SELECT pg_advisory_lock(77493)`); err != nil {
		t.Fatal(err)
	}
	defer lock.ExecContext(context.Background(), `SELECT pg_advisory_unlock(77493)`)
	type response struct {
		status int
		body   map[string]any
	}
	done := make(chan response, 1)
	go func() {
		status, body := searchRecords(t, origin, "projectId="+projectID+"&tag=selected")
		done <- response{status, body}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=77493 AND NOT granted)`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("search did not reach the metadata read barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = db.Exec(`UPDATE snapshot_entities SET data=jsonb_set(data,'{tags}','["changed"]'::jsonb),version=version+1 WHERE kind='metadata' AND id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = lock.ExecContext(context.Background(), `SELECT pg_advisory_unlock(77493)`); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.status != 200 || len(result.body["records"].([]any)) != 1 {
		t.Fatalf("snapshot search unavailable: %d %v", result.status, result.body)
	}
	metadata := result.body["records"].([]any)[0].(map[string]any)["metadata"].(map[string]any)
	if len(metadata["tags"].([]any)) != 1 || metadata["tags"].([]any)[0] != "selected" {
		t.Fatalf("selected metadata and returned metadata came from different snapshots: %v", result.body)
	}
	status, fresh := searchRecords(t, origin, "projectId="+projectID+"&tag=changed")
	if status != 200 || len(fresh["records"].([]any)) != 1 || fresh["records"].([]any)[0].(map[string]any)["metadata"].(map[string]any)["tags"].([]any)[0] != "changed" {
		t.Fatalf("later search did not observe committed metadata: %d %v", status, fresh)
	}
}
