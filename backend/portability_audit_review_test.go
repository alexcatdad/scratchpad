package backend_test

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNativeImportRejectsUnsafeAuditDisplayFieldsAtomically(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			_, before := curationRequest(t, origin, "POST", "/export", map[string]any{})
			for _, field := range []string{"action", "entityType"} {
				for _, value := range []any{map[string]any{"unexpected": "object"}, []any{"unexpected"}, true, 42, nil} {
					archive := portableArchive()
					archive["data"].(map[string]any)["audit"].([]any)[0].(map[string]any)[field] = value
					status, result := curationRequest(t, origin, "POST", "/import", archive)
					if status != 400 || result["error"].(map[string]any)["code"] != "IMPORT_INVALID" {
						t.Fatalf("unsafe audit %s accepted: %d %v", field, status, result)
					}
					status, after := curationRequest(t, origin, "POST", "/export", map[string]any{})
					if status != 200 || !reflect.DeepEqual(before["data"], after["data"]) {
						t.Fatal("invalid audit partially imported knowledge")
					}
				}
			}
		})
	}
}

func TestNativeImportPreservesHistoricalAuditNamesAndExtensions(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			archive := portableArchive()
			audit := archive["data"].(map[string]any)["audit"].([]any)[0].(map[string]any)
			audit["action"] = "historical.unrecognized.event"
			audit["historicalExtension"] = map[string]any{"counter": json.Number("9007199254740993")}
			audit["actor"] = []any{"Unverified historical attribution"}
			missing := map[string]any{"id": "audit_missing_optional", "version": 1, "createdAt": audit["createdAt"], "entityId": "rec:portable"}
			unknown := map[string]any{"id": "audit_unknown_type", "version": 1, "createdAt": audit["createdAt"], "entityId": "rec:portable", "entityType": "historical.unrecognized.type", "action": ""}
			archive["data"].(map[string]any)["audit"] = []any{audit, missing, unknown}
			status, result := curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 {
				t.Fatalf("historical audit names rejected: %d %v", status, result)
			}
			status, detail := numericArchiveRequest(t, origin, "GET", "/records/rec:portable", "")
			if status != 200 {
				t.Fatal("record detail unavailable")
			}
			found := false
			for _, value := range detail["audit"].([]any) {
				event := value.(map[string]any)
				if event["id"] == audit["id"] {
					found = true
					if event["action"] != audit["action"] || !reflect.DeepEqual(event["actor"], audit["actor"]) || event["historicalExtension"].(map[string]any)["counter"] != json.Number("9007199254740993") {
						t.Fatal("historical audit data changed")
					}
				}
			}
			if !found {
				t.Fatal("historical audit omitted from record detail")
			}
			status, exported := numericArchiveRequest(t, origin, "POST", "/export", `{}`)
			if status != 200 {
				t.Fatal("archive unavailable")
			}
			for _, value := range exported["data"].(map[string]any)["audit"].([]any) {
				event := value.(map[string]any)
				if event["id"] == missing["id"] {
					if _, exists := event["action"]; exists {
						t.Fatal("import invented missing audit action")
					}
				}
				if event["id"] == unknown["id"] && event["entityType"] != unknown["entityType"] {
					t.Fatal("import changed unknown audit entity type")
				}
			}
		})
	}
}
