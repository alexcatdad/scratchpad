package backend_test

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf16"
)

func TestCompletionOrdersMinuteSecondAndPreciseInstantsWithStableTies(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			var mu sync.Mutex
			seen := map[string]bool{}
			observed := []string{}
			provider := completionService(t, func(input map[string]any) any {
				mu.Lock()
				defer mu.Unlock()
				parts := input["records"].([]any)
				ids := []string{}
				for _, part := range parts {
					id := part.(map[string]any)["id"].(string)
					ids = append(ids, id)
					if !seen[id] {
						seen[id] = true
						observed = append(observed, id)
					}
				}
				return map[string]any{"artifacts": []any{map[string]any{"kind": "export", "title": "Ordered historical document", "content": map[string]any{"text": "Source-backed chronological document", "markdown": "## Batch " + ids[0] + "\n\n" + strings.Join(ids, ", ")}, "sourceRecordIds": []any{ids[0]}}}}
			})
			defer provider.Close()
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []any{}})
			if status != 200 {
				t.Fatal("provider fixture failed")
			}
			project := jobProject(t, origin, "Historical completion chronology", true, false)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			expected := []string{}
			for _, fixture := range []struct{ id, at string }{
				{"rec_0_minute", "2026-10-01T00:00Z"},
				{"rec_1_second", "2026-10-01T00:00:00.000Z"},
				{"rec_2_fraction", "2026-10-01T00:00:00.0000000001Z"},
				{"rec_3_equal", "2026-10-01T00:00:00.0000000001000Z"},
				{"rec_4_later", "2026-10-01T00:00:00.0000000002Z"},
				{"rec_5_second", "2026-10-01T00:00:01Z"},
			} {
				finding := strings.Repeat("evidence ", 1000)
				record, _ := json.Marshal(map[string]any{"id": fixture.id, "projectId": project, "type": "finding", "title": fixture.id, "content": "finding: " + finding, "payload": map[string]any{"finding": finding}, "authority": "observed", "version": 1, "createdAt": "2026-10-01T00:00:00Z", "recordedAt": fixture.at})
				if _, err = tx.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('record',$1,$2,1,'2026-10-01T00:00:00Z')`, fixture.id, string(record)); err != nil {
					t.Fatal(err)
				}
				metadata, _ := json.Marshal(map[string]any{"id": fixture.id, "recordId": fixture.id, "tags": []any{}, "archived": false, "version": 1, "createdAt": "2026-10-01T00:00:00Z"})
				if _, err = tx.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('metadata',$1,$2,1,'2026-10-01T00:00:00Z')`, fixture.id, string(metadata)); err != nil {
					t.Fatal(err)
				}
				expected = append(expected, fixture.id)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			status, queued := curationRequest(t, origin, "POST", "/summaries/export", map[string]any{"projectId": project, "format": "decisions"})
			if status != 202 {
				t.Fatal("export enqueue failed")
			}
			waitAIJob(t, origin, queued["job"].(map[string]any)["id"].(string), "completed")
			mu.Lock()
			got := append([]string{}, observed...)
			mu.Unlock()
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("completion reordered precise instants or equivalent historical ties: got%v want%v", got, expected)
			}
			status, result := curationRequest(t, origin, "GET", "/suggestions?projectId="+project, nil)
			if status != 200 || len(result["suggestions"].([]any)) != 1 {
				t.Fatal("ordered export missing")
			}
			markdown := result["suggestions"].([]any)[0].(map[string]any)["content"].(map[string]any)["markdown"].(string)
			previous := -1
			for _, id := range expected {
				position := strings.Index(markdown, "## Batch "+id)
				if position <= previous {
					t.Fatal("document sections lost chronological batch order")
				}
				previous = position
			}
		})
	}
}

func TestExportBoundsCombinedSectionsAndFinalCitationsBeforeActivation(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		for _, kind := range []string{"sections", "citations", "within-bound"} {
			t.Run(engine+"/"+kind, func(t *testing.T) {
				path, databaseURL := portableInstance(t, engine)
				var calls atomic.Int32
				provider := completionService(t, func(input map[string]any) any {
					calls.Add(1)
					record := input["records"].([]any)[0].(map[string]any)
					markdown := strings.Repeat("section ", 3750)
					if kind == "citations" {
						markdown = strings.Repeat("💡", 24750)
					}
					if kind == "within-bound" {
						markdown = strings.Repeat("💡", 24000)
					}
					return map[string]any{"artifacts": []any{map[string]any{"kind": "export", "title": "Bounded private document", "content": map[string]any{"text": "Source-backed document", "markdown": markdown}, "sourceRecordIds": []any{record["id"]}}}}
				})
				defer provider.Close()
				origin := startServer(t, path, databaseURL)
				status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []any{}})
				if status != 200 {
					t.Fatal("provider fixture failed")
				}
				project := jobProject(t, origin, "Whole document bound", true, false)
				finding, title := "Cited source evidence.", strings.Repeat("T", 500)
				if kind == "sections" {
					finding = strings.Repeat("evidence ", 7000)
					title = "Long source evidence"
				}
				status, _ = captureRequest(t, origin, project, "", map[string]any{"type": "finding", "title": title, "authority": "observed", "payload": map[string]any{"finding": finding}})
				if status != 201 {
					t.Fatal("source fixture failed")
				}
				status, queued := curationRequest(t, origin, "POST", "/summaries/export", map[string]any{"projectId": project, "format": "handoff"})
				if status != 202 {
					t.Fatal("export enqueue failed")
				}
				job := waitProviderReviewJob(t, origin, queued["job"].(map[string]any)["id"].(string))
				if kind == "within-bound" {
					if job["status"] != "completed" || job["attempts"] != float64(1) || calls.Load() != 1 {
						t.Fatal("valid Unicode document was measured as UTF-8 bytes or rejected")
					}
					status, result := curationRequest(t, origin, "GET", "/suggestions?projectId="+project, nil)
					if status != 200 || len(result["suggestions"].([]any)) != 1 {
						t.Fatal("valid bounded document missing")
					}
					markdown := result["suggestions"].([]any)[0].(map[string]any)["content"].(map[string]any)["markdown"].(string)
					if len(utf16.Encode([]rune(markdown))) > 50000 || !strings.Contains(markdown, "AI-generated draft") || !strings.Contains(markdown, title) {
						t.Fatal("valid document lost its bound, draft label or citations")
					}
					return
				}
				if job["status"] != "failed" || job["attempts"] != float64(1) || job["lastError"] != "Generated document exceeds the 50000-character limit including source citations." {
					t.Fatalf("oversized whole document activated or retried: %+v", job)
				}
				wantedCalls := int32(2)
				if kind == "citations" {
					wantedCalls = 1
				}
				if calls.Load() != wantedCalls {
					t.Fatalf("oversized document continued unnecessary generation: calls%d want%d", calls.Load(), wantedCalls)
				}
				status, result := curationRequest(t, origin, "GET", "/suggestions?projectId="+project, nil)
				if status != 200 || len(result["suggestions"].([]any)) != 0 {
					t.Fatal("oversized document left a partially activated artifact")
				}
			})
		}
	}
}
