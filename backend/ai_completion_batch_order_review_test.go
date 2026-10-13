package backend_test

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"
)

func TestCrossProjectCompletionOrdersSelectedBatchesWithoutChangingFairCoverage(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			var mu sync.Mutex
			observed := [][]map[string]any{}
			provider := completionService(t, func(input map[string]any) any {
				batch, ids := []map[string]any{}, []any{}
				for _, value := range input["records"].([]any) {
					record := value.(map[string]any)
					batch = append(batch, record)
					ids = append(ids, record["id"])
				}
				mu.Lock()
				observed = append(observed, batch)
				mu.Unlock()
				return map[string]any{"artifacts": []any{map[string]any{"kind": "summary", "title": "Selected source histories", "content": map[string]any{"text": "Independent histories retain their recorded chronology."}, "sourceRecordIds": ids}}}
			})
			defer provider.Close()
			origin := startServer(t, path, databaseURL)
			first := jobProject(t, origin, "Earlier project", true, true)
			second := jobProject(t, origin, "Later project", true, true)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []any{"summary"}})
			if status != 200 {
				t.Fatal("provider configuration failed")
			}
			driver, location := "sqlite", path
			if engine == "postgres" {
				driver, location = "pgx", databaseURL
			}
			db, err := sql.Open(driver, location)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			instants := map[string]string{}
			content := "Complete source: " + strings.Repeat("α", 3900) + "💡end"
			for _, fixture := range []struct{ id, project, at string }{
				{"rec_batch_a0", first, "2026-10-01T00:00Z"},
				{"rec_batch_a1", first, "2026-10-01T00:00:00.000Z"},
				{"rec_batch_a2", first, "2026-10-01T00:00:00.0000000001Z"},
				{"rec_batch_a3", first, "2026-10-01T00:00:00.0000000001000Z"},
				{"rec_batch_b4", second, "2026-10-01T00:00:00.0000000002Z"},
				{"rec_batch_b5", second, "2026-10-01T00:00:01Z"},
			} {
				instants[fixture.id] = fixture.at
				record, _ := json.Marshal(map[string]any{"id": fixture.id, "projectId": fixture.project, "type": "finding", "title": fixture.id, "content": content, "payload": map[string]any{"finding": content}, "authority": "observed", "version": 1, "createdAt": "2026-10-01T00:00:00Z", "recordedAt": fixture.at})
				if _, err = tx.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('record',$1,$2,1,'2026-10-01T00:00:00Z')`, fixture.id, string(record)); err != nil {
					t.Fatal(err)
				}
				metadata, _ := json.Marshal(map[string]any{"id": fixture.id, "recordId": fixture.id, "tags": []any{}, "archived": false, "version": 1, "createdAt": "2026-10-01T00:00:00Z"})
				if _, err = tx.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('metadata',$1,$2,1,'2026-10-01T00:00:00Z')`, fixture.id, string(metadata)); err != nil {
					t.Fatal(err)
				}
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			status, queued := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "analyze", "projectIds": []any{first, second}, "crossProject": true})
			if status != 202 {
				t.Fatal("cross-project enqueue failed")
			}
			waitAIJob(t, origin, queued["job"].(map[string]any)["id"].(string), "completed")
			mu.Lock()
			batches := append([][]map[string]any{}, observed...)
			mu.Unlock()
			got := [][]string{}
			for _, batch := range batches {
				ids, projects, size := []string{}, map[string]bool{}, 0
				for _, record := range batch {
					id := record["id"].(string)
					ids = append(ids, id)
					projects[record["projectId"].(string)] = true
					if record["recordedAt"] != instants[id] || record["content"] != content {
						t.Fatal("provider input changed exact timestamp or complete Unicode content")
					}
					encoded, _ := json.Marshal(record)
					size += len(utf16.Encode([]rune(string(encoded))))
				}
				if len(batch) > 40 || size > 14000 {
					t.Fatal("provider batch exceeded accepted input budgets")
				}
				if len(projects) != 2 {
					t.Fatal("chronological presentation lost independent-project batch coverage")
				}
				got = append(got, ids)
			}
			// Round-robin selection places a0/b4/a1 in the first batch and
			// b5/a2/a3 in the second. Each request presents its selected sources
			// chronologically; later requests need not form one global chronology.
			want := [][]string{{"rec_batch_a0", "rec_batch_a1", "rec_batch_b4"}, {"rec_batch_a2", "rec_batch_a3", "rec_batch_b5"}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("provider request ordering or fair membership changed: got%v want%v", got, want)
			}
		})
	}
}

func TestCrossProjectCompletionRetainsOrderedUnicodeFragmentsAcrossBatches(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			var mu sync.Mutex
			contents, nextPart, totals := map[string]string{}, map[string]int{}, map[string]int{}
			calls, invalid := 0, false
			provider := completionService(t, func(input map[string]any) any {
				mu.Lock()
				defer mu.Unlock()
				calls++
				ids, size := []any{}, 0
				for _, value := range input["records"].([]any) {
					item := value.(map[string]any)
					id := item["id"].(string)
					nextPart[id]++
					part, total := int(item["part"].(float64)), int(item["totalParts"].(float64))
					if part != nextPart[id] || (totals[id] != 0 && totals[id] != total) {
						invalid = true
					}
					totals[id] = total
					contents[id] += item["content"].(string)
					encoded, _ := json.Marshal(item)
					size += len(utf16.Encode([]rune(string(encoded))))
					ids = append(ids, id)
				}
				if size > 14000 || len(ids) > 40 {
					invalid = true
				}
				return map[string]any{"artifacts": []any{map[string]any{"kind": "summary", "title": "Complete source fragments", "content": map[string]any{"text": "Preserve every supplied source fragment."}, "sourceRecordIds": []any{ids[0]}}}}
			})
			defer provider.Close()
			origin := startServer(t, path, databaseURL)
			first := jobProject(t, origin, "Long Unicode source", true, true)
			second := jobProject(t, origin, "Independent source", true, true)
			want := map[string]string{}
			for _, source := range []struct{ project, text string }{{first, strings.Repeat("x", 5999) + "💡" + strings.Repeat("y", 5999) + "💡" + strings.Repeat("z", 5999)}, {second, "Independent second-project evidence."}} {
				status, captured := captureRequest(t, origin, source.project, "", map[string]any{"type": "finding", "title": "Fragment source", "authority": "observed", "payload": map[string]any{"finding": source.text}})
				if status != 201 {
					t.Fatal("fragment fixture capture failed")
				}
				record := captured["record"].(map[string]any)
				want[record["id"].(string)] = record["content"].(string)
			}
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []any{"summary"}})
			if status != 200 {
				t.Fatal("fragment provider configuration failed")
			}
			status, queued := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "analyze", "projectIds": []any{first, second}, "crossProject": true})
			if status != 202 {
				t.Fatal("fragment enqueue failed")
			}
			waitAIJob(t, origin, queued["job"].(map[string]any)["id"].(string), "completed")
			mu.Lock()
			defer mu.Unlock()
			if invalid || calls < 2 || !reflect.DeepEqual(contents, want) {
				t.Fatal("completion reordered, omitted or corrupted source fragments across bounded requests")
			}
			for id, count := range nextPart {
				if count != totals[id] {
					t.Fatal("completion omitted declared source fragments")
				}
			}
		})
	}
}
