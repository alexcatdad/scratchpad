package backend_test

import (
	"strings"
	"testing"
)

func TestLegacyJSONLUTF16LimitPreservesMultibyteTextAndAtomicity(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, response := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "JSONL UTF16 budget"}, restoredToken)
			if status != 200 {
				t.Fatal("project fixture failed")
			}
			project := response["project"].(map[string]any)["id"].(string)
			valid := `{"id":"rec_utf16_limit","type":"finding","date":"2025-01-01","finding":"界😀"}`
			// BMP text uses one UTF16 unit and supplementary text uses two.
			// U+3000 padding is an ignored blank line, so a large accepted request
			// persists only one tiny useful record, never a giant source fixture.
			const limit = 32 * 1024 * 1024
			prefixUnits := len(valid) - len("界😀") + 3 + 1
			padding := strings.Repeat("\u3000", 1024*1024) + strings.Repeat(" ", limit-prefixUnits-1024*1024)
			text := valid + "\n" + padding
			status, imported := curationRequest(t, origin, "POST", "/import", map[string]any{"format": "jsonl", "projectId": project, "jsonl": text})
			if status != 200 || imported["imported"] != float64(1) || imported["skipped"] != float64(0) {
				t.Fatalf("valid JSONL at 32 Mi UTF16 units rejected despite fitting 64 MiB request: HTTP%d bytes=%d %v", status, len(text), imported)
			}
			status, detail := curationRequest(t, origin, "GET", "/records/rec_utf16_limit", nil)
			if status != 200 || detail["record"].(map[string]any)["payload"].(map[string]any)["finding"] != "界😀" {
				t.Fatal("in-budget non-ASCII evidence was not preserved")
			}
			oversized := strings.Replace(text, "rec_utf16_limit", "rec_utf16_other", 1) + " "
			status, _ = curationRequest(t, origin, "POST", "/import", map[string]any{"format": "jsonl", "projectId": project, "jsonl": oversized})
			if status != 400 {
				t.Fatalf("JSONL above 32 Mi UTF16 units admitted: HTTP%d", status)
			}
			status, listing := searchRecords(t, origin, "projectId="+project)
			if status != 200 || len(listing["records"].([]any)) != 1 || listing["records"].([]any)[0].(map[string]any)["id"] != "rec_utf16_limit" {
				t.Fatal("over-unit-limit JSONL partially committed its valid first line")
			}
			// The independent outer byte cap still rejects an otherwise in-budget
			// UTF16 string. Its first valid record cannot be partially committed.
			outer := strings.Replace(valid, "rec_utf16_limit", "rec_utf16_outer", 1) + "\n" + strings.Repeat("\u3000", 64*1024*1024/3+1)
			status, _ = curationRequest(t, origin, "POST", "/import", map[string]any{"format": "jsonl", "projectId": project, "jsonl": outer})
			if status != 413 {
				t.Fatalf("outer 64 MiB request limit was weakened: HTTP%d", status)
			}
			status, listing = searchRecords(t, origin, "projectId="+project)
			if status != 200 || len(listing["records"].([]any)) != 1 {
				t.Fatal("outer-limit rejection changed knowledge")
			}
		})
	}
}
