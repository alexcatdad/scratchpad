package backend_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestJobActivityDistinguishesLiveLeasesFromUnclaimableRunningHistory(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0})
			if status != 200 {
				t.Fatal("disabled fixture failed")
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
			live := time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
			expired := time.Now().Add(-time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
			for _, check := range []struct {
				name, typ       string
				lease           any
				present, active bool
			}{
				{"live-disabled", "embed", live, true, true},
				{"expired-disabled", "embed", expired, true, false},
				{"missing-lease", "embed", nil, false, false},
				{"null-lease", "embed", nil, true, false},
				{"invalid-lease", "embed", "not-a-lease", true, false},
				{"invalid-calendar", "embed", "9999-02-30T00:00:00.000Z", true, false},
				{"numeric-lease", "embed", 0, true, false},
				{"live-old-generation", "rag_index", live, true, true},
				{"expired-old-generation", "rag_index", expired, true, false},
			} {
				t.Run(check.name, func(t *testing.T) {
					job := map[string]any{"id": "job_lease_review", "type": check.typ, "status": "running", "version": 2, "attempts": 1, "leaseToken": "synthetic-retained-lease", "fingerprint": "obsolete-generation", "processingVersion": "passage-v1", "createdAt": "2026-10-01T00:00:00Z"}
					if check.present {
						job["leaseUntil"] = check.lease
					}
					raw, _ := json.Marshal(job)
					if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_job','job_lease_review',$1,2,'2026-10-01T00:00:00Z') ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data,version=excluded.version`, string(raw)); err != nil {
						t.Fatal(err)
					}
					status, result := curationRequest(t, origin, "GET", "/ai/jobs?jobId=job_lease_review", nil)
					if status != 200 || result["hasActive"] != check.active {
						t.Fatalf("running activity mismatch for %s: HTTP %d %+v", check.name, status, result)
					}
					var expected map[string]any
					json.Unmarshal(raw, &expected)
					if len(result["jobs"].([]any)) != 1 || !reflect.DeepEqual(result["jobs"].([]any)[0], expected) {
						t.Fatal("activity read rewrote running history or lease provenance")
					}
				})
			}
		})
	}
}

func TestExpiredRunningRAGHistoryResumesOnlyWithCompatibleSettings(t *testing.T) {
	path, databaseURL := ragInstance(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input []string `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "invalid", 400)
			return
		}
		data := []any{}
		for i := range request.Input {
			data = append(data, map[string]any{"index": i, "embedding": []int{1, 0}})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingModel": "historical-running-generation", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("provider fixture failed")
	}
	project := jobProject(t, origin, "Expired running indexing", true, false)
	curationCapture(t, origin, project, "Retain abandoned generation and recover compatible work")
	old := waitRAGJobs(t, origin, 1)[0].(map[string]any)
	id := old["id"].(string)
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	expired := time.Now().Add(-time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	old["status"], old["attempts"], old["leaseToken"], old["leaseUntil"] = "running", 1, "synthetic-abandoned-generation", expired
	writeFixture := func(lease string) {
		t.Helper()
		old["leaseUntil"] = lease
		raw, _ := json.Marshal(old)
		if _, err := db.Exec(`UPDATE entities SET data=$1 WHERE kind='ai_job' AND id=$2`, string(raw), id); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture(expired)
	status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "enabled": true, "embeddingModel": "current-running-generation"})
	if status != 200 {
		t.Fatal("generation change failed")
	}
	var currentID string
	for _, item := range waitRAGJobs(t, origin, 2) {
		job := item.(map[string]any)
		if job["fingerprint"] != old["fingerprint"] {
			currentID = job["id"].(string)
		}
	}
	if currentID == "" {
		t.Fatal("current generation was not queued")
	}
	waitAIJob(t, origin, currentID, "completed")
	check := func(active bool) {
		t.Helper()
		status, result := curationRequest(t, origin, "GET", "/ai/jobs?jobId="+id, nil)
		if status != 200 || result["hasActive"] != active {
			t.Fatalf("running generation activity: HTTP %d %+v", status, result)
		}
		job := result["jobs"].([]any)[0].(map[string]any)
		if job["status"] != "running" || job["fingerprint"] != old["fingerprint"] || job["leaseUntil"] != old["leaseUntil"] || job["attempts"] != float64(1) {
			t.Fatal("incompatible running history was rewritten or claimed")
		}
	}
	check(false)
	writeFixture(time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05.000Z"))
	check(true)
	writeFixture(expired)
	check(false)
	status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 2, "embeddingModel": "historical-running-generation"})
	if status != 200 {
		t.Fatal("compatible restoration failed")
	}
	resumed := waitAIJob(t, origin, id, "completed")
	if resumed["fingerprint"] != old["fingerprint"] || resumed["attempts"] != float64(2) || resumed["leaseToken"] != nil || resumed["leaseUntil"] != nil || len(allAIJobs(t, origin)) != 2 {
		t.Fatal("compatible recovery did not preserve the original job identity and generation")
	}
	status, result := curationRequest(t, origin, "GET", "/ai/jobs", nil)
	if status != 200 || result["hasActive"] != false {
		t.Fatalf("completed generations kept polling active: HTTP %d %+v", status, result)
	}
}
