package backend_test

import (
	"net/url"
	"testing"
)

// Inventory assertions follow public continuation rather than assuming the
// default page contains all retained operational history.
func allAIJobs(t *testing.T, origin string) []any {
	t.Helper()
	jobs := []any{}
	seenIDs, seenCursors := map[string]bool{}, map[string]bool{}
	route := "/ai/jobs"
	for {
		status, response := curationRequest(t, origin, "GET", route, nil)
		if status != 200 {
			t.Fatalf("job history unavailable: HTTP %d", status)
		}
		page, ok := response["jobs"].([]any)
		if !ok {
			t.Fatal("job page is invalid")
		}
		for _, item := range page {
			id := item.(map[string]any)["id"].(string)
			if seenIDs[id] {
				t.Fatalf("job continuation duplicated %s", id)
			}
			seenIDs[id] = true
			jobs = append(jobs, item)
		}
		cursor, _ := response["nextCursor"].(string)
		if cursor == "" {
			return jobs
		}
		if seenCursors[cursor] {
			t.Fatal("job continuation did not advance")
		}
		seenCursors[cursor] = true
		route = "/ai/jobs?cursor=" + url.QueryEscape(cursor)
	}
}
