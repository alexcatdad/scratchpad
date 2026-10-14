package backend_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf16"
)

func assertRAGEvidenceFields(t *testing.T, results []any, record map[string]any, fields ...string) map[string]map[string]any {
	t.Helper()
	if len(results) != len(fields) {
		t.Fatalf("got%d evidence items, want%d", len(results), len(fields))
	}
	wanted := map[string]bool{}
	for _, field := range fields {
		wanted[field] = true
	}
	items := map[string]map[string]any{}
	identities := map[string]bool{}
	for _, raw := range results {
		item := raw.(map[string]any)
		location := item["sourceLocation"].(map[string]any)
		field := location["field"].(string)
		id := item["id"].(string)
		if item["recordId"] != record["id"] || !wanted[field] || items[field] != nil || identities[id] {
			t.Fatal("duplicate, unrelated or incorrectly located evidence")
		}
		units := utf16.Encode([]rune(record[field].(string)))
		start, end := int(location["start"].(float64)), int(location["end"].(float64))
		if start < 0 || end < start || end > len(units) || location["unit"] != "utf16" || string(utf16.Decode(units[start:end])) != item["text"] {
			t.Fatal("citation range does not identify exact immutable source text")
		}
		items[field], identities[id] = item, true
	}
	return items
}

func TestRAGPassageLexicalMatchCannotBeCrowdedOutByRepeatedTitles(t *testing.T) {
	path, databaseURL := ragInstance(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2, "similarityThreshold": 0.5})
	if status != 200 {
		t.Fatal("settings failed")
	}
	project := jobProject(t, origin, "Passage lexical grounding", true, false)
	records := map[string]map[string]any{}
	var witness map[string]any
	for i := 0; i < 8; i++ {
		title, text := strings.Repeat("LOCAL_WITNESS ", 20)+fmt.Sprint(i), strings.Repeat("x", 95000)
		if i == 7 {
			title, text = "Genuine body evidence", strings.Repeat("y", 3000)+" LOCAL_WITNESS belongs to this body passage. "+strings.Repeat("z", 7000)
		}
		status, captured := captureRequest(t, origin, project, "", map[string]any{"type": "finding", "title": title, "authority": "observed", "payload": map[string]any{"finding": text}})
		if status != 201 {
			t.Fatal("capture failed")
		}
		record := captured["record"].(map[string]any)
		records[record["id"].(string)] = record
		if i == 7 {
			witness = record
		}
	}
	jobs := waitRAGJobs(t, origin, 8)
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Synthetic source-faithful historical projections avoid 114 worker ticks.
	for _, raw := range jobs {
		job := raw.(map[string]any)
		record := records[job["recordId"].(string)]
		content := record["content"].(string)
		count := 0
		for start := 0; start < len(content); start += 6000 {
			end := min(start+6000, len(content))
			identity, err := json.Marshal(map[string]any{"recordId": record["id"], "contentHash": job["contentHash"], "processingVersion": "passage-v1", "start": start, "end": end})
			if err != nil {
				t.Fatal(err)
			}
			id := fmt.Sprintf("passage_%x", sha256.Sum256(identity))
			if _, err = tx.Exec(`INSERT INTO rag_passages(id,fingerprint,record_id,project_id,content_hash,processing_version,source_start,source_end,text,dimensions,embedding) VALUES($1,$2,$3,$4,$5,'passage-v1',$6,$7,$8,2,'[0,1]'::vector)`, id, job["fingerprint"], record["id"], project, job["contentHash"], start, end, content[start:end]); err != nil {
				t.Fatal(err)
			}
			count++
		}
		patch, _ := json.Marshal(map[string]any{"status": "completed", "passageCount": count})
		if _, err = tx.Exec(`UPDATE entities SET data=data || $1::jsonb WHERE kind='ai_job' AND id=$2`, patch, job["id"]); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	status, coverage := curationRequest(t, origin, "GET", "/rag/coverage?projectId="+project, nil)
	if status != 200 || coverage["status"] != "complete" || coverage["indexedSources"] != float64(8) {
		t.Fatal("invalid historical index fixture")
	}
	check := func(t *testing.T, enabled bool) {
		status, result := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": project, "query": "LOCAL_WITNESS", "limit": 100, "maxChars": 64000})
		if status != 200 {
			t.Fatalf("retrieval failed: HTTP%d %v", status, result)
		}
		items := result["results"].([]any)
		if len(items) != 8 || result["resultsOmitted"] != float64(0) {
			t.Fatalf("expected one local body match and seven distinct title hits: %d results, %v omitted", len(items), result["resultsOmitted"])
		}
		if result["semantic"].(map[string]any)["available"] != enabled {
			t.Fatal("wrong semantic availability")
		}
		seen := map[string]bool{}
		for _, raw := range items {
			item := raw.(map[string]any)
			id := item["recordId"].(string)
			if seen[id] {
				t.Fatal("title repeated across unrelated passages")
			}
			seen[id] = true
			location := item["sourceLocation"].(map[string]any)
			field := location["field"].(string)
			if id == witness["id"] && field != "content" || id != witness["id"] && field != "title" {
				t.Fatal("wrong lexical provenance")
			}
			units := utf16.Encode([]rune(records[id][field].(string)))
			start, end := int(location["start"].(float64)), int(location["end"].(float64))
			if string(utf16.Decode(units[start:end])) != item["text"] || !strings.Contains(item["text"].(string), "LOCAL_WITNESS") {
				t.Fatal("citation does not identify matching source text")
			}
		}
	}
	t.Run("disabled-provider", func(t *testing.T) { check(t, false) })
	status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "enabled": true})
	if status != 200 {
		t.Fatal("enable failed")
	}
	t.Run("complete-index", func(t *testing.T) { check(t, true) })
}

func TestRAGCompleteTitleQueryDoesNotCitePartiallyMatchingBody(t *testing.T) {
	path, databaseURL := ragInstance(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Input) != 1 {
			http.Error(w, "invalid", 400)
			return
		}
		vector := []int{1, 0}
		if strings.Contains(body.Input[0], "\n") {
			vector = []int{0, 1}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": vector}}})
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2, "similarityThreshold": 0.5})
	if status != 200 {
		t.Fatal("settings failed")
	}
	project := jobProject(t, origin, "Mixed title/body terms", true, false)
	status, captured := captureRequest(t, origin, project, "", map[string]any{"type": "finding", "title": "🙂 TITLE_WITNESS SECOND_TERM", "authority": "observed", "payload": map[string]any{"finding": "TITLE_WITNESS appears in the body without the other query term."}})
	if status != 201 {
		t.Fatal("capture failed")
	}
	record := captured["record"].(map[string]any)
	jobs := waitRAGJobs(t, origin, 1)
	waitAIJob(t, origin, jobs[0].(map[string]any)["id"].(string), "completed")
	status, result := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": project, "query": "TITLE_WITNESS SECOND_TERM"})
	if status != 200 || len(result["results"].([]any)) != 1 {
		t.Fatalf("title evidence lost HTTP%d %v", status, result)
	}
	item := result["results"].([]any)[0].(map[string]any)
	location := item["sourceLocation"].(map[string]any)
	if item["recordId"] != record["id"] || location["field"] != "title" || item["text"] != record["title"] || location["start"] != float64(0) || location["end"] != float64(len(utf16.Encode([]rune(record["title"].(string))))) {
		t.Fatal("title match cited a partially matching body")
	}
}
