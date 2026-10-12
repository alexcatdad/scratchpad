package backend_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRAGCaptureAndQueueAuditCommitAtomicallyWithoutProvider(t *testing.T) {
	path, databaseURL := ragInstance(t)
	origin := startServer(t, path, databaseURL)
	projectID := jobProject(t, origin, "Atomic automatic indexing", true, false)
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE FUNCTION rag_queue_fault_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='audit' AND NEW.data->>'action'='ai.job.queued' THEN RAISE EXCEPTION 'synthetic-private-fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER rag_queue_fault BEFORE INSERT ON entities FOR EACH ROW EXECUTE FUNCTION rag_queue_fault_fn()`)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"type": "decision", "title": "Atomic capture", "authority": "explicit", "payload": map[string]any{"decision": "Preserve the source and its durable indexing job together."}}
	status, _ := captureRequest(t, origin, projectID, "atomic-rag-retry", body)
	if status != 503 {
		t.Fatalf("capture must fail when its queue audit cannot commit: %d", status)
	}
	status, response := curationRequest(t, origin, "GET", "/records", nil)
	if status != 200 || len(response["records"].([]any)) != 0 {
		t.Fatal("failed enqueue left a partially saved capture")
	}
	status, response = curationRequest(t, origin, "GET", "/ai/jobs", nil)
	if status != 200 || len(response["jobs"].([]any)) != 0 {
		t.Fatal("failed capture left a partial indexing job")
	}
	if _, err = db.Exec(`DROP TRIGGER rag_queue_fault ON entities; DROP FUNCTION rag_queue_fault_fn()`); err != nil {
		t.Fatal(err)
	}
	status, response = captureRequest(t, origin, projectID, "atomic-rag-retry", body)
	if status != 201 {
		t.Fatal("failed capture reserved its retry identity")
	}
	record := response["record"].(map[string]any)
	status, _ = captureRequest(t, origin, projectID, "atomic-rag-retry", body)
	if status != 201 {
		t.Fatal("capture retry failed")
	}
	status, response = curationRequest(t, origin, "GET", "/ai/jobs", nil)
	if status != 200 || len(response["jobs"].([]any)) != 1 {
		t.Fatal("capture retry duplicated or lost its indexing job")
	}
	job := response["jobs"].([]any)[0].(map[string]any)
	if job["type"] != "rag_index" || job["recordId"] != record["id"] || job["status"] != "queued" || job["attempts"] != float64(0) {
		t.Fatalf("capture did not queue source generation without a provider: %v", job)
	}
}

func TestRAGAutomaticCaptureQueuesWhileProviderSecretCannotBeDecrypted(t *testing.T) {
	path, databaseURL := ragInstance(t)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected provider call", 503)
	}))
	defer provider.Close()
	var projectID string
	t.Run("configure", func(t *testing.T) {
		t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", syntheticEncryptionKey(t, 23))
		origin := startServer(t, path, databaseURL)
		status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "apiKey": "synthetic-provider-token", "embeddingDimensions": 2})
		if status != 200 {
			t.Fatal("encrypted provider configuration failed")
		}
		projectID = jobProject(t, origin, "Keyless source capture", true, false)
	})
	t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", "")
	origin := startServer(t, path, databaseURL)
	record := curationCapture(t, origin, projectID, "Source availability is independent of the provider key")
	jobs := waitRAGJobs(t, origin, 1)
	time.Sleep(1200 * time.Millisecond)
	jobs = waitRAGJobs(t, origin, 1)
	job := jobs[0].(map[string]any)
	if job["recordId"] != record["id"] || job["status"] != "queued" || job["attempts"] != float64(0) || calls.Load() != 0 {
		t.Fatal("missing provider key blocked capture or permitted provider dispatch")
	}
}

