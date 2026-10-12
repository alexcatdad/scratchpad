package backend_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNativeImportRejectsMalformedEvidenceAtomically(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			_, before := curationRequest(t, origin, "POST", "/export", map[string]any{})
			cases := []struct {
				field string
				value any
			}{
				{"reference", map[string]any{"unexpected": "object"}},
				{"reference", nil}, {"reference", false}, {"reference", []any{"unexpected"}}, {"reference", ""}, {"reference", strings.Repeat("🦊", 2001)},
				{"kind", "unsupported"}, {"kind", nil}, {"kind", 42}, {"kind", map[string]any{}},
				{"revision", nil}, {"revision", false}, {"revision", strings.Repeat("🦊", 251)},
				{"description", nil}, {"description", map[string]any{}}, {"description", strings.Repeat("🦊", 5001)},
			}
			for _, test := range cases {
				archive := portableArchive()
				archive["data"].(map[string]any)["evidence"].([]any)[0].(map[string]any)[test.field] = test.value
				status, result := curationRequest(t, origin, "POST", "/import", archive)
				if status != 400 || result["error"].(map[string]any)["code"] != "IMPORT_INVALID" {
					t.Fatalf("invalid evidence %s accepted: %d %v", test.field, status, result)
				}
				status, after := curationRequest(t, origin, "POST", "/export", map[string]any{})
				if status != 200 || !reflect.DeepEqual(before["data"], after["data"]) {
					t.Fatal("invalid evidence partially imported knowledge")
				}
			}
		})
	}
}

func TestNativeImportPreservesAllSupportedEvidenceKindsAndExtensions(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			archive := portableArchive()
			data := archive["data"].(map[string]any)
			base := data["evidence"].([]any)[0].(map[string]any)
			entries := []any{}
			for i, kind := range []string{"url", "git_commit", "pull_request", "issue", "file", "conversation", "test", "deployment", "other"} {
				entry := map[string]any{}
				for key, value := range base {
					entry[key] = value
				}
				entry["id"], entry["kind"], entry["reference"] = "evi_"+kind, kind, "Historical "+kind
				entry["historicalExtension"] = map[string]any{"counter": json.Number("9007199254740993")}
				if i == 0 {
					entry["reference"], entry["revision"], entry["description"] = strings.Repeat("🦊", 2000), strings.Repeat("😀", 250), strings.Repeat("x", 10000)
				}
				if i == 1 {
					entry["revision"], entry["description"] = "", ""
				}
				entries = append(entries, entry)
			}
			data["evidence"] = entries
			status, result := curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 {
				t.Fatalf("supported evidence rejected: %d %v", status, result)
			}
			status, detail := numericArchiveRequest(t, origin, "GET", "/records/rec:portable", "")
			if status != 200 || len(detail["evidence"].([]any)) != 9 {
				t.Fatalf("restored evidence unavailable: %d %v", status, detail)
			}
			seen := map[any]bool{}
			for _, value := range detail["evidence"].([]any) {
				entry := value.(map[string]any)
				seen[entry["kind"]] = true
				if entry["historicalExtension"].(map[string]any)["counter"] != json.Number("9007199254740993") {
					t.Fatal("evidence extension changed")
				}
				for _, expected := range entries {
					original := expected.(map[string]any)
					if original["id"] == entry["id"] {
						for _, field := range []string{"reference", "revision", "description"} {
							if original[field] != entry[field] {
								t.Fatalf("evidence field %s changed", field)
							}
						}
					}
				}
			}
			if len(seen) != 9 {
				t.Fatal("supported evidence kind lost")
			}
		})
	}
}
