package backend_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// Labels are authored from source meaning, independently of ranking or embeddings.
// The local lane evaluates admission/citations and lexical recall. The optional
// lane uses actual provider embeddings; synthetic vectors cannot pass as quality.
func TestRAGRepresentativeEvaluation(t *testing.T) {
	for _, mode := range []string{"lexical", "model"} {
		t.Run(mode, func(t *testing.T) {
			settings := map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0}
			if mode == "model" {
				endpoint := os.Getenv("TEST_RAG_EVALUATION_BASE_URL")
				if endpoint == "" {
					t.Skip("real embedding provider not supplied; paraphrase quality remains unverified")
				}
				parsed, err := url.Parse(endpoint)
				if err != nil || parsed.Scheme != "http" || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1") || parsed.User != nil {
					t.Fatal("evaluation requires an explicitly supplied credential-free loopback HTTP provider")
				}
				dimensions, err := strconv.Atoi(os.Getenv("TEST_RAG_EVALUATION_DIMENSIONS"))
				model := os.Getenv("TEST_RAG_EVALUATION_MODEL")
				if err != nil || dimensions < 1 || model == "" {
					t.Fatal("evaluation requires explicit embedding model and dimensions")
				}
				settings["enabled"], settings["baseUrl"], settings["embeddingModel"], settings["embeddingDimensions"] = true, endpoint, model, dimensions
				settings["similarityThreshold"] = 0.5
				settings["requestTimeoutSeconds"] = 30
			}
			path, databaseURL := ragInstance(t)
			origin := startServer(t, path, databaseURL)
			if status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", settings); status != 200 {
				t.Fatal("evaluation configuration failed")
			}
			project := jobProject(t, origin, "Evaluation primary", true, true)
			outside := jobProject(t, origin, "Evaluation outside scope", true, true)
			private := jobProject(t, origin, "Evaluation forbidden", false, false)
			records := map[string]map[string]any{}
			add := func(label, projectID, title, text string) {
				status, response := captureRequest(t, origin, projectID, "", map[string]any{"type": "finding", "title": title, "authority": "observed", "confidence": "high", "payload": map[string]any{"finding": text}})
				if status != 201 {
					t.Fatalf("corpus source %s failed: %d", label, status)
				}
				records[label] = response["record"].(map[string]any)
			}
			add("exact", project, "Installation key", "KEEP_KEY_42 identifies the deployment encryption key. Retain that file outside the database backup.")
			add("paraphrase", project, "Credential retirement", "Removing a published SSH key revokes its derived sessions immediately. Republishing the public key does not restore previously invalidated sessions.")
			add("long", project, "Recovery appendix", strings.Repeat("Synthetic unrelated appendix material. ", 210)+" LONG_RECOVERY_77: restore the retained encryption key before enabling provider requests. "+strings.Repeat("Additional unrelated appendix. ", 70))
			add("old", project, "Superseded backup policy", "BACKUP_POLICY_83: back up only the database; installation keys may be discarded.")
			add("new", project, "Current backup policy", "BACKUP_POLICY_83: retain the installation key separately, together with a restorable database backup.")
			add("conflictA", project, "Cache observation one", "CACHE_CONFLICT_19: the cache improved request latency on the synthetic workload.")
			add("conflictB", project, "Cache observation two", "CACHE_CONFLICT_19: the cache increased request latency on the synthetic workload.")
			add("partial", project, "Partly retained policy", "PARTIAL_POLICY_29: preserve database snapshots and preserve installation keys.")
			add("partialNew", project, "Snapshot refinement", "PARTIAL_POLICY_29: database snapshots now include an integrity verification step.")
			add("outside", outside, "Other project install", "KEEP_KEY_42 is also used in another project. OUTSIDE_SCOPE_31 identifies this evidence.")
			add("private", private, "Private installation", "KEEP_KEY_42 FORBIDDEN_SCOPE_31 private project evidence.")
			for i := 0; i < 24; i++ {
				add(fmt.Sprintf("distractor%d", i), project, fmt.Sprintf("Build observation %d", i), "A synthetic build completed and emitted a temporary artifact. No recovery or credential behavior was measured.")
			}
			edgeIDs := map[string]string{}
			for _, edge := range []struct{ from, to, kind string }{{"new", "old", "replaces"}, {"conflictA", "conflictB", "contradicts"}, {"partialNew", "partial", "partially_replaces"}} {
				status, response := curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": records[edge.from]["id"], "toRecordId": records[edge.to]["id"], "type": edge.kind, "authority": "explicit"})
				if status != 201 {
					t.Fatal("evaluation relationship failed")
				}
				edgeIDs[edge.kind] = response["relationship"].(map[string]any)["id"].(string)
			}
			if mode == "model" {
				jobs := waitRAGJobs(t, origin, len(records)-1)
				deadline := time.Now().Add(3 * time.Minute)
				for {
					complete := 0
					for _, value := range jobs {
						job := value.(map[string]any)
						if job["status"] == "completed" {
							complete++
						}
						if job["status"] == "failed" {
							t.Fatal("real-provider indexing failed; inspect local job status")
						}
					}
					if complete == len(jobs) {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("real-provider corpus indexing exceeded three minutes")
					}
					time.Sleep(100 * time.Millisecond)
					_, response := curationRequest(t, origin, "GET", "/ai/jobs", nil)
					jobs = response["jobs"].([]any)
				}
			}
			cases := []struct {
				name, query, intent string
				relevant            []string
				cross               bool
			}{
				{"exact identifier", "KEEP_KEY_42", "current", []string{"exact"}, false},
				{"long passage", "LONG_RECOVERY_77", "current", []string{"long"}, false},
				{"current replacement", "BACKUP_POLICY_83", "current", []string{"new"}, false},
				{"historical policy", "BACKUP_POLICY_83", "history", []string{"old", "new"}, false},
				{"contradictions", "CACHE_CONFLICT_19", "current", []string{"conflictA", "conflictB"}, false},
				{"partial supersession", "PARTIAL_POLICY_29", "history", []string{"partial", "partialNew"}, false},
				{"explicit project union", "KEEP_KEY_42", "current", []string{"exact", "outside"}, true},
				{"unanswerable identifier", "ABSENT_ZEBRA_987654", "current", nil, false},
			}
			if mode == "model" {
				cases = append(cases, struct {
					name, query, intent string
					relevant            []string
					cross               bool
				}{"semantic paraphrase", "Can adding a removed machine credential again revive an invalidated login?", "current", []string{"paraphrase"}, false})
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					body := map[string]any{"projectId": project, "query": c.query, "intent": c.intent, "limit": 5, "maxChars": 12000}
					if c.cross {
						delete(body, "projectId")
						body["projectIds"], body["crossProject"] = []string{project, outside}, true
					}
					started := time.Now()
					status, response := curationRequest(t, origin, "POST", "/rag/retrieve", body)
					elapsed := time.Since(started)
					if status != 200 {
						t.Fatalf("retrieval failed: %d", status)
					}
					encoded, _ := json.Marshal(response)
					results := response["results"].([]any)
					semantic := response["semantic"].(map[string]any)
					if mode == "model" && (semantic["available"] != true || semantic["indexCoverage"] != "complete") {
						t.Fatal("model evaluation did not use a healthy complete semantic index")
					}
					if mode == "lexical" && (semantic["available"] != false || semantic["indexCoverage"] != "incomplete") {
						t.Fatal("pending corpus was presented as complete semantic evidence")
					}
					wanted := map[string]bool{}
					for _, label := range c.relevant {
						wanted[records[label]["id"].(string)] = true
					}
					found := map[string]bool{}
					seen := map[string]bool{}
					edges := map[string]bool{}
					for _, value := range results {
						item := value.(map[string]any)
						id := item["recordId"].(string)
						if seen[item["id"].(string)] {
							t.Fatal("duplicate passage")
						}
						seen[item["id"].(string)] = true
						if item["projectId"] != project && (!c.cross || item["projectId"] != outside) {
							t.Fatal("forbidden or out-of-scope evidence")
						}
						if c.intent == "current" && id == records["old"]["id"] {
							t.Fatal("stale evidence promoted to current")
						}
						if wanted[id] && (c.name != "long passage" || strings.Contains(item["text"].(string), "LONG_RECOVERY_77")) {
							found[id] = true
						}
						if item["sourceUrl"] != "/api/v1/records/"+id {
							t.Fatal("invalid source URL")
						}
						detailStatus, detail := curationRequest(t, origin, "GET", "/records/"+id, nil)
						if detailStatus != 200 {
							t.Fatal("citation cannot be inspected")
						}
						record := detail["record"].(map[string]any)
						if item["authority"] != record["authority"] || item["confidence"] != record["confidence"] {
							t.Fatal("retrieval ranking changed source authority or confidence")
						}
						source := utf16.Encode([]rune(record["content"].(string)))
						loc := item["sourceLocation"].(map[string]any)
						start, end := int(loc["start"].(float64)), int(loc["end"].(float64))
						if start < 0 || end > len(source) || start >= end || string(utf16.Decode(source[start:end])) != item["text"] {
							t.Fatal("excerpt differs from immutable citation")
						}
						if id == records["partial"]["id"] && item["applicability"] != "partially_superseded" {
							t.Fatal("partial replacement lost its applicability annotation")
						}
						for _, raw := range item["relationships"].([]any) {
							edges[raw.(map[string]any)["id"].(string)] = true
						}
					}
					recall := "n/a"
					if len(wanted) > 0 {
						recall = fmt.Sprintf("%.3f", float64(len(found))/float64(len(wanted)))
					}
					t.Logf("mode=%s recall@5=%s relevant=%d returned=%d duplicatePassages=0 staleCurrent=0 forbidden=0 bytes=%d latencyMs=%.3f", mode, recall, len(wanted), len(results), len(encoded)+1, float64(elapsed.Microseconds())/1000)
					if len(found) != len(wanted) {
						t.Fatalf("labeled evidence recall@5 below 1: %d/%d", len(found), len(wanted))
					}
					if len(encoded)+1 > 12000 {
						t.Fatal("response exceeded byte budget")
					}
					if c.name == "contradictions" && !edges[edgeIDs["contradicts"]] {
						t.Fatal("contradictory evidence lost its explicit relationship")
					}
					if mode == "lexical" && c.name == "unanswerable identifier" {
						if len(results) != 0 || response["semantic"].(map[string]any)["available"] != false || response["semantic"].(map[string]any)["indexCoverage"] != "incomplete" {
							t.Fatal("unanswerable query hid incomplete/unavailable index uncertainty")
						}
					}
				})
			}
			status, _ := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectIds": []string{project, private}, "crossProject": true, "query": "KEEP_KEY_42"})
			if status != 403 {
				t.Fatalf("explicit forbidden scope admitted: %d", status)
			}
		})
	}
}
