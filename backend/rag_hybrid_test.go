package backend_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRAGProviderOutageReturnsCitedLexicalEvidenceAndIndexUncertainty(t *testing.T) {
	path, databaseURL := ragInstance(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "synthetic-private-provider-error", 503) }))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("provider fixture failed")
	}
	projectID := jobProject(t, origin, "Lexical outage evidence", true, false)
	status, response := captureRequest(t, origin, projectID, "", map[string]any{"type": "decision", "title": "Outage evidence", "authority": "explicit", "payload": map[string]any{"decision": strings.Repeat("x", 5500) + " Retain EXACT_IDENTIFIER_42 during provider outages. " + strings.Repeat("y", 1800)}})
	if status != 201 {
		t.Fatal("source fixture failed")
	}
	record := response["record"].(map[string]any)
	status, response = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "EXACT_IDENTIFIER_42"})
	if status != 200 || len(response["results"].([]any)) != 1 {
		t.Fatalf("provider outage discarded lexical evidence: %d %v", status, response)
	}
	item := response["results"].([]any)[0].(map[string]any)
	semantic := response["semantic"].(map[string]any)
	if item["recordId"] != record["id"] || !strings.Contains(item["text"].(string), "EXACT_IDENTIFIER_42") || item["sourceUrl"] != "/api/v1/records/"+record["id"].(string) || semantic["available"] != false || semantic["indexCoverage"] != "incomplete" || semantic["reason"] != "AI_UNAVAILABLE" {
		t.Fatalf("fallback lost citation or uncertainty: %v", response)
	}
}

func TestRAGHybridRetrievalKeepsExactIdentifiersAndSemanticParaphrases(t *testing.T) {
	path, databaseURL := ragInstance(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Input) != 1 {
			http.Error(w, "invalid input", 400)
			return
		}
		vector := []int{1, 0}
		if strings.Contains(body.Input[0], "EXACT_FUSION_ID_9") && strings.Contains(body.Input[0], "\n") {
			vector = []int{0, 1}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": vector}}})
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("provider fixture failed")
	}
	projectID := jobProject(t, origin, "Hybrid evidence", true, false)
	status, response := captureRequest(t, origin, projectID, "", map[string]any{"type": "finding", "title": "Exact identifier", "authority": "observed", "payload": map[string]any{"finding": "Retain EXACT_FUSION_ID_9 for recovery."}})
	if status != 201 {
		t.Fatal("exact source failed")
	}
	exactID := response["record"].(map[string]any)["id"].(string)
	paraphrase := curationCapture(t, origin, projectID, "Recovery rationale")
	jobs := waitRAGJobs(t, origin, 2)
	for _, value := range jobs {
		waitAIJob(t, origin, value.(map[string]any)["id"].(string), "completed")
	}
	status, response = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "EXACT_FUSION_ID_9"})
	if status != 200 || len(response["results"].([]any)) != 2 {
		t.Fatalf("hybrid retrieval lost lexical or semantic evidence: %d %v", status, response)
	}
	seen := map[string]bool{}
	for _, value := range response["results"].([]any) {
		item := value.(map[string]any)
		seen[item["recordId"].(string)] = true
	}
	if !seen[exactID] || !seen[paraphrase["id"].(string)] {
		t.Fatal("rank fusion did not preserve both cited evidence sources")
	}
}

func TestRAGPendingIndexReturnsRawLexicalEvidenceWithHealthyQueryProvider(t *testing.T) {
	path, databaseURL := ragInstance(t)
	release := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Input) != 1 {
			http.Error(w, "invalid input", 400)
			return
		}
		if strings.Contains(body.Input[0], "\n") {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer func() { close(release); provider.Close() }()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("configuration failed")
	}
	projectID := jobProject(t, origin, "Pending lexical evidence", true, false)
	curationCapture(t, origin, projectID, "EXACT_PENDING_INDEX")
	status, response := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "EXACT_PENDING_INDEX"})
	if status != 200 || len(response["results"].([]any)) != 1 {
		t.Fatalf("pending indexing hid lexical evidence: %d %v", status, response)
	}
	semantic := response["semantic"].(map[string]any)
	if semantic["available"] != false || semantic["reason"] != "RAG_INDEX_INCOMPLETE" || semantic["indexCoverage"] != "incomplete" {
		t.Fatal("pending indexing was misrepresented")
	}
}

func TestRAGMissingPgvectorReturnsLexicalEvidenceWithoutClaimingIndexCoverage(t *testing.T) {
	path, databaseURL := ragInstance(t)
	origin := startServer(t, path, databaseURL)
	projectID := jobProject(t, origin, "Missing index evidence", true, false)
	record := curationCapture(t, origin, projectID, "EXACT_MISSING_INDEX_TOKEN")
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`DROP EXTENSION vector CASCADE`); err != nil {
		t.Fatal(err)
	}
	status, response := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "EXACT_MISSING_INDEX_TOKEN"})
	if status != 200 || len(response["results"].([]any)) != 1 {
		t.Fatalf("missing pgvector discarded lexical evidence: %d %v", status, response)
	}
	item := response["results"].([]any)[0].(map[string]any)
	semantic := response["semantic"].(map[string]any)
	if item["recordId"] != record["id"] || semantic["available"] != false || semantic["indexCoverage"] != "unavailable" || semantic["reason"] != "RAG_UNAVAILABLE" {
		t.Fatal("missing index was presented as indexed or absent evidence")
	}
}
