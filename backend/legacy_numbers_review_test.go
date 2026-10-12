package backend_test

import (
	"encoding/json"
	"testing"
)

func TestLegacyImportPreservesExactNumericalEvidenceAndReplay(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			projectID := jobProject(t, origin, "Legacy numerical evidence", false, false)
			line := `{"id":"legacy_number","type":"finding","finding":"Keep exact historical evidence","payload":{"counter":9007199254740993,"fraction":1.00000000000000000001},"extension":{"counter":9007199254740993}}`
			input := map[string]any{"format": "jsonl", "projectId": projectID, "jsonl": line}
			status, result := curationRequest(t, origin, "POST", "/import", input)
			if status != 200 || result["imported"] != float64(1) {
				t.Fatalf("legacy import failed: %d %v", status, result)
			}
			status, detail := numericArchiveRequest(t, origin, "GET", "/records/legacy_number", "")
			if status != 200 {
				t.Fatalf("source unavailable: %d", status)
			}
			payload := detail["record"].(map[string]any)["payload"].(map[string]any)
			original := payload["legacyOriginal"].(map[string]any)
			if payload["counter"] != json.Number("9007199254740993") || payload["fraction"] != json.Number("1.00000000000000000001") || original["extension"].(map[string]any)["counter"] != json.Number("9007199254740993") {
				t.Fatalf("legacy import rounded immutable evidence: %v", payload)
			}
			status, result = curationRequest(t, origin, "POST", "/import", input)
			if status != 200 || result["records"].([]any)[0].(map[string]any)["status"] != "skipped" {
				t.Fatalf("exact replay conflicted: %d %v", status, result)
			}
			input["jsonl"] = `{"id":"legacy_number","type":"finding","finding":"Keep exact historical evidence","payload":{"counter":9007199254740992,"fraction":1.00000000000000000001},"extension":{"counter":9007199254740993}}`
			status, result = curationRequest(t, origin, "POST", "/import", input)
			if status != 200 || result["records"].([]any)[0].(map[string]any)["status"] != "conflict" {
				t.Fatalf("distinct numerical evidence did not conflict: %d %v", status, result)
			}
		})
	}
}
