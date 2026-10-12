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
)

func TestRAGFusedOmissionsCountAllDistinctCandidates(t *testing.T) {
	path, databaseURL := ragInstance(t)
	// These synthetic vectors exercise fusion accounting, not model quality.
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "invalid", 400)
			return
		}
		data := []any{}
		for i, input := range body.Input {
			vector := []int{1, 0}
			if strings.Contains(input, "FUSION_WITNESS") && strings.Contains(input, "\n") {
				vector = []int{0, 1}
			}
			data = append(data, map[string]any{"index": i, "embedding": vector})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2, "similarityThreshold": 0.5})
	if status != 200 {
		t.Fatal("provider fixture failed")
	}
	project := jobProject(t, origin, "Fusion omissions", true, false)
	records := map[string]map[string]any{}
	for i := 0; i < 200; i++ {
		title := fmt.Sprintf("SEMANTIC_WITNESS %03d", i)
		if i >= 100 {
			title = fmt.Sprintf("FUSION_WITNESS %03d", i)
		}
		record := curationCapture(t, origin, project, title)
		records[record["id"].(string)] = record
	}
	jobs := waitRAGJobs(t, origin, 200)
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Install a historical source-faithful synthetic index fixture. Public
	// captures supply the canonical source hashes and generation fingerprints;
	// exercising 200 one-second worker ticks is unrelated to result accounting.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, value := range jobs {
		job := value.(map[string]any)
		record := records[job["recordId"].(string)]
		content := record["content"].(string)
		identity, err := json.Marshal(map[string]any{"recordId": record["id"], "contentHash": job["contentHash"], "processingVersion": "passage-v1", "start": 0, "end": len(content)})
		if err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("passage_%x", sha256.Sum256(identity))
		vector := "[1,0]"
		if strings.Contains(record["title"].(string), "FUSION_WITNESS") {
			vector = "[0,1]"
		}
		if _, err = tx.Exec(`INSERT INTO rag_passages(id,fingerprint,record_id,project_id,content_hash,processing_version,source_start,source_end,text,dimensions,embedding) VALUES($1,$2,$3,$4,$5,'passage-v1',0,$6,$7,2,$8::vector)`, id, job["fingerprint"], record["id"], project, job["contentHash"], len(content), content, vector); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`UPDATE entities SET data=data || '{"status":"completed","passageCount":1}'::jsonb WHERE kind='ai_job' AND id=$1`, job["id"]); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "enabled": true})
	if status != 200 {
		t.Fatal("provider enable failed")
	}
	status, coverage := curationRequest(t, origin, "GET", "/rag/coverage?projectId="+project, nil)
	if status != 200 || coverage["status"] != "complete" || coverage["indexedSources"] != float64(200) {
		t.Fatalf("historical index fixture invalid: HTTP%d %v", status, coverage)
	}
	for _, check := range []struct {
		query                string
		limit, budget, total int
	}{
		{"FUSION_WITNESS", 100, 64000, 200},
		{"FUSION_WITNESS", 5, 64000, 200},
		{"FUSION_WITNESS", 100, 4096, 200},
		{"SEMANTIC_WITNESS", 100, 64000, 100},
	} {
		t.Run(fmt.Sprintf("%s/%d/%d", check.query, check.limit, check.budget), func(t *testing.T) {
			status, result := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": project, "query": check.query, "limit": check.limit, "maxChars": check.budget})
			if status != 200 {
				t.Fatalf("retrieval failed HTTP%d %v", status, result)
			}
			items := result["results"].([]any)
			omitted := int(result["resultsOmitted"].(float64))
			if len(items)+omitted != check.total {
				t.Fatalf("fusion lost omission accounting: returned%d omitted%d wanttotal%d", len(items), omitted, check.total)
			}
			seen := map[string]bool{}
			groups := map[string]bool{}
			for _, raw := range items {
				item := raw.(map[string]any)
				id := item["id"].(string)
				record := records[item["recordId"].(string)]
				if seen[id] || record == nil {
					t.Fatal("duplicate or uncited evidence")
				}
				if strings.Contains(record["title"].(string), "FUSION_WITNESS") {
					groups["lexical"] = true
				} else {
					groups["semantic"] = true
				}
				seen[id] = true
			}
			if check.query == "FUSION_WITNESS" && len(groups) != 2 {
				t.Fatal("disjoint semantic and lexical evidence did not survive fusion")
			}
			if check.query == "SEMANTIC_WITNESS" && groups["lexical"] {
				t.Fatal("deduplicated overlapping retrieval admitted unrelated evidence")
			}
			encoded, err := json.Marshal(result)
			if err != nil || len(encoded)+1 > check.budget || len(items) > check.limit || len(items) == 0 {
				t.Fatal("result bounding failed")
			}
		})
	}
}
