package backend_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestRAGCoverageRequiresSourceFaithfulPassagesAndRebuildRestoresCoverage(t *testing.T) {
	path, databaseURL := ragInstance(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("configuration failed")
	}
	projectID := jobProject(t, origin, "Projection integrity", true, false)
	status, captured := captureRequest(t, origin, projectID, "", map[string]any{"type": "finding", "title": "Original integrity evidence", "authority": "observed", "payload": map[string]any{"finding": strings.Repeat("a", 5990) + "🙂 ORIGINAL_INTEGRITY_TOKEN belongs to immutable evidence."}})
	if status != 201 {
		t.Fatal("source capture failed")
	}
	record := captured["record"].(map[string]any)
	jobs := waitRAGJobs(t, origin, 1)
	job := waitAIJob(t, origin, jobs[0].(map[string]any)["id"].(string), "completed")
	route := "/rag/coverage?projectId=" + projectID
	status, coverage := curationRequest(t, origin, "GET", route, nil)
	if status != 200 || coverage["status"] != "complete" || coverage["passageCount"] != float64(2) {
		t.Fatalf("original two-passage projection not complete: %d %v", status, coverage)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Corrupt only a rebuildable derivative; source and completed count stay intact.
	if _, err = db.Exec(`UPDATE rag_passages SET text='CORRUPTED_SYNTHETIC_TEXT' WHERE record_id=$1 AND source_start>0`, record["id"]); err != nil {
		t.Fatal(err)
	}
	status, coverage = curationRequest(t, origin, "GET", route, nil)
	if status != 200 || coverage["status"] != "incomplete" || coverage["indexedSources"] != float64(0) || coverage["unindexedSources"] != float64(1) {
		t.Fatalf("corrupted passage text was reported as complete evidence: %d %v", status, coverage)
	}
	status, response := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "ORIGINAL_INTEGRITY_TOKEN"})
	if status != 200 || response["semantic"].(map[string]any)["indexCoverage"] != "incomplete" {
		t.Fatal("retrieval did not report corrupted indexing uncertainty")
	}
	for _, value := range response["results"].([]any) {
		if strings.Contains(value.(map[string]any)["text"].(string), "CORRUPTED_SYNTHETIC_TEXT") {
			t.Fatal("corrupted derivative became cited evidence")
		}
	}
	status, response = curationRequest(t, origin, "POST", "/rag/index", map[string]any{"projectId": projectID, "recordId": record["id"], "rebuild": true, "expectedVersion": job["version"]})
	if status != 202 {
		t.Fatal("explicit rebuild failed")
	}
	waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "completed")
	status, coverage = curationRequest(t, origin, "GET", route, nil)
	if status != 200 || coverage["status"] != "complete" || coverage["passageCount"] != float64(2) {
		t.Fatalf("explicit rebuild did not restore complete coverage: %d %v", status, coverage)
	}
	if _, err = db.Exec(`INSERT INTO rag_passages SELECT 'passage_corrupted_synthetic_id',fingerprint,record_id,project_id,content_hash,processing_version,source_start,source_end,text,dimensions,embedding FROM rag_passages WHERE record_id=$1 AND source_start=0`, record["id"]); err != nil {
		t.Fatal(err)
	}
	status, coverage = curationRequest(t, origin, "GET", route, nil)
	if status != 200 || coverage["status"] != "incomplete" {
		t.Fatalf("extra noncanonical passage was reported as complete coverage: %d %v", status, coverage)
	}
	status, response = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "integrity evidence"})
	if status != 200 {
		t.Fatal("retrieval failed after a corrupt projection")
	}
	for _, value := range response["results"].([]any) {
		if value.(map[string]any)["id"] == "passage_corrupted_synthetic_id" {
			t.Fatal("noncanonical duplicate passage became cited evidence")
		}
	}
	if _, err = db.Exec(`DELETE FROM rag_passages WHERE id='passage_corrupted_synthetic_id'`); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"embedding='[0,0]'::vector", "embedding='[1,0]'::vector,source_start=source_start-1"} {
		if _, err = db.Exec(`UPDATE rag_passages SET `+fault+` WHERE record_id=$1 AND source_start>0`, record["id"]); err != nil {
			t.Fatal(err)
		}
		status, coverage = curationRequest(t, origin, "GET", route, nil)
		if status != 200 || coverage["status"] != "incomplete" || coverage["indexedSources"] != float64(0) {
			t.Fatalf("unusable vector or overlapping range counted as coverage: %d %v", status, coverage)
		}
	}
	status, detail := curationRequest(t, origin, "GET", "/records/"+record["id"].(string), nil)
	if status != 200 || !reflect.DeepEqual(detail["record"], record) {
		t.Fatal("projection corruption or rebuild rewrote immutable source evidence")
	}
}

