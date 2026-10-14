package backend_test

import "testing"

func TestPostSearchZeroLimitPreservesDefaultPage(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			project := jobProject(t, origin, "Default POST search", false, false)
			for range 31 {
				curationCapture(t, origin, project, "Default page evidence")
			}
			for _, body := range []map[string]any{
				{"projectIds": []string{project}},
				{"projectIds": []string{project}, "limit": 0},
			} {
				status, result := curationRequest(t, origin, "POST", "/search", body)
				if status != 200 {
					t.Fatalf("valid default POST search returned HTTP %d", status)
				}
				if len(result["records"].([]any)) != 30 || result["nextCursor"] == nil {
					t.Fatal("zero or omitted POST limit did not retain the default page")
				}
			}
			status, result := curationRequest(t, origin, "POST", "/search", map[string]any{"projectIds": []string{project}, "limit": 1})
			if status != 200 || len(result["records"].([]any)) != 1 {
				t.Fatal("explicit positive POST limit was ignored")
			}
			for _, value := range []any{-1, 0.5, "0", nil, false, map[string]any{}} {
				status, _ := curationRequest(t, origin, "POST", "/search", map[string]any{"limit": value})
				if status != 400 {
					t.Fatalf("invalid POST limit %#v returned HTTP %d", value, status)
				}
			}
			status, _ = curationRequest(t, origin, "GET", "/records?limit=0", nil)
			if status != 400 {
				t.Fatal("GET zero limit unexpectedly became valid")
			}
		})
	}
}
