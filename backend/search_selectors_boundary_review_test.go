package backend_test

import (
	"fmt"
	"net/url"
	"testing"
)

func TestSearchRepeatedSelectorsPreserveMatchingRecordsWithoutBindFailures(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			project := jobProject(t, origin, "Repeated search selectors", false, false)
			status, captured := captureRequest(t, origin, project, "", map[string]any{"type": "finding", "title": "SelectorMatchingFinding", "authority": "observed", "payload": map[string]any{"finding": "SelectorMatchingFinding remains searchable."}})
			if status != 201 {
				t.Fatal("finding capture failed")
			}
			record := captured["record"].(map[string]any)
			projects, types := make([]string, 33000), make([]string, 33000)
			for i := range projects {
				projects[i], types[i] = project, "finding"
			}
			status, response := curationRequest(t, origin, "POST", "/search", map[string]any{"query": "SelectorMatchingFinding", "projectIds": projects, "types": types})
			if status != 200 {
				t.Fatalf("valid repeated selector sets failed: HTTP%d %v", status, response["error"])
			}
			rows := response["records"].([]any)
			if len(rows) != 1 || rows[0].(map[string]any)["id"] != record["id"] {
				t.Fatal("repeated selectors changed the matching record set")
			}
			query := url.Values{}
			for range 512 {
				query.Add("projectIds", project)
				query.Add("types", "finding")
			}
			status, response = searchRecords(t, origin, query.Encode())
			if status != 200 || len(response["records"].([]any)) != 1 || response["records"].([]any)[0].(map[string]any)["id"] != record["id"] {
				t.Fatal("GET selector normalization changed the matching record set")
			}
		})
	}
}

func TestSearchRejectsCombinedDistinctSelectorBudgetBeforeDatabaseQuery(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			projects, types := make([]string, 16350), make([]string, 16351)
			for i := range projects {
				projects[i] = fmt.Sprintf("proj_missing_%05d", i)
			}
			for i := range types {
				types[i] = fmt.Sprintf("historical_type_%05d", i)
			}
			status, response := curationRequest(t, origin, "POST", "/search", map[string]any{"projectIds": projects, "types": types})
			if status != 400 || response["error"].(map[string]any)["code"] != "VALIDATION_FAILED" {
				t.Fatalf("32701 distinct selectors were not a fixed validation error: HTTP%d", status)
			}
			for _, body := range []map[string]any{{"projectIds": nil}, {"types": "finding"}, {"projectIds": []any{"proj_missing", false}}, {"types": []any{"finding", 7}}} {
				status, response = curationRequest(t, origin, "POST", "/search", body)
				if status != 400 || response["error"].(map[string]any)["code"] != "VALIDATION_FAILED" {
					t.Fatal("malformed selector array validation changed")
				}
			}
		})
	}
}
