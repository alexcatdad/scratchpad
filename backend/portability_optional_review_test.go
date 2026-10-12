package backend_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func optionalArchiveFixture() map[string]any {
	archive := portableArchive()
	data := archive["data"].(map[string]any)
	first := data["record"].([]any)[0].(map[string]any)
	second := map[string]any{}
	for key, value := range first {
		second[key] = value
	}
	second["id"] = "rec_optional_second"
	data["record"] = append(data["record"].([]any), second)
	data["metadata"] = append(data["metadata"].([]any), map[string]any{"id": "rec_optional_second", "recordId": "rec_optional_second", "version": 1, "createdAt": first["createdAt"], "tags": []any{}, "archived": false})
	data["relationship"] = []any{map[string]any{"id": "rel_optional", "fromRecordId": "rec:portable", "toRecordId": "rec_optional_second", "type": "related_to", "status": "accepted", "authority": "explicit", "version": 1, "createdAt": first["createdAt"]}}
	return archive
}

func TestNativeImportPreservesHistoricalOptionalTextAndUnknownExtensions(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			archive := optionalArchiveFixture()
			data := archive["data"].(map[string]any)
			record := data["record"].([]any)[0].(map[string]any)
			record["confidence"], record["confidenceReason"] = "high", "  "+strings.Repeat("🦊", 50000)+"  "
			data["revision"].([]any)[0].(map[string]any)["reason"] = strings.Repeat("Historical reason ", 10000)
			data["relationship"].([]any)[0].(map[string]any)["note"] = strings.Repeat("🦊", 5000)
			data["mirror"].([]any)[0].(map[string]any)["path"] = ""
			data["mirror"].([]any)[0].(map[string]any)["error"] = strings.Repeat("🦊", 2000)
			for _, kind := range []string{"record", "revision", "relationship", "mirror"} {
				data[kind].([]any)[0].(map[string]any)["historicalExtension"] = map[string]any{"counter": json.Number("9007199254740993")}
			}
			status, result := curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 {
				t.Fatalf("valid historical optional fields rejected: %d %v", status, result)
			}
			status, detail := numericArchiveRequest(t, origin, "GET", "/records/rec:portable", "")
			if status != 200 {
				t.Fatal("imported detail unavailable")
			}
			for _, pair := range []struct{ kind, section, field string }{{"record", "record", "confidenceReason"}, {"revision", "revisions", "reason"}, {"relationship", "relationships", "note"}, {"mirror", "mirror", "error"}} {
				var returned map[string]any
				if pair.section == "record" || pair.section == "mirror" {
					returned = detail[pair.section].(map[string]any)
				} else {
					returned = detail[pair.section].([]any)[0].(map[string]any)
				}
				if returned[pair.field] != data[pair.kind].([]any)[0].(map[string]any)[pair.field] || returned["historicalExtension"].(map[string]any)["counter"] != json.Number("9007199254740993") {
					t.Fatalf("optional %s evidence changed", pair.kind)
				}
			}
			// Original legacy authority remains null; confidence omission on the
			// second record remains omission rather than an invented default.
			status, second := numericArchiveRequest(t, origin, "GET", "/records/rec_optional_second", "")
			if status != 200 || second["record"].(map[string]any)["authority"] != nil {
				t.Fatal("legacy authority changed")
			}
			if _, exists := second["record"].(map[string]any)["confidence"]; exists {
				t.Fatal("import invented historical confidence")
			}
		})
	}
}

func TestNativeImportRejectsUnsafeOptionalPresentationFieldsAtomically(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			_, before := curationRequest(t, origin, "POST", "/export", map[string]any{})
			cases := []struct {
				kind, field string
				value       any
			}{
				{"record", "confidence", map[string]any{"bad": "object"}},
				{"record", "confidenceReason", map[string]any{}},
				{"relationship", "note", map[string]any{}},
				{"record", "confidence", nil}, {"record", "confidence", 42}, {"record", "confidence", "unsupported"},
				{"record", "confidenceReason", nil}, {"record", "confidenceReason", []any{"bad"}}, {"record", "confidenceReason", strings.Repeat("🦊", 50001)},
				{"relationship", "note", nil}, {"relationship", "note", false}, {"relationship", "note", strings.Repeat("🦊", 5001)},
				{"revision", "reason", map[string]any{}}, {"revision", "reason", nil},
				{"mirror", "path", map[string]any{}}, {"mirror", "path", strings.Repeat("🦊", 2001)}, {"mirror", "error", nil}, {"mirror", "error", false},
			}
			for _, test := range cases {
				archive := optionalArchiveFixture()
				archive["data"].(map[string]any)[test.kind].([]any)[0].(map[string]any)[test.field] = test.value
				status, result := curationRequest(t, origin, "POST", "/import", archive)
				if status != 400 || result["error"].(map[string]any)["code"] != "IMPORT_INVALID" {
					t.Fatalf("unsafe optional %s.%s accepted: %d %v", test.kind, test.field, status, result)
				}
				status, after := curationRequest(t, origin, "POST", "/export", map[string]any{})
				if status != 200 || !reflect.DeepEqual(before["data"], after["data"]) {
					t.Fatal("invalid optional field partially imported knowledge")
				}
			}
		})
	}
}
