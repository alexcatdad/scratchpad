package backend_test

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNativeImportRejectsVersionsOutsideExactSharedPersistenceRange(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			_, before := curationRequest(t, origin, "POST", "/export", map[string]any{})
			for _, version := range []string{"2147483648", "9007199254740993", "1.00000000000000000001", "2147483647.0000000001"} {
				t.Run(version, func(t *testing.T) {
					archive := portableArchive()
					archive["data"].(map[string]any)["record"].([]any)[0].(map[string]any)["version"] = json.Number(version)
					status, result := curationRequest(t, origin, "POST", "/import", archive)
					if status != 400 || result["error"].(map[string]any)["code"] != "IMPORT_INVALID" {
						t.Fatalf("nonportable version accepted: %d %v", status, result)
					}
					status, after := curationRequest(t, origin, "POST", "/export", map[string]any{})
					if status != 200 || !reflect.DeepEqual(before["data"], after["data"]) {
						t.Fatal("invalid version partially imported knowledge")
					}
				})
			}
			archive := portableArchive()
			archive["data"].(map[string]any)["record"].([]any)[0].(map[string]any)["version"] = json.Number("2147483647")
			status, result := curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 {
				t.Fatalf("maximum shared version rejected: %d %v", status, result)
			}
			status, result = curationRequest(t, origin, "GET", "/records/rec:portable", nil)
			if status != 200 || result["record"].(map[string]any)["version"] != float64(2147483647) {
				t.Fatalf("historical version changed: %d %v", status, result)
			}
		})
	}
}
