package backend_test

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"
)

func TestAIScheduleShorteningUsesLastCycleAcrossWorkers(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			jobProject(t, origin, "Changed schedule", true, false)
			status, result := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 43200, "analysisModes": []any{}})
			if status != 200 {
				t.Fatalf("configuration failed: %d %v", status, result)
			}
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			// Historical last cycle was two minutes ago; its old 30-day cadence
			// must not postpone a newly selected one-minute cadence.
			row := map[string]any{"id": "global", "version": 1, "createdAt": "2026-10-01T00:00:00Z", "nextAt": time.Now().Add(30*24*time.Hour - 2*time.Minute).UnixMilli()}
			raw, _ := json.Marshal(row)
			_, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_schedule','global',$1,1,'2026-10-01T00:00:00Z')`, string(raw))
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			startServer(t, path, databaseURL)
			status, result = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "enabled": true, "scheduleMinutes": 1})
			if status != 200 {
				t.Fatalf("shortened cadence rejected: %d %v", status, result)
			}
			jobs := scheduledJobs(t, origin, 1)
			id := jobs[0].(map[string]any)["id"].(string)
			waitAIJob(t, origin, id, "completed")
			startServer(t, path, databaseURL)
			time.Sleep(1200 * time.Millisecond)
			if scheduledJobs(t, origin, 1)[0].(map[string]any)["id"] != id {
				t.Fatal("cadence update duplicated work across workers/restart")
			}
		})
	}
}

func TestAIScheduleLengtheningAndPausedCadence(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		for _, paused := range []bool{false, true} {
			name := engine + "/lengthen"
			if paused {
				name = engine + "/pause-shorten"
			}
			t.Run(name, func(t *testing.T) {
				path, databaseURL := portableInstance(t, engine)
				origin := startServer(t, path, databaseURL)
				jobProject(t, origin, "Changed cadence", true, false)
				oldMinutes := 1
				if paused {
					oldMinutes = 43200
				}
				status, result := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": oldMinutes, "analysisModes": []any{}})
				if status != 200 {
					t.Fatalf("configuration failed: %d %v", status, result)
				}
				driver, dsn := "sqlite", path
				if databaseURL != "" {
					driver, dsn = "pgx", databaseURL
				}
				db, err := sql.Open(driver, dsn)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(map[string]any{"id": "global", "version": 1, "createdAt": "2026-10-01T00:00:00Z", "nextAt": time.Now().Add(time.Duration(oldMinutes-2) * time.Minute).UnixMilli()})
				_, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_schedule','global',$1,1,'2026-10-01T00:00:00Z')`, string(raw))
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
				minutes := 43200
				if paused {
					minutes = 0
				}
				status, result = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "enabled": true, "scheduleMinutes": minutes})
				if status != 200 {
					t.Fatalf("cadence change failed: %d %v", status, result)
				}
				startServer(t, path, databaseURL)
				time.Sleep(1200 * time.Millisecond)
				scheduledJobs(t, origin, 0)
				status, result = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 2, "scheduleMinutes": 1})
				if status != 200 {
					t.Fatalf("shortened cadence failed: %d %v", status, result)
				}
				id := scheduledJobs(t, origin, 1)[0].(map[string]any)["id"].(string)
				waitAIJob(t, origin, id, "completed")
			})
		}
	}
}
