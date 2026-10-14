package backend_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func jobProject(t *testing.T, origin, name string, ai, cross bool) string {
	t.Helper()
	status, response := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": name}, restoredToken)
	if status != 200 {
		t.Fatal("project fixture failed")
	}
	project := response["project"].(map[string]any)
	settings := project["settings"].(map[string]any)
	settings["aiProcessing"], settings["crossProjectAnalysis"] = ai, cross
	status, _ = projectRequest(t, origin, "PATCH", "/"+project["id"].(string)+"/settings", map[string]any{"expectedVersion": 1, "settings": settings}, restoredToken)
	if status != 200 {
		t.Fatal("consent fixture failed")
	}
	return project["id"].(string)
}

func TestAIJobSurvivesOperationalSQLiteRestore(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0})
	if status != 200 {
		t.Fatal("AI fixture failed")
	}
	projectID := jobProject(t, origin, "Restored queue", true, false)
	status, result := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "export", "projectId": projectID, "format": "decisions"})
	if status != 202 {
		t.Fatal("enqueue failed")
	}
	job := result["job"]
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	snapshot := filepath.Join(dir, "queue.sqlite")
	if out, err := backupCommand(path, "", snapshot).CombinedOutput(); err != nil {
		t.Fatalf("queue snapshot failed: %v %s", err, out)
	}
	restored := startServer(t, restoreSnapshotFixture(t, snapshot), "")
	status, result = curationRequest(t, restored, "GET", "/ai/jobs", nil)
	if status != 200 || len(result["jobs"].([]any)) != 1 || !reflect.DeepEqual(result["jobs"].([]any)[0], job) {
		t.Fatal("operational restore lost queued execution state")
	}
}

func TestAIJobQueuePersistsCanonicalScopeWithoutProviderCalls(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			var projectID, jobID string
			for _, phase := range []string{"enqueue", "restart"} {
				t.Run(phase, func(t *testing.T) {
					origin := startServer(t, path, databaseURL)
					if phase == "enqueue" {
						status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": "http://127.0.0.1:1/v1"})
						if status != 200 {
							t.Fatal("AI fixture failed")
						}
						projectID = jobProject(t, origin, "Durable jobs", true, true)
						status, result := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "embed", "projectId": projectID})
						if status != 202 {
							t.Fatalf("job enqueue failed: %d", status)
						}
						job := result["job"].(map[string]any)
						jobID = job["id"].(string)
						if job["status"] != "queued" || job["attempts"] != float64(0) || job["version"] != float64(1) || job["format"] != "handoff" || !reflect.DeepEqual(job["scope"], map[string]any{"projectIds": []any{projectID}, "crossProject": false}) {
							t.Fatal("queued job did not retain canonical scope")
						}
					}
					status, result := curationRequest(t, origin, "GET", "/ai/jobs", nil)
					if status != 200 || len(result["jobs"].([]any)) != 1 || result["jobs"].([]any)[0].(map[string]any)["id"] != jobID {
						t.Fatal("durable job missing after restart")
					}
					status, result = curationRequest(t, origin, "POST", "/export", map[string]any{})
					if status != 200 {
						t.Fatal("export failed")
					}
					if _, exists := result["data"].(map[string]any)["ai_job"]; exists {
						t.Fatal("operational jobs entered knowledge export")
					}
				})
			}
		})
	}
}

