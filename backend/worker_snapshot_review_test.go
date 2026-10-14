package backend_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostgresWorkerInputReadDoesNotBlockIndependentCuration(t *testing.T) {
	for _, kind := range []string{"embed", "analyze"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if kind == "embed" {
					json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []int{1, 0}}}})
				} else {
					json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{"artifacts":[]}`}}}})
				}
			}))
			defer provider.Close()
			path, databaseURL := portableInstance(t, "postgres")
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2, "analysisModes": []any{"summary"}})
			if status != 200 {
				t.Fatal("settings fixture failed")
			}
			project := jobProject(t, origin, "Worker read participant", true, false)
			record := curationCapture(t, origin, project, "Immutable worker source")
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
			// Block only the native source projection read. Metadata writes to
			// entities must remain responsive while the worker input scan waits.
			if _, err = barrier.ExecContext(ctx, "LOCK TABLE context_headers IN ACCESS EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}
			status, queued := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"projectId": project, "type": kind})
			if status != 202 {
				t.Fatal("enqueue failed")
			}
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
					t.Fatal("worker did not reach source read barrier")
				}
				time.Sleep(10 * time.Millisecond)
			}
			edited := make(chan int, 1)
			go func() {
				status, _ := curationRequest(t, origin, "PATCH", "/records/"+record["id"].(string)+"/metadata", map[string]any{"expectedVersion": 1, "tags": []any{"responsive"}})
				edited <- status
			}()
			blocked := false
			select {
			case status = <-edited:
			case <-time.After(3 * time.Second):
				blocked = true
			}
			if err = barrier.Rollback(); err != nil {
				t.Fatal(err)
			}
			if blocked {
				status = <-edited
			}
			job := waitAIJob(t, origin, queued["job"].(map[string]any)["id"].(string), "completed")
			if blocked {
				t.Fatal("paused worker source scan blocked independent metadata curation")
			}
			if status != 200 || calls.Load() != 1 || job["sourceCount"] != float64(1) {
				t.Fatalf("worker or independent curation lost source: HTTP%d job %v calls %d", status, job, calls.Load())
			}
			status, detail := curationRequest(t, origin, "GET", "/records/"+record["id"].(string), nil)
			if status != 200 || detail["metadata"].(map[string]any)["tags"].([]any)[0] != "responsive" {
				t.Fatal("responsive metadata edit was not persisted")
			}
		})
	}
}

func TestHistoricalWorkerDiscardsAdmittedOutputAfterInputConsentWithdrawal(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		for _, kind := range []string{"embed", "analyze"} {
			for _, mode := range []string{"single", "multiple"} {
				t.Run(engine+"/"+kind+"/"+mode, func(t *testing.T) {
					started, release := make(chan struct{}), make(chan struct{})
					var once sync.Once
					unblock := func() { once.Do(func() { close(release) }) }
					var calls atomic.Int32
					provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if calls.Add(1) == 1 {
							close(started)
						}
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
						if kind == "embed" {
							json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []int{1, 0}}}})
						} else {
							json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{"artifacts":[]}`}}}})
						}
					}))
					defer func() { unblock(); provider.Close() }()
					path, databaseURL := portableInstance(t, engine)
					origin := startServer(t, path, databaseURL)
					withdrawn := jobProject(t, origin, "Withdrawn admitted worker input", true, true)
					// An eligible empty project keeps the historical all-project scope
					// valid, so only loaded-source membership can reject activation.
					jobProject(t, origin, "Retained empty participant", true, true)
					if mode == "single" {
						curationCapture(t, origin, withdrawn, "Single admitted source")
					} else {
						status, _ := captureRequest(t, origin, withdrawn, "", map[string]any{"type": "finding", "title": "Multiple input chunks", "authority": "observed", "payload": map[string]any{"finding": strings.Repeat("Synthetic long evidence. ", 1000)}})
						if status != 201 {
							t.Fatal("long source fixture failed")
						}
					}
					driver, dsn := "sqlite", path
					if engine == "postgres" {
						driver, dsn = "pgx", databaseURL
					}
					db, err := sql.Open(driver, dsn)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					jobID := "job_historical_admitted_consent"
					job := map[string]any{"id": jobID, "version": 1, "createdAt": "2026-10-01T00:00:00Z", "type": kind, "scope": map[string]any{"crossProject": true}, "status": "queued", "attempts": 0, "runAfter": "9999-01-01T00:00:00Z"}
					raw, _ := json.Marshal(job)
					if _, err = db.Exec("INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_job',$1,$2,1,'2026-10-01T00:00:00Z')", jobID, string(raw)); err != nil {
						t.Fatal(err)
					}
					status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2, "analysisModes": []any{"summary"}})
					if status != 200 {
						t.Fatal("settings fixture failed")
					}
					job["runAfter"] = "2026-10-01T00:00:00Z"
					raw, _ = json.Marshal(job)
					if _, err = db.Exec("UPDATE entities SET data=$1 WHERE kind='ai_job' AND id=$2", string(raw), jobID); err != nil {
						t.Fatal(err)
					}
					select {
					case <-started:
					case <-time.After(5 * time.Second):
						t.Fatal("historical worker did not dispatch")
					}
					status, response := projectRequest(t, origin, "GET", "/"+withdrawn, nil, restoredToken)
					if status != 200 {
						t.Fatal("project lookup failed")
					}
					project := response["project"].(map[string]any)
					settings := project["settings"].(map[string]any)
					settings["aiProcessing"] = false
					status, _ = projectRequest(t, origin, "PATCH", "/"+withdrawn+"/settings", map[string]any{"expectedVersion": project["version"], "settings": settings}, restoredToken)
					if status != 200 {
						t.Fatal("provider wait blocked consent withdrawal")
					}
					unblock()
					terminal := waitProviderReviewJob(t, origin, jobID)
					if terminal["status"] != "failed" || calls.Load() != 1 {
						t.Fatalf("withdrawn admitted output activated: %v calls %d", terminal, calls.Load())
					}
					// Restore visibility before checking the archive: denied consent
					// must not merely hide outputs that were incorrectly activated.
					settings["aiProcessing"] = true
					status, _ = projectRequest(t, origin, "PATCH", "/"+withdrawn+"/settings", map[string]any{"expectedVersion": project["version"].(float64) + 1, "settings": settings}, restoredToken)
					if status != 200 {
						t.Fatal("consent restoration failed")
					}
					status, archive := curationRequest(t, origin, "POST", "/export", map[string]any{})
					if status != 200 || len(archive["data"].(map[string]any)["ai_artifact"].([]any)) != 0 {
						t.Fatal("failed admitted job retained activated output")
					}
					if kind == "embed" {
						status, result := curationRequest(t, origin, "POST", "/search/semantic", map[string]any{"projectId": withdrawn, "query": "restored input"})
						if status != 200 || result["indexRequired"] != true || len(result["results"].([]any)) != 0 || calls.Load() != 1 {
							t.Fatal("withdrawn admitted embedding was activated")
						}
					}
				})
			}
		}
	}
}

