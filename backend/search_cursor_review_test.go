package backend_test

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestSearchCursorVisitsMixedCaseHistoryExactlyOnce(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			var archive map[string]any
			if err := json.Unmarshal([]byte(exactNumberArchive), &archive); err != nil {
				t.Fatal(err)
			}
			data := archive["data"].(map[string]any)
			base := data["record"].([]any)[0].(map[string]any)
			records, metadata := []any{}, []any{}
			for _, id := range []string{"A", "a", "B", "b"} {
				record := map[string]any{}
				for key, value := range base {
					record[key] = value
				}
				record["id"] = id
				records = append(records, record)
				metadata = append(metadata, map[string]any{"id": id, "recordId": id, "version": 1, "createdAt": base["createdAt"], "tags": []any{}, "archived": false})
			}
			data["record"], data["metadata"] = records, metadata
			status, result := curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 {
				t.Fatalf("history fixture failed: %d %v", status, result)
			}
			for _, query := range []string{"", "q=Exact"} {
				status, full := searchRecords(t, origin, query)
				if status != 200 || len(full["records"].([]any)) != 4 {
					t.Fatalf("complete history unavailable: %d %v", status, full)
				}
				cursor := ""
				seen := map[string]bool{}
				for page, expected := range full["records"].([]any) {
					status, response := searchRecords(t, origin, query+"&limit=1&cursor="+url.QueryEscape(cursor))
					if status != 200 || len(response["records"].([]any)) != 1 {
						t.Fatalf("history page %d omitted evidence: %d %v", page, status, response)
					}
					id := response["records"].([]any)[0].(map[string]any)["id"].(string)
					if seen[id] || id != expected.(map[string]any)["id"] {
						t.Fatalf("history page %d duplicated or reordered evidence: %s", page, id)
					}
					seen[id] = true
					cursor, _ = response["nextCursor"].(string)
					if page < 3 && cursor == "" {
						t.Fatalf("history ended after %d records", page+1)
					}
				}
				if cursor != "" {
					t.Fatal("history continued beyond its last record")
				}
			}
		})
	}
}
