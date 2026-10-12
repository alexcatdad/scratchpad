package backend_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestAIScheduleDeadlineSurvivesOperationalSQLiteRestore(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	jobProject(t, origin, "Restored scheduling", true, false)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 1, "analysisModes": []any{}})
	if status != 200 {
		t.Fatal("configuration failed")
	}
	job := scheduledJobs(t, origin, 1)[0].(map[string]any)
	id := job["id"].(string)
	waitAIJob(t, origin, id, "completed")
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	snapshot := filepath.Join(dir, "schedule.sqlite")
	if out, err := backupCommand(path, "", snapshot).CombinedOutput(); err != nil {
		t.Fatalf("schedule backup failed: %v %s", err, out)
	}
	restored := startServer(t, restoreSnapshotFixture(t, snapshot), "")
	time.Sleep(1200 * time.Millisecond)
	jobs := scheduledJobs(t, restored, 1)
	if jobs[0].(map[string]any)["id"] != id {
		t.Fatal("operational restore reset the deadline or replaced durable work")
	}
	status, response := curationRequest(t, restored, "POST", "/export", map[string]any{})
	if status != 200 {
		t.Fatal("knowledge export failed")
	}
	if _, exists := response["data"].(map[string]any)["ai_schedule"]; exists {
		t.Fatal("operational scheduling entered knowledge export")
	}
}

func TestAIScheduleZeroKeepsManualProcessingAvailable(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			provider := completionService(t, func(input map[string]any) any {
				record := input["records"].([]any)[0].(map[string]any)
				return map[string]any{"artifacts": []any{map[string]any{"kind": "summary", "title": "Manual interpretation", "content": map[string]any{"text": "Manual work remains available"}, "sourceRecordIds": []any{record["id"]}}}}
			})
			defer provider.Close()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			projectID := jobProject(t, origin, "Manual processing", true, true)
			curationCapture(t, origin, projectID, "Manual source history")
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []any{"summary"}})
			if status != 200 {
				t.Fatal("configuration failed")
			}
			time.Sleep(1200 * time.Millisecond)
			status, response := curationRequest(t, origin, "GET", "/ai/jobs", nil)
			if status != 200 || len(response["jobs"].([]any)) != 0 {
				t.Fatal("manual-only configuration scheduled background work")
			}
			status, response = curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "analyze", "projectId": projectID})
			if status != 202 {
				t.Fatal("manual enqueue failed")
			}
			waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "completed")
		})
	}
}

func TestAIScheduleWithNoAnalysisModesIndexesRealSourcesOnly(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Input []string `json:"input"`
				}
				if r.URL.Path != "/v1/embeddings" || json.NewDecoder(r.Body).Decode(&body) != nil {
					http.Error(w, "unexpected request", 400)
					return
				}
				data := []any{}
				for index := range body.Input {
					data = append(data, map[string]any{"index": index, "embedding": []float64{1, 0}})
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"data": data})
			}))
			defer provider.Close()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			first := jobProject(t, origin, "Embedded first", true, true)
			second := jobProject(t, origin, "Embedded second", true, true)
			curationCapture(t, origin, first, "First indexed source")
			curationCapture(t, origin, second, "Second indexed source")
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 1, "analysisModes": []any{}, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
			if status != 200 {
				t.Fatal("configuration failed")
			}
			jobs := scheduledJobs(t, origin, 2)
			for _, item := range jobs {
				job := item.(map[string]any)
				if job["type"] != "embed" {
					t.Fatal("empty analysis modes scheduled completion work")
				}
				completed := waitAIJob(t, origin, job["id"].(string), "completed")
				if completed["sourceCount"] != float64(1) {
					t.Fatal("scheduled embedding did not process its source")
				}
			}
			for _, id := range []string{first, second} {
				status, response := curationRequest(t, origin, "POST", "/search/semantic", map[string]any{"projectId": id, "query": "preserved evidence"})
				if status != 200 || len(response["results"].([]any)) != 1 || response["results"].([]any)[0].(map[string]any)["record"].(map[string]any)["projectId"] != id {
					t.Fatal("scheduled embedding missing from scoped semantic retrieval")
				}
			}
		})
	}
}

func TestAIScheduleRespectsActiveCrossProjectWork(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			first := jobProject(t, origin, "Active first", true, true)
			second := jobProject(t, origin, "Active second", true, true)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			job := map[string]any{"id": "job_active", "version": 1, "createdAt": "2026-10-01T00:00:00Z", "type": "analyze", "status": "queued", "attempts": 0, "runAfter": "2099-01-01T00:00:00Z", "scope": map[string]any{"projectIds": []any{first, second}, "crossProject": true}}
			raw, _ := json.Marshal(job)
			_, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_job','job_active',$1,1,'2026-10-01T00:00:00Z')`, string(raw))
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 1})
			if status != 200 {
				t.Fatal("configuration failed")
			}
			jobs := scheduledJobs(t, origin, 3)
			embeds := map[string]bool{}
			for _, item := range jobs {
				job := item.(map[string]any)
				if job["id"] == "job_active" {
					continue
				}
				if job["type"] != "embed" {
					t.Fatal("active cross analysis did not suppress overlapping analysis")
				}
				scope := job["scope"].(map[string]any)
				ids := scope["projectIds"].([]any)
				if len(ids) != 1 || scope["crossProject"] != false {
					t.Fatal("scheduled embeddings escaped local scope")
				}
				embeds[ids[0].(string)] = true
			}
			if !embeds[first] || !embeds[second] || len(embeds) != 2 {
				t.Fatal("independent embedding work was not scheduled")
			}
		})
	}
}

