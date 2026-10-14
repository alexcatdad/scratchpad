package backend_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestPostgresSemanticCorpusReadDoesNotBlockIndependentCuration(t *testing.T) {
	path, databaseURL := portableInstance(t, "postgres")
	origin := startServer(t, path, databaseURL)
	if status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0}); status != 200 {
		t.Fatal("settings fixture failed")
	}
	project := jobProject(t, origin, "Semantic read participant", true, false)
	record := curationCapture(t, origin, project, "Immutable semantic source")
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	barrier, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer barrier.Rollback()
	// Pause the native corpus query, without locking entities used by metadata
	// curation. Public writes must not inherit the semantic reader's writer lock.
	if _, err = barrier.ExecContext(ctx, "LOCK TABLE context_headers IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	type response struct {
		status int
		body   map[string]any
	}
	semantic := make(chan response, 1)
	go func() {
		status, body := curationRequest(t, origin, "POST", "/search/semantic", map[string]any{"projectId": project, "query": "Synthetic semantic query"})
		semantic <- response{status, body}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE relation='context_headers'::regclass AND NOT granted)").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("semantic search did not reach corpus read barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	curation := make(chan response, 1)
	go func() {
		status, body := curationRequest(t, origin, "PATCH", "/records/"+record["id"].(string)+"/metadata", map[string]any{"expectedVersion": 1, "tags": []any{"responsive"}})
		curation <- response{status, body}
	}()
	var edit response
	blocked := false
	select {
	case edit = <-curation:
	case <-time.After(3 * time.Second):
		blocked = true
	}
	if err = barrier.Rollback(); err != nil {
		t.Fatal(err)
	}
	if blocked {
		edit = <-curation
	}
	search := <-semantic
	if blocked {
		t.Fatal("paused read-only semantic corpus scan blocked independent metadata curation")
	}
	if edit.status != 200 {
		t.Fatalf("independent curation failed: HTTP%d %v", edit.status, edit.body)
	}
	if search.status != 200 || search.body["indexRequired"] != true || len(search.body["results"].([]any)) != 0 {
		t.Fatalf("unindexed corpus did not retain ordinary semantic response: HTTP%d %v", search.status, search.body)
	}
	status, detail := curationRequest(t, origin, "GET", "/records/"+record["id"].(string), nil)
	if status != 200 || detail["metadata"].(map[string]any)["tags"].([]any)[0] != "responsive" {
		t.Fatal("responsive metadata edit was not persisted")
	}
}