func waitRAGJobs(t *testing.T, origin string, count int) []any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, response := curationRequest(t, origin, "GET", "/ai/jobs", nil)
		if status != 200 {
			t.Fatal("jobs unavailable")
		}
		jobs := response["jobs"].([]any)
		if len(jobs) == count {
			return jobs
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d durable jobs, got %d", count, len(jobs))
		}
		time.Sleep(40 * time.Millisecond)
	}
}
func TestRAGBackfillsEligibleHistoryAndNewEmbeddingGenerationsWhileAIDisabled(t *testing.T) {
	path, databaseURL := ragInstance(t)
	var recordID string
	t.Run("existing-history", func(t *testing.T) {
		t.Setenv("SCRATCHPAD_RAG_ENABLED", "false")
		origin := startServer(t, path, databaseURL)
		status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0, "embeddingDimensions": 2, "embeddingModel": "backfill-old"})
		if status != 200 {
			t.Fatal("provider configuration failed")
		}
		projectID := jobProject(t, origin, "Eligible backfill", true, false)
		recordID = curationCapture(t, origin, projectID, "Historical source")["id"].(string)
		privateProject := jobProject(t, origin, "Excluded history", false, false)
		curationCapture(t, origin, privateProject, "Consent is required")
		status, _ = captureRequest(t, origin, projectID, "", map[string]any{"type": "decision", "title": "Generated source", "authority": "derived", "payload": map[string]any{"decision": "Generated interpretations are not first-release RAG evidence."}})
		if status != 201 {
			t.Fatal("derived fixture failed")
		}
		waitRAGJobs(t, origin, 0)
	})
	origin := startServer(t, path, databaseURL)
	jobs := waitRAGJobs(t, origin, 1)
	old := jobs[0].(map[string]any)
	if old["recordId"] != recordID || old["status"] != "queued" || old["attempts"] != float64(0) {
		t.Fatal("backfill selected forbidden history or required a provider")
	}
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "embeddingModel": "backfill-new"})
	if status != 200 {
		t.Fatal("embedding change failed")
	}
	jobs = waitRAGJobs(t, origin, 2)
	if jobs[0].(map[string]any)["fingerprint"] == jobs[1].(map[string]any)["fingerprint"] {
		t.Fatal("new embedding compatibility did not queue a distinct generation")
	}
	status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 2, "model": "new-completion-choice"})
	if status != 200 {
		t.Fatal("completion change failed")
	}
	time.Sleep(1200 * time.Millisecond)
	waitRAGJobs(t, origin, 2)
}

func TestRAGBackfillCoversMultipleBatchesAndDoesNotDuplicateAfterRestart(t *testing.T) {
	path, databaseURL := ragInstance(t)
	sources := map[string]bool{}
	t.Run("history", func(t *testing.T) {
		t.Setenv("SCRATCHPAD_RAG_ENABLED", "false")
		origin := startServer(t, path, databaseURL)
		projectID := jobProject(t, origin, "Historical corpus", true, false)
		for i := 0; i < 33; i++ {
			record := curationCapture(t, origin, projectID, "Historical evidence")
			sources[record["id"].(string)] = true
		}
	})
	for _, phase := range []string{"backfill", "restart"} {
		t.Run(phase, func(t *testing.T) {
			origin := startServer(t, path, databaseURL)
			jobs := waitRAGJobs(t, origin, 33)
			seen := map[string]bool{}
			for _, value := range jobs {
				job := value.(map[string]any)
				id := job["recordId"].(string)
				if !sources[id] || seen[id] || job["status"] != "queued" || job["attempts"] != float64(0) {
					t.Fatal("backfill omitted, duplicated or dispatched unavailable source work")
				}
				seen[id] = true
			}
		})
	}
}

func TestRAGHistoryEnqueueContinuesWhileEmbeddingProviderIsBlocked(t *testing.T) {
	path, databaseURL := ragInstance(t)
	started, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if first.CompareAndSwap(false, true) {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer func() { close(release); provider.Close() }()
	t.Run("history", func(t *testing.T) {
		t.Setenv("SCRATCHPAD_RAG_ENABLED", "false")
		origin := startServer(t, path, databaseURL)
		status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
		if status != 200 {
			t.Fatal("provider fixture failed")
		}
		projectID := jobProject(t, origin, "Provider-independent history queue", true, false)
		for i := 0; i < 33; i++ {
			curationCapture(t, origin, projectID, "Historical source")
		}
	})
	origin := startServer(t, path, databaseURL)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	waitRAGJobs(t, origin, 33)
}