func TestPostgresHistoricalWorkerRejectsInputConsentWithdrawnDuringSnapshot(t *testing.T) {
	for _, kind := range []string{"embed", "analyze"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if kind == "embed" {
					json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []int{1, 0}}}})
				} else {
					json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{"artifacts":[]}`}}}})
				}
			}))
			defer provider.Close()
			path, databaseURL := portableInstance(t, "postgres")
			origin := startServer(t, path, databaseURL)
			withdrawn := jobProject(t, origin, "Withdrawn historical worker participant", true, true)
			retained := jobProject(t, origin, "Retained historical worker participant", true, true)
			curationCapture(t, origin, withdrawn, "Withdrawn source")
			curationCapture(t, origin, retained, "Retained source")
			db, err := sql.Open("pgx", databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			// Supported older queue writers retained all-project scopes. Keep this
			// controlled input dormant until its readonly corpus barrier is ready.
			jobID := "job_historical_input_consent"
			job := map[string]any{"id": jobID, "version": 1, "createdAt": "2026-10-01T00:00:00Z", "type": kind, "scope": map[string]any{"crossProject": true}, "status": "queued", "attempts": 0, "runAfter": "9999-01-01T00:00:00Z"}
			raw, _ := json.Marshal(job)
			if _, err = db.Exec("INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_job',$1,$2,1,'2026-10-01T00:00:00Z')", jobID, string(raw)); err != nil {
				t.Fatal(err)
			}
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2, "analysisModes": []any{"summary"}})
			if status != 200 {
				t.Fatal("settings fixture failed")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			barrier, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer barrier.Rollback()
			if _, err = barrier.ExecContext(ctx, "LOCK TABLE context_headers IN ACCESS EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}
			if _, err = db.ExecContext(ctx, `UPDATE entities SET data=jsonb_set(data,'{runAfter}','"2026-10-01T00:00:00Z"') WHERE kind='ai_job' AND id=$1`, jobID); err != nil {
				t.Fatal(err)
			}
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
					t.Fatal("historical worker did not reach source read barrier")
				}
				time.Sleep(10 * time.Millisecond)
			}
			status, response := projectRequest(t, origin, "GET", "/"+withdrawn, nil, restoredToken)
			if status != 200 {
				t.Fatal("project lookup failed")
			}
			project := response["project"].(map[string]any)
			settings := project["settings"].(map[string]any)
			settings["aiProcessing"] = false
			status, _ = projectRequest(t, origin, "PATCH", "/"+withdrawn+"/settings", map[string]any{"expectedVersion": project["version"], "settings": settings}, restoredToken)
			if status != 200 {
				t.Fatal("readonly source snapshot blocked consent withdrawal")
			}
			if err = barrier.Rollback(); err != nil {
				t.Fatal(err)
			}
			deadline = time.Now().Add(5 * time.Second)
			for {
				status, response = curationRequest(t, origin, "GET", "/ai/jobs?jobId="+jobID, nil)
				if status != 200 {
					t.Fatal("job lookup failed")
				}
				current := response["jobs"].([]any)[0].(map[string]any)
				if current["status"] == "failed" || current["status"] == "completed" {
					if current["status"] != "failed" || calls.Load() != 0 || current["attempts"] != float64(1) {
						t.Fatalf("withdrawn snapshot inputs were dispatched or activated: job %v provider calls %d", current, calls.Load())
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("historical worker did not settle after snapshot release")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