func TestAIJobRetryPreservesIdentityAndChecksVersionAndConsent(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0})
			if status != 200 {
				t.Fatal("AI fixture failed")
			}
			projectID := jobProject(t, origin, "Retry scope", true, false)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			row := map[string]any{"id": "job_historical", "version": 3, "createdAt": "2026-10-01T00:00:00Z", "type": "analyze", "format": "handoff", "status": "failed", "attempts": 3, "lastError": "Synthetic failure", "completedAt": "2026-10-01T01:00:00Z", "leaseToken": "synthetic-old-lease", "leaseUntil": "2026-10-01T00:30:00Z", "scope": map[string]any{"projectIds": []any{projectID}, "crossProject": false}}
			raw, _ := json.Marshal(row)
			if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_job','job_historical',$1,3,'2026-10-01T00:00:00Z')`, string(raw)); err != nil {
				t.Fatal(err)
			}
			status, project := projectRequest(t, origin, "GET", "/"+projectID, nil, restoredToken)
			if status != 200 {
				t.Fatal("project missing")
			}
			settings := project["project"].(map[string]any)["settings"].(map[string]any)
			settings["aiProcessing"] = false
			status, _ = projectRequest(t, origin, "PATCH", "/"+projectID+"/settings", map[string]any{"expectedVersion": 2, "settings": settings}, restoredToken)
			if status != 200 {
				t.Fatal("consent revocation failed")
			}
			status, _ = curationRequest(t, origin, "POST", "/ai/jobs/job_historical/retry", map[string]any{"expectedVersion": 3})
			if status != 403 {
				t.Fatal("retry ignored revoked consent")
			}
			settings["aiProcessing"] = true
			status, _ = projectRequest(t, origin, "PATCH", "/"+projectID+"/settings", map[string]any{"expectedVersion": 3, "settings": settings}, restoredToken)
			if status != 200 {
				t.Fatal("consent restoration failed")
			}
			status, result := curationRequest(t, origin, "POST", "/ai/jobs/job_historical/retry", map[string]any{"expectedVersion": 2})
			if status != 409 {
				t.Fatal("stale retry accepted")
			}
			status, result = curationRequest(t, origin, "POST", "/ai/jobs/job_historical/retry", map[string]any{"expectedVersion": 3})
			if status != 202 {
				t.Fatal("failed job retry rejected")
			}
			job := result["job"].(map[string]any)
			if job["id"] != "job_historical" || job["version"] != float64(4) || job["status"] != "queued" || job["attempts"] != float64(0) || job["leaseToken"] != nil || job["leaseUntil"] != nil || job["lastError"] != nil || job["completedAt"] != nil {
				t.Fatal("retry did not reset execution state without replacing identity")
			}
			status, _ = curationRequest(t, origin, "POST", "/ai/jobs/job_historical/retry", map[string]any{"expectedVersion": 4})
			if status != 409 {
				t.Fatal("non-failed job retried")
			}
		})
	}
}

func TestAIJobEnqueueAuditFailureLeavesNoJob(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0})
			if status != 200 {
				t.Fatal("AI fixture failed")
			}
			projectID := jobProject(t, origin, "Atomic queue", true, true)
			status, before := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("export fixture failed")
			}
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			fault := `CREATE TRIGGER queue_fault BEFORE INSERT ON entities WHEN NEW.kind='audit' AND json_extract(NEW.data,'$.action')='ai.job.queued' BEGIN SELECT RAISE(ABORT,'synthetic-private-fault'); END`
			if engine == "postgres" {
				fault = `CREATE FUNCTION queue_fault_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='audit' AND NEW.data->>'action'='ai.job.queued' THEN RAISE EXCEPTION 'synthetic-private-fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER queue_fault BEFORE INSERT ON entities FOR EACH ROW EXECUTE FUNCTION queue_fault_fn()`
			}
			if _, err = db.Exec(fault); err != nil {
				t.Fatal(err)
			}
			status, _ = curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "embed", "projectId": projectID})
			if status != 503 {
				t.Fatal("late queue failure did not fail safely")
			}
			status, result := curationRequest(t, origin, "GET", "/ai/jobs", nil)
			if status != 200 || len(result["jobs"].([]any)) != 0 {
				t.Fatal("failed enqueue left a durable job")
			}
			status, after := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 || !reflect.DeepEqual(before["data"], after["data"]) {
				t.Fatal("failed enqueue left an audit")
			}
		})
	}
}

func TestAIJobScopeRequiresExplicitParticipationAndCurrentConsent(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0})
			if status != 200 {
				t.Fatal("AI fixture failed")
			}
			allowed := jobProject(t, origin, "Participates", true, true)
			private := jobProject(t, origin, "Private participant", true, false)
			denied := jobProject(t, origin, "No AI", false, true)
			for _, test := range []struct {
				body   map[string]any
				status int
			}{
				{map[string]any{"type": "analyze"}, 400},
				{map[string]any{"type": "embed", "projectId": "proj_missing"}, 404},
				{map[string]any{"type": "embed", "projectId": denied}, 403},
				{map[string]any{"type": "export", "projectIds": []any{allowed, private}, "crossProject": true}, 403},
				{map[string]any{"type": "embed", "projectIds": []any{allowed, denied}, "crossProject": true}, 403},
			} {
				status, _ := curationRequest(t, origin, "POST", "/ai/jobs", test.body)
				if status != test.status {
					t.Fatalf("scope check failed: %d", status)
				}
			}
			status, result := curationRequest(t, origin, "GET", "/ai/jobs", nil)
			if status != 200 || len(result["jobs"].([]any)) != 0 {
				t.Fatal("denied scope created jobs")
			}
			status, result = curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "export", "crossProject": true, "format": "architecture"})
			if status != 202 || !reflect.DeepEqual(result["job"].(map[string]any)["scope"], map[string]any{"projectIds": []any{allowed}, "crossProject": true}) {
				t.Fatal("implicit cross-project scope included private/denied projects")
			}
			status, result = curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "embed", "projectId": private})
			if status != 202 {
				t.Fatal("single-project private processing refused")
			}
		})
	}
}
