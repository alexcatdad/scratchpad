package backend_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestNativeArchivesPreserveHistoricalTagsBeyondMutationLimits(t *testing.T) {
	many := []any{}
	for i := 0; i < 101; i++ {
		many = append(many, fmt.Sprintf("historical-tag-%03d", i))
	}
	for _, engine := range []string{"sqlite", "postgres"} {
		for _, check := range []struct {
			name string
			tags []any
		}{{"101-tags", many}, {"102-utf16-unit-tag", []any{strings.Repeat("🦊", 51)}}} {
			t.Run(engine+"/"+check.name, func(t *testing.T) {
				path, databaseURL := portableInstance(t, engine)
				origin := startServer(t, path, databaseURL)
				archive := portableArchive()
				metadata := archive["data"].(map[string]any)["metadata"].([]any)[0].(map[string]any)
				metadata["tags"] = check.tags
				status, result := curationRequest(t, origin, "POST", "/import", archive)
				if status != 200 || result["imported"] != float64(9) {
					t.Fatalf("valid historical native tags rejected: HTTP %d %+v", status, result)
				}
				status, detail := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
				if status != 200 || !reflect.DeepEqual(detail["metadata"], metadata) {
					t.Fatal("native import truncated or rewrote historical tags")
				}
				status, _ = curationRequest(t, origin, "PATCH", "/records/rec:portable/metadata", map[string]any{"expectedVersion": 1, "tags": check.tags})
				if status != 400 {
					t.Fatalf("editing limits were relaxed by native import: HTTP %d", status)
				}
				status, unchanged := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
				if status != 200 || !reflect.DeepEqual(unchanged["metadata"], metadata) {
					t.Fatal("rejected oversized edit changed historical metadata/version")
				}
				status, exported := curationRequest(t, origin, "POST", "/export", map[string]any{})
				if status != 200 || !reflect.DeepEqual(exported["data"].(map[string]any)["metadata"], []any{metadata}) {
					t.Fatal("native export lost exact historical tags")
				}
				status, replay := curationRequest(t, origin, "POST", "/import", exported)
				if status != 200 || replay["imported"] != float64(0) {
					t.Fatalf("valid historical tag archive cannot replay: HTTP %d %+v", status, replay)
				}
			})
		}
	}
}