func TestAIScheduleAuditFailureRollsBackCycleAndDeadline(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			jobProject(t, origin, "Cycle first", true, true)
			jobProject(t, origin, "Cycle second", true, true)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			schedule := `{"id":"global","version":1,"createdAt":"2026-10-01T00:00:00Z","nextAt":0}`
			if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_schedule','global',$1,1,'2026-10-01T00:00:00Z')`, schedule); err != nil {
				t.Fatal(err)
			}
			fault := `CREATE TRIGGER schedule_fault BEFORE INSERT ON entities WHEN NEW.kind='audit' AND json_extract(NEW.data,'$.action')='ai.job.queued' AND (SELECT COUNT(*) FROM entities WHERE kind='ai_job')>2 BEGIN SELECT RAISE(ABORT,'synthetic-private-fault'); END`
			if engine == "postgres" {
				fault = `CREATE FUNCTION schedule_fault_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='audit' AND NEW.data->>'action'='ai.job.queued' AND (SELECT COUNT(*) FROM entities WHERE kind='ai_job')>2 THEN RAISE EXCEPTION 'synthetic-private-fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER schedule_fault BEFORE INSERT ON entities FOR EACH ROW EXECUTE FUNCTION schedule_fault_fn()`
			}
			if _, err = db.Exec(fault); err != nil {
				t.Fatal(err)
			}
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 1})
			if status != 200 {
				t.Fatal("configuration failed")
			}
			time.Sleep(1200 * time.Millisecond)
			status, response := curationRequest(t, origin, "GET", "/ai/jobs", nil)
			if status != 200 || len(response["jobs"].([]any)) != 0 {
				t.Fatal("failed scheduled cycle left partial work")
			}
			drop := `DROP TRIGGER schedule_fault`
			if engine == "postgres" {
				drop = `DROP TRIGGER schedule_fault ON entities`
			}
			if _, err = db.Exec(drop); err != nil {
				t.Fatal(err)
			}
			// Immediate recovery proves the failed cycle did not postpone its persisted deadline.
			scheduledJobs(t, origin, 5)
		})
	}
}

func scheduledJobs(t *testing.T, origin string, count int) []any {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		status, response := curationRequest(t, origin, "GET", "/ai/jobs", nil)
		if status != 200 {
			t.Fatal("job listing failed")
		}
		jobs := response["jobs"].([]any)
		if len(jobs) == count {
			return jobs
		}
		if len(jobs) > count || time.Now().After(deadline) {
			t.Fatalf("expected %d scheduled jobs, got %d", count, len(jobs))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAISchedulePersistsAndDeduplicatesAcrossServersAndRestart(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			var identities []string
			for _, phase := range []string{"schedule", "restart"} {
				t.Run(phase, func(t *testing.T) {
					origin := startServer(t, path, databaseURL)
					if phase == "schedule" {
						first := jobProject(t, origin, "Scheduled first", true, true)
						second := jobProject(t, origin, "Scheduled second", true, true)
						local := jobProject(t, origin, "Scheduled local only", true, false)
						jobProject(t, origin, "Denied schedule", false, true)
						startServer(t, path, databaseURL)
						status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 1})
						if status != 200 {
							t.Fatal("scheduled configuration failed")
						}
						jobs := scheduledJobs(t, origin, 7)
						localTypes := map[string]map[string]bool{}
						cross := 0
						for _, item := range jobs {
							job := item.(map[string]any)
							identities = append(identities, job["id"].(string))
							scope := job["scope"].(map[string]any)
							ids := scope["projectIds"].([]any)
							if scope["crossProject"] == true {
								cross++
								if job["type"] != "analyze" || !reflect.DeepEqual(ids, []any{first, second}) {
									t.Fatal("cross schedule escaped participating projects")
								}
							} else {
								if len(ids) != 1 {
									t.Fatal("local schedule escaped its project")
								}
								id := ids[0].(string)
								if id != first && id != second && id != local {
									t.Fatal("scheduler included denied project")
								}
								if localTypes[id] == nil {
									localTypes[id] = map[string]bool{}
								}
								localTypes[id][job["type"].(string)] = true
							}
						}
						if cross != 1 || len(localTypes) != 3 {
							t.Fatal("eligible scheduled scope incomplete")
						}
						for _, types := range localTypes {
							if !types["embed"] || !types["analyze"] || len(types) != 2 {
								t.Fatal("scheduled job types incomplete")
							}
						}
						for _, id := range identities {
							waitAIJob(t, origin, id, "completed")
						}
						sort.Strings(identities)
					} else {
						time.Sleep(1200 * time.Millisecond)
						jobs := scheduledJobs(t, origin, 7)
						actual := []string{}
						for _, item := range jobs {
							actual = append(actual, item.(map[string]any)["id"].(string))
						}
						sort.Strings(actual)
						if !reflect.DeepEqual(actual, identities) {
							t.Fatal("restart reset persisted schedule or duplicated work")
						}
					}
				})
			}
		})
	}
}
