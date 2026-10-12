package backend_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
