package backend_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRAGLexicalFallbackRetainsAcceptedHistoryEdgesAndHidesExcludedProjects(t *testing.T) {
	path, databaseURL := ragInstance(t)
	origin := startServer(t, path, databaseURL)
	projectID := jobProject(t, origin, "Fallback graph", true, false)
	status, response := captureRequest(t, origin, projectID, "", map[string]any{"type": "finding", "title": "Historical graph source", "authority": "observed", "payload": map[string]any{"finding": "Retain EXACT_FALLBACK_GRAPH as historical evidence."}})
	if status != 201 {
		t.Fatal("source fixture failed")
	}
	old := response["record"].(map[string]any)
	replacement := curationCapture(t, origin, projectID, "Replacement")
	contradiction := curationCapture(t, origin, projectID, "Contradictory evidence")
	wanted := map[string]bool{}
	for _, edge := range []struct {
		record map[string]any
		typ    string
	}{{replacement, "replaces"}, {contradiction, "contradicts"}} {
		status, response = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": edge.record["id"], "toRecordId": old["id"], "type": edge.typ, "authority": "explicit"})
		if status != 201 {
			t.Fatal("relationship fixture failed")
		}
		wanted[response["relationship"].(map[string]any)["id"].(string)] = true
	}
	privateProject := jobProject(t, origin, "Excluded graph", false, false)
	private := curationCapture(t, origin, privateProject, "Private evidence")
	status, _ = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": private["id"], "toRecordId": old["id"], "type": "related_to", "authority": "explicit", "note": "private-note-must-not-leak"})
	if status != 201 {
		t.Fatal("private graph fixture failed")
	}
	status, response = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "EXACT_FALLBACK_GRAPH", "intent": "history"})
	if status != 200 || len(response["results"].([]any)) != 1 {
		t.Fatalf("historical fallback missing: %d %v", status, response)
	}
	item := response["results"].([]any)[0].(map[string]any)
	if item["applicability"] != "superseded" || len(item["relationships"].([]any)) != 2 {
		t.Fatalf("fallback lost governing history edges: %v", item)
	}
	for _, value := range item["relationships"].([]any) {
		edge := value.(map[string]any)
		if !wanted[edge["id"].(string)] {
			t.Fatal("fallback included a relationship outside selected participation")
		}
	}
	encoded, _ := json.Marshal(response)
	if strings.Contains(string(encoded), private["id"].(string)) || strings.Contains(string(encoded), "private-note-must-not-leak") {
		t.Fatal("excluded project reference leaked")
	}
	status, response = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "EXACT_FALLBACK_GRAPH"})
	if status != 200 || len(response["results"].([]any)) != 0 {
		t.Fatal("historical source was promoted to current guidance during fallback")
	}
}

func TestRAGLexicalFallbackBudgetKeepsShortEvidenceAndReportsEdgeOmissions(t *testing.T) {
	path, databaseURL := ragInstance(t)
	origin := startServer(t, path, databaseURL)
	projectID := jobProject(t, origin, "Short budgeted evidence", true, false)
	record := curationCapture(t, origin, projectID, "EXACT_FALLBACK_BUDGET")
	for i := 0; i < 10; i++ {
		other := curationCapture(t, origin, projectID, "Related evidence")
		status, _ := curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": other["id"], "toRecordId": record["id"], "type": "related_to", "authority": "explicit", "note": strings.Repeat("文", 128)})
		if status != 201 {
			t.Fatal("relationship fixture failed")
		}
	}
	status, response := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "EXACT_FALLBACK_BUDGET", "maxChars": 4096})
	if status != 200 || len(response["results"].([]any)) != 1 {
		t.Fatalf("edge previews discarded short source evidence: %d %v", status, response)
	}
	item := response["results"].([]any)[0].(map[string]any)
	encoded, _ := json.Marshal(response)
	if len(encoded)+1 > 4096 || item["relationshipsOmitted"].(float64) < 1 || item["text"] != record["title"] || item["sourceLocation"].(map[string]any)["field"] != "title" {
		t.Fatal("fallback budget lost immutable excerpt or omission accounting")
	}
}
