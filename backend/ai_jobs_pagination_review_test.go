package backend_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSQLiteJobListingIgnoresMalformedOffPageBodiesAndFailsSelectedBodiesSafely(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for id, raw := range map[string]string{"job_good": `{"id":"job_good","createdAt":"2026-10-01T00:00Z","status":"queued"}`, "job_corrupt": `synthetic-private-corrupt-body`, "job_null": `null`} {
		if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_job',$1,$2,1,'2026-10-01T00:00Z')`, id, raw); err != nil {
			t.Fatal(err)
		}
	}
	origin := startServer(t, path, databaseURL)
	for _, query := range []string{"limit=1", "jobId=job_good"} {
		status, result := curationRequest(t, origin, "GET", "/ai/jobs?"+query, nil)
		if status != 200 || len(result["jobs"].([]any)) != 1 || result["hasActive"] != true {
			t.Fatalf("malformed off-page history blocked valid listing: HTTP %d", status)
		}
	}
	for _, id := range []string{"job_corrupt", "job_null"} {
		status, result := curationRequest(t, origin, "GET", "/ai/jobs?jobId="+id, nil)
		raw, _ := json.Marshal(result)
		if status != 503 || strings.Contains(string(raw), "synthetic-private-corrupt-body") {
			t.Fatalf("selected malformed %s body did not fail safely: HTTP %d", id, status)
		}
	}
}

func TestPostgresJobPageAndActiveFlagShareSnapshotDuringCompletion(t *testing.T) {
	path, databaseURL := portableInstance(t, "postgres")
	origin := startServer(t, path, databaseURL)
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_job','job_snapshot','{"id":"job_snapshot","createdAt":"2026-10-01T00:00Z","status":"queued"}',1,'2026-10-01T00:00Z');
ALTER TABLE entities RENAME TO snapshot_entities;
CREATE FUNCTION job_read_gate(kind TEXT,value JSONB) RETURNS JSONB LANGUAGE plpgsql VOLATILE AS $$ BEGIN IF kind='ai_job' THEN PERFORM pg_advisory_xact_lock(77494); END IF; RETURN value; END $$;
CREATE VIEW entities AS SELECT kind,id,job_read_gate(kind,data) AS data,version,created_at FROM snapshot_entities`)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lock, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err = lock.ExecContext(ctx, `SELECT pg_advisory_lock(77494)`); err != nil {
		t.Fatal(err)
	}
	defer lock.ExecContext(context.Background(), `SELECT pg_advisory_unlock(77494)`)
	type response struct {
		status int
		body   map[string]any
	}
	done := make(chan response, 1)
	go func() {
		status, body := curationRequest(t, origin, "GET", "/ai/jobs", nil)
		done <- response{status, body}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=77494 AND NOT granted)`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job listing did not reach snapshot barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = db.ExecContext(ctx, `UPDATE snapshot_entities SET data=jsonb_set(data,'{status}','"completed"'::jsonb),version=version+1 WHERE kind='ai_job' AND id='job_snapshot'`); err != nil {
		t.Fatal(err)
	}
	if _, err = lock.ExecContext(ctx, `SELECT pg_advisory_unlock(77494)`); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.status != 200 || result.body["jobs"].([]any)[0].(map[string]any)["status"] != "queued" || result.body["hasActive"] != true {
		t.Fatalf("queued page and active flag came from different snapshots: HTTP %d %+v", result.status, result.body)
	}
	status, fresh := curationRequest(t, origin, "GET", "/ai/jobs", nil)
	if status != 200 || fresh["jobs"].([]any)[0].(map[string]any)["status"] != "completed" || fresh["hasActive"] != false {
		t.Fatal("later job poll did not observe committed completion")
	}
}

