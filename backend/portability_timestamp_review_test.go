package backend_test

import (
	"reflect"
	"testing"
)

func TestNativeImportValidatesOptionalHappenedAtAtomically(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, before := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("initial export failed")
			}
			for _, value := range []any{"not-a-date", "2026-02-30T00:00Z", nil, true, 123, map[string]any{}} {
				archive := portableArchive()
				archive["data"].(map[string]any)["record"].([]any)[0].(map[string]any)["happenedAt"] = value
				status, result := curationRequest(t, origin, "POST", "/import", archive)
				if status != 400 || result["error"].(map[string]any)["code"] != "IMPORT_INVALID" {
					t.Fatalf("invalid happenedAt %#v did not return IMPORT_INVALID: HTTP %d", value, status)
				}
				status, after := curationRequest(t, origin, "POST", "/export", map[string]any{})
				if status != 200 || !reflect.DeepEqual(before["data"], after["data"]) {
					t.Fatal("invalid timestamp partially modified imported state")
				}
			}
		})
		for _, value := range []string{"2026-10-01T00:00Z", "2026-10-01T00:00:01Z", "2026-10-01T00:00:01.123456789123Z"} {
			t.Run(engine+"/"+value, func(t *testing.T) {
				path, databaseURL := portableInstance(t, engine)
				origin := startServer(t, path, databaseURL)
				archive := portableArchive()
				archive["data"].(map[string]any)["record"].([]any)[0].(map[string]any)["happenedAt"] = value
				status, result := curationRequest(t, origin, "POST", "/import", archive)
				if status != 200 {
					t.Fatalf("valid happenedAt rejected: HTTP %d %+v", status, result)
				}
				status, result = curationRequest(t, origin, "GET", "/records/rec:portable", nil)
				if status != 200 || result["record"].(map[string]any)["happenedAt"] != value {
					t.Fatal("import rewrote source timestamp")
				}
			})
		}
	}
}
