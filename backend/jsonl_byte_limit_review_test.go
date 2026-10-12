package backend_test

import (
	"strings"
	"testing"
)

func TestLegacyJSONLByteLimitRejectsMultibyteTextAtomically(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, response := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "JSONL byte budget"}, restoredToken)
			if status != 200 {
				t.Fatal("project fixture failed")
			}
			project := response["project"].(map[string]any)["id"].(string)
			valid := `{"id":"rec_utf8_limit","type":"finding","date":"2025-01-01","finding":"Preserve byte-limit atomicity."}`
			text := valid + "\n" + strings.Repeat("界", 32*1024*1024/3+1)
			status, _ = curationRequest(t, origin, "POST", "/import", map[string]any{"format": "jsonl", "projectId": project, "jsonl": text})
			if status != 400 {
				t.Fatalf("JSONL text exceeding 32MiB UTF8 bytes was admitted: HTTP%d bytes=%d", status, len(text))
			}
			status, listing := searchRecords(t, origin, "projectId="+project)
			if status != 200 || len(listing["records"].([]any)) != 0 {
				t.Fatal("oversized JSONL partially committed its valid first line")
			}
			status, imported := curationRequest(t, origin, "POST", "/import", map[string]any{"format": "jsonl", "projectId": project, "jsonl": valid + "\n" + `{"id":"rec_multibyte_ok","type":"finding","finding":"界é😀"}`})
			if status != 200 || imported["imported"] != float64(2) {
				t.Fatal("in-budget multilingual JSONL import changed")
			}
		})
	}
}