func TestAIJobListingBoundsHistoryAndKeepsOldActiveJobsReachable(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 205; i++ {
				id, status := fmt.Sprintf("job_%03d", i), "completed"
				if i == 204 {
					status = "queued"
				}
				job := map[string]any{"id": id, "version": 1, "createdAt": "2026-10-01T00:00Z", "status": status, "type": "analyze", "attempts": 0}
				raw, _ := json.Marshal(job)
				stored := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second).Format(time.RFC3339)
				if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_job',$1,$2,1,$3)`, id, string(raw), stored); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			db.Close()
			origin := startServer(t, path, databaseURL)
			status, result := curationRequest(t, origin, "GET", "/ai/jobs", nil)
			if status != 200 || len(result["jobs"].([]any)) != 100 || result["hasActive"] != true {
				t.Fatalf("default job listing did not bound history and retain active indicator: HTTP %d, jobs %d", status, len(result["jobs"].([]any)))
			}
			if result["jobs"].([]any)[0].(map[string]any)["id"] != "job_000" || result["jobs"].([]any)[99].(map[string]any)["id"] != "job_099" {
				t.Fatal("historical timestamp ties changed job ordering")
			}
			seen := map[string]bool{}
			for page := 0; ; page++ {
				for _, raw := range result["jobs"].([]any) {
					id := raw.(map[string]any)["id"].(string)
					if seen[id] {
						t.Fatalf("pagination repeated job %s", id)
					}
					seen[id] = true
				}
				cursor, exists := result["nextCursor"].(string)
				if !exists || cursor == "" {
					break
				}
				if page > 3 {
					t.Fatal("pagination did not terminate")
				}
				status, result = curationRequest(t, origin, "GET", "/ai/jobs?cursor="+url.QueryEscape(cursor), nil)
				if status != 200 || len(result["jobs"].([]any)) > 100 {
					t.Fatal("continuation was not bounded")
				}
			}
			if len(seen) != 205 {
				t.Fatalf("pagination lost retained history: %d", len(seen))
			}
			if len(waitRAGJobs(t, origin, 205)) != 205 || len(scheduledJobs(t, origin, 205)) != 205 {
				t.Fatal("inventory polling helpers truncated retained job history")
			}
			if waitAIJob(t, origin, "job_204", "queued")["id"] != "job_204" || waitProviderReviewJob(t, origin, "job_203")["id"] != "job_203" {
				t.Fatal("identity polling helpers lost jobs outside the default page")
			}
			status, result = curationRequest(t, origin, "GET", "/ai/jobs?jobId=job_204", nil)
			if status != 200 || len(result["jobs"].([]any)) != 1 || result["jobs"].([]any)[0].(map[string]any)["status"] != "queued" {
				t.Fatal("old active job cannot be polled by identity")
			}
			status, result = curationRequest(t, origin, "GET", "/ai/jobs?limit=2", nil)
			if status != 200 || len(result["jobs"].([]any)) != 2 {
				t.Fatal("explicit job page size ignored")
			}
			status, result = curationRequest(t, origin, "GET", "/ai/jobs?limit=200", nil)
			if status != 200 || len(result["jobs"].([]any)) != 200 || result["nextCursor"] == nil {
				t.Fatal("maximum page size or continuation changed")
			}
			status, result = curationRequest(t, origin, "GET", "/ai/jobs?jobId=job_missing", nil)
			if status != 200 || len(result["jobs"].([]any)) != 0 || result["hasActive"] != true || result["nextCursor"] != nil {
				t.Fatal("specific missing job lookup changed global active state")
			}
			for _, query := range []string{"limit=0", "limit=201", "limit=no", "limit=", "cursor=", "jobId=", "cursor=invalid", "jobId=job_204&cursor=invalid"} {
				status, _ = curationRequest(t, origin, "GET", "/ai/jobs?"+query, nil)
				if status != 400 {
					t.Fatalf("invalid pagination accepted: %s HTTP %d", query, status)
				}
			}
			for _, id := range []string{"../job", "job/unsafe", "job%2Funsafe", "job\nunsafe", strings.Repeat("a", 501)} {
				status, _ = curationRequest(t, origin, "GET", "/ai/jobs?jobId="+url.QueryEscape(id), nil)
				if status != 400 {
					t.Fatalf("unsafe specific job identity accepted: HTTP %d", status)
				}
			}
		})
	}
}
