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

func TestRAGPartialIndexCombinesSemanticEvidenceWithPendingLexicalSources(t *testing.T) {
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
		if strings.Contains(body.Input[0], "PENDING_PARTIAL_INDEX") && strings.Contains(body.Input[0], "\n") {
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
	projectID := jobProject(t, origin, "Partial index evidence", true, false)
	indexed := curationCapture(t, origin, projectID, "Established recovery rationale")
	jobs := waitRAGJobs(t, origin, 1)
	waitAIJob(t, origin, jobs[0].(map[string]any)["id"].(string), "completed")
	status, captured := captureRequest(t, origin, projectID, "", map[string]any{"type": "decision", "title": "Pending recovery detail", "authority": "explicit", "payload": map[string]any{"decision": "Retain raw PENDING_PARTIAL_INDEX evidence for recovery."}})
	if status != 201 {
		t.Fatal("pending source capture failed")
	}
	pending := captured["record"].(map[string]any)
	otherProject := jobProject(t, origin, "Outside partial retrieval scope", true, false)
	curationCapture(t, origin, otherProject, "PENDING_PARTIAL_INDEX")
	status, response := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "PENDING_PARTIAL_INDEX", "maxChars": 4096})
	if status != 200 {
		t.Fatalf("partial retrieval failed: %d %v", status, response)
	}
	results := response["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("healthy partial index hid raw or semantic evidence: %v", response)
	}
	seen := map[string]bool{}
	for _, value := range results {
		item := value.(map[string]any)
		id := item["recordId"].(string)
		if seen[id] {
			t.Fatal("partial merge duplicated a source passage")
		}
		seen[id] = true
		if item["sourceUrl"] != "/api/v1/records/"+id || item["rankScore"] == nil {
			t.Fatal("partial merge lost citation or fused rank")
		}
		if item["projectId"] != projectID {
			t.Fatal("raw merge leaked another project's evidence")
		}
		if id == pending["id"] && !strings.Contains(item["text"].(string), "PENDING_PARTIAL_INDEX") {
			t.Fatal("pending exact match was absent from its cited excerpt")
		}
	}
	if !seen[indexed["id"].(string)] || !seen[pending["id"].(string)] {
		t.Fatal("partial merge omitted an eligible source")
	}
	semantic := response["semantic"].(map[string]any)
	if semantic["available"] != true || semantic["indexCoverage"] != "incomplete" {
		t.Fatal("healthy query provider or partial coverage misrepresented")
	}
	encoded, _ := json.Marshal(response)
	if len(encoded)+1 > 4096 {
		t.Fatal("partial merge exceeded byte budget")
	}
	// Both raw sources match this term, including the already indexed passage.
	status, response = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "raw"})
	if status != 200 || len(response["results"].([]any)) != 2 {
		t.Fatalf("overlapping lexical and semantic candidates were duplicated or lost: %d %v", status, response)
	}
	seen = map[string]bool{}
	for _, value := range response["results"].([]any) {
		item := value.(map[string]any)
		id := item["id"].(string)
		if seen[id] {
			t.Fatal("raw lexical merge duplicated an indexed passage")
		}
		seen[id] = true
	}
}
