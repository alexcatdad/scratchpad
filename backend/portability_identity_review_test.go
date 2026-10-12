package backend_test

import (
	"reflect"
	"testing"
)

func TestNativeImportRejectsUnaddressableGlobalSettings(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			_, before := curationRequest(t, origin, "POST", "/export", map[string]any{})
			archive := portableArchive()
			data := archive["data"].(map[string]any)
			settings := map[string]any{"id": "another-global", "version": 1, "createdAt": "2026-10-01T00:00:00.000Z", "defaultProjectSettings": data["project"].([]any)[0].(map[string]any)["settings"]}
			data["settings"] = []any{settings}
			status, result := curationRequest(t, origin, "POST", "/import", archive)
			if status != 400 || result["error"].(map[string]any)["code"] != "IMPORT_INVALID" {
				t.Fatalf("unaddressable settings accepted: %d %v", status, result)
			}
			status, after := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 || !reflect.DeepEqual(before["data"], after["data"]) {
				t.Fatal("rejected settings partially imported knowledge")
			}
			settings["id"] = "global"
			status, result = curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 {
				t.Fatalf("valid global settings rejected: %d %v", status, result)
			}
			status, result = curationRequest(t, origin, "GET", "/settings", nil)
			if status != 200 || !reflect.DeepEqual(result["settings"].(map[string]any)["defaultProjectSettings"], settings["defaultProjectSettings"]) {
				t.Fatalf("restored settings inaccessible: %d %v", status, result)
			}
		})
	}
}

func TestNativeImportRejectsUnaddressableRecordMirror(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			_, before := curationRequest(t, origin, "POST", "/export", map[string]any{})
			archive := portableArchive()
			mirror := archive["data"].(map[string]any)["mirror"].([]any)[0].(map[string]any)
			mirror["id"] = "mirror_other"
			status, result := curationRequest(t, origin, "POST", "/import", archive)
			if status != 400 || result["error"].(map[string]any)["code"] != "IMPORT_INVALID" {
				t.Fatalf("unaddressable mirror accepted: %d %v", status, result)
			}
			status, after := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 || !reflect.DeepEqual(before["data"], after["data"]) {
				t.Fatal("rejected mirror partially imported knowledge")
			}
			mirror["id"] = mirror["recordId"]
			status, result = curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 {
				t.Fatalf("valid record mirror rejected: %d %v", status, result)
			}
			status, detail := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
			if status != 200 || !reflect.DeepEqual(detail["mirror"], mirror) {
				t.Fatalf("restored mirror inaccessible: %d %v", status, detail)
			}
		})
	}
}