func TestRAGCoverageDistinguishesQueuedCompleteAndIncompatibleEvidence(t *testing.T) {
	path, databaseURL := ragInstance(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("configuration failed")
	}
	projectID := jobProject(t, origin, "Coverage evidence", true, false)
	curationCapture(t, origin, projectID, "Index coverage is not absent evidence")
	jobs := waitRAGJobs(t, origin, 1)
	route := "/rag/coverage?projectId=" + projectID
	unauthenticated, err := http.Get(origin + "/api/v1" + route)
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated.Body.Close()
	if unauthenticated.StatusCode != 401 {
		t.Fatal("coverage exposed project information without authentication")
	}
	privateProject := jobProject(t, origin, "Excluded coverage", false, false)
	status, _ = curationRequest(t, origin, "GET", "/rag/coverage?projectId="+privateProject, nil)
	if status != 403 {
		t.Fatal("coverage ignored project participation consent")
	}
	status, response := curationRequest(t, origin, "GET", route, nil)
	if status != 200 || response["status"] != "incomplete" || response["eligibleSources"] != float64(1) || response["queuedSources"] != float64(1) || response["indexedSources"] != float64(0) {
		t.Fatalf("queued coverage not distinguished: %d %v", status, response)
	}
	status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "enabled": true})
	if status != 200 {
		t.Fatal("enablement failed")
	}
	waitAIJob(t, origin, jobs[0].(map[string]any)["id"].(string), "completed")
	status, response = curationRequest(t, origin, "GET", route, nil)
	if status != 200 || response["status"] != "complete" || response["indexedSources"] != float64(1) || response["passageCount"] != float64(1) {
		t.Fatalf("completed coverage not proved: %d %v", status, response)
	}
	status, response = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "coverage evidence"})
	if status != 200 || response["semantic"].(map[string]any)["indexCoverage"] != "complete" {
		t.Fatalf("retrieved evidence did not carry assessed coverage: %d %v", status, response)
	}
	status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 2, "enabled": false, "embeddingModel": "different-embedding-generation"})
	if status != 200 {
		t.Fatal("generation change failed")
	}
	waitRAGJobs(t, origin, 2)
	status, response = curationRequest(t, origin, "GET", route, nil)
	if status != 200 || response["status"] != "incomplete" || response["indexedSources"] != float64(0) || response["queuedSources"] != float64(1) || response["incompatibleSources"] != float64(1) {
		t.Fatalf("incompatible evidence was presented as coverage: %d %v", status, response)
	}
}

func TestRAGCurrentGenerationDoesNotWaitBehindStaleQueuedHistory(t *testing.T) {
	path, databaseURL := ragInstance(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("configuration failed")
	}
	projectID := jobProject(t, origin, "Generation priority", true, false)
	for i := 0; i < 33; i++ {
		curationCapture(t, origin, projectID, "Historical generation")
	}
	waitRAGJobs(t, origin, 33)
	status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "enabled": true, "embeddingModel": "current-generation-model"})
	if status != 200 {
		t.Fatal("generation update failed")
	}
	jobs := waitRAGJobs(t, origin, 66)
	status, coverage := curationRequest(t, origin, "GET", "/rag/coverage?projectId="+projectID, nil)
	if status != 200 {
		t.Fatal("current coverage unavailable")
	}
	var target, created string
	for _, value := range jobs {
		job := value.(map[string]any)
		if job["fingerprint"] == coverage["fingerprint"] {
			id, at := job["id"].(string), job["createdAt"].(string)
			if target == "" || at < created || (at == created && id < target) {
				target, created = id, at
			}
		}
	}
	if target == "" {
		t.Fatal("no current generation was queued")
	}
	waitAIJob(t, origin, target, "completed")
}
