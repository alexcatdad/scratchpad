package backend_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf16"
)

func TestRAGReplacementOutsideRequestedConsentDoesNotHideEvidence(t *testing.T) {
	path, databaseURL := ragInstance(t)
	var outage atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if outage.Load() {
			http.Error(w, "synthetic outage", 503)
			return
		}
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("configuration failed")
	}
	selected := jobProject(t, origin, "Selected scope", true, false)
	excluded := jobProject(t, origin, "Excluded nonconsenting scope", false, false)
	source := curationCapture(t, origin, selected, "SCOPED_SOURCE_EVIDENCE")
	replacement := curationCapture(t, origin, excluded, "Excluded replacement")
	for _, job := range waitRAGJobs(t, origin, 1) {
		waitAIJob(t, origin, job.(map[string]any)["id"].(string), "completed")
	}
	status, _ = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": replacement["id"], "toRecordId": source["id"], "type": "replaces", "authority": "explicit"})
	if status != 201 {
		t.Fatal("accepted replacement failed")
	}
	for _, fallback := range []bool{false, true} {
		outage.Store(fallback)
		for _, intent := range []string{"current", "history"} {
			status, response := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": selected, "query": "SCOPED_SOURCE_EVIDENCE", "intent": intent})
			if status != 200 {
				t.Fatalf("excluded replacement hid scoped evidence (fallback=%v intent=%s): %d %v", fallback, intent, status, response)
			}
			fields := []string{"title"}
			if !fallback {
				fields = append(fields, "content")
			}
			items := assertRAGEvidenceFields(t, response["results"].([]any), source, fields...)
			for _, item := range items {
				if item["applicability"] != "current" || len(item["relationships"].([]any)) != 0 {
					t.Fatalf("excluded relationship escaped requested scope: %v", item)
				}
			}
		}
	}
}

func TestRAGIndexedLexicalExcerptContainsLateMatchAndExactSourceBounds(t *testing.T) {
	path, databaseURL := ragInstance(t)
	var outage atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if outage.Load() {
			http.Error(w, "synthetic outage", 503)
			return
		}
		var input struct {
			Input []string `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Input) != 1 {
			http.Error(w, "invalid fixture input", 400)
			return
		}
		vector := "[1,0]"
		if strings.Contains(input.Input[0], "\n") {
			vector = "[0,1]"
		}
		w.Write([]byte(`{"data":[{"index":0,"embedding":` + vector + `}]} `))
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("configuration failed")
	}
	projectID := jobProject(t, origin, "Late lexical match", true, false)
	status, response := captureRequest(t, origin, projectID, "", map[string]any{"type": "finding", "title": "Long evidence", "authority": "observed", "payload": map[string]any{"finding": strings.Repeat("😀 filler ", 180) + " LATE_LEXICAL_NEEDLE_73 preserves recovery evidence. " + strings.Repeat("🙂 trailing ", 80)}})
	if status != 201 {
		t.Fatal("source fixture failed")
	}
	source := response["record"].(map[string]any)
	for _, job := range waitRAGJobs(t, origin, 1) {
		waitAIJob(t, origin, job.(map[string]any)["id"].(string), "completed")
	}
	for _, fallback := range []bool{false, true} {
		outage.Store(fallback)
		status, response = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "LATE_LEXICAL_NEEDLE_73"})
		if status != 200 || len(response["results"].([]any)) != 1 {
			t.Fatalf("lexical evidence unavailable (fallback=%v): %d %v", fallback, status, response)
		}
		item := response["results"].([]any)[0].(map[string]any)
		text := item["text"].(string)
		location := item["sourceLocation"].(map[string]any)
		start, end := int(location["start"].(float64)), int(location["end"].(float64))
		units := utf16.Encode([]rune(source["content"].(string)))
		if item["recordId"] != source["id"] || !strings.Contains(text, "LATE_LEXICAL_NEEDLE_73") || start <= 1000 || end > len(units) || len(utf16.Encode([]rune(text))) > 1000 || string(utf16.Decode(units[start:end])) != text || strings.ContainsRune(text, '\ufffd') {
			t.Fatalf("late lexical match or UTF16 citation lost (fallback=%v): %v", fallback, item)
		}
	}
}
