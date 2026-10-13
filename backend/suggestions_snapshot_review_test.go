package backend_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestPostgresSuggestionReadKeepsCurationResponsiveAndRechecksCurrentConsent(t *testing.T) {
	for _, mode := range []string{"curation", "consent"} {
		t.Run(mode, func(t *testing.T) {
			path, databaseURL := portableInstance(t, "postgres")
			origin := startServer(t, path, databaseURL)
			suggestionFixture(t, origin, "summary", map[string]any{"text": "Consistent readable source interpretation"})
			db, err := sql.Open("pgx", databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			// This synthetic predicate view remains auto-updatable. Only artifact
			// reads wait; unrelated metadata/project writes retain their real path.
			_, err = db.Exec(`ALTER TABLE entities RENAME TO suggestion_entities;
CREATE FUNCTION suggestion_read_gate(kind TEXT) RETURNS BOOLEAN LANGUAGE plpgsql VOLATILE AS $$ BEGIN IF kind='ai_artifact' THEN PERFORM pg_advisory_xact_lock(77497); END IF; RETURN true; END $$;
CREATE VIEW entities AS SELECT * FROM suggestion_entities WHERE suggestion_read_gate(kind)`)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			lock, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if _, err = lock.ExecContext(ctx, "SELECT pg_advisory_lock(77497)"); err != nil {
				t.Fatal(err)
			}
			defer lock.ExecContext(context.Background(), "SELECT pg_advisory_unlock(77497)")
			type response struct {
				status int
				body   map[string]any
			}
			done := make(chan response, 1)
			go func() {
				status, body := curationRequest(t, origin, "GET", "/suggestions?projectId=proj_portable&limit=1", nil)
				done <- response{status, body}
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var waiting bool
				if err = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=77497 AND NOT granted)").Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("suggestion read did not reach artifact barrier")
				}
				time.Sleep(10 * time.Millisecond)
			}
			edited := make(chan int, 1)
			go func() {
				status := 0
				if mode == "curation" {
					status, _ = curationRequest(t, origin, "PATCH", "/records/rec:portable/metadata", map[string]any{"expectedVersion": 1, "tags": []any{"responsive"}})
				} else {
					status, project := projectRequest(t, origin, "GET", "/proj_portable", nil, restoredToken)
					if status != 200 {
						edited <- status
						return
					}
					current := project["project"].(map[string]any)
					settings := current["settings"].(map[string]any)
					settings["aiProcessing"] = false
					status, _ = projectRequest(t, origin, "PATCH", "/proj_portable/settings", map[string]any{"expectedVersion": current["version"], "settings": settings}, restoredToken)
					edited <- status
					return
				}
				edited <- status
			}()
			status, blocked := 0, false
			select {
			case status = <-edited:
			case <-time.After(3 * time.Second):
				blocked = true
			}
			if _, err = lock.ExecContext(ctx, "SELECT pg_advisory_unlock(77497)"); err != nil {
				t.Fatal(err)
			}
			if blocked {
				status = <-edited
			}
			result := <-done
			if blocked {
				t.Fatal("paused suggestion read blocked independent write")
			}
			if status != 200 {
				t.Fatalf("independent write failed: HTTP%d", status)
			}
			if mode == "consent" {
				if result.status != 403 || result.body["error"].(map[string]any)["code"] != "AI_NOT_ALLOWED" {
					t.Fatal("withdrawn snapshot suggestion became readable")
				}
			} else if result.status != 200 || len(result.body["suggestions"].([]any)) != 1 || result.body["hasMore"] != false {
				t.Fatal("consistent readable suggestion missing")
			}
		})
	}
}
