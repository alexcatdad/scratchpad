package backend_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const exactArchiveRecordID = "rec_88319c58-3c43-4c6c-ab57-20ac68fd0475"
const exactNumberArchive = `{"format":"scratchpad","version":1,"data":{"project":[{"id":"proj_8a737eb5-1579-4329-a8ac-857603107f21","version":1,"createdAt":"2026-10-01T00:00:00Z","name":"Exact numbers","kind":"normal","settings":{"enabledRecordTypes":["finding"],"repoMirroring":{"enabled":false,"recordTypes":["finding"]},"crossProjectAnalysis":false,"aiProcessing":false}}],"record":[{"id":"rec_88319c58-3c43-4c6c-ab57-20ac68fd0475","version":1,"createdAt":"2026-10-01T00:00:00Z","recordedAt":"2026-10-01T00:00:00Z","projectId":"proj_8a737eb5-1579-4329-a8ac-857603107f21","type":"finding","authority":"observed","title":"Exact source evidence","content":"finding: Preserve the original numerical evidence.","payload":{"finding":"Preserve the original numerical evidence.","counter":9007199254740993,"fraction":1.00000000000000000001},"historicalExtension":{"counter":9007199254740993}}],"metadata":[{"id":"rec_88319c58-3c43-4c6c-ab57-20ac68fd0475","recordId":"rec_88319c58-3c43-4c6c-ab57-20ac68fd0475","version":1,"createdAt":"2026-10-01T00:00:00Z","tags":[],"archived":false}]}}`

func numericArchiveRequest(t *testing.T, origin, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, origin+"/api/v1"+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+restoredToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var result map[string]any
	decoder := json.NewDecoder(res.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("invalid public response: HTTP %d, %v", res.StatusCode, err)
	}
	return res.StatusCode, result
}

func TestNativeArchivePreservesExactFlexibleJSONNumbers(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, result := numericArchiveRequest(t, origin, "POST", "/import", exactNumberArchive)
			if status != 200 {
				t.Fatalf("native archive rejected: %d %+v", status, result)
			}
			status, result = numericArchiveRequest(t, origin, "GET", "/records/"+exactArchiveRecordID, "")
			if status != 200 {
				t.Fatal("imported source unavailable")
			}
			record := result["record"].(map[string]any)
			payload := record["payload"].(map[string]any)
			if payload["counter"] != json.Number("9007199254740993") || payload["fraction"] != json.Number("1.00000000000000000001") || record["historicalExtension"].(map[string]any)["counter"] != json.Number("9007199254740993") {
				t.Fatalf("native import changed immutable numerical evidence: %v", record)
			}
			for _, view := range []struct{ path, section string }{
				{"/records?projectId=proj_8a737eb5-1579-4329-a8ac-857603107f21", "records"},
				{"/projects/proj_8a737eb5-1579-4329-a8ac-857603107f21/context", "openFindings"},
			} {
				status, result = numericArchiveRequest(t, origin, "GET", view.path, "")
				if status != 200 || len(result[view.section].([]any)) != 1 {
					t.Fatalf("immutable record view unavailable: %s", view.path)
				}
				selected := result[view.section].([]any)[0].(map[string]any)
				if selected["payload"].(map[string]any)["counter"] != json.Number("9007199254740993") || selected["payload"].(map[string]any)["fraction"] != json.Number("1.00000000000000000001") || selected["historicalExtension"].(map[string]any)["counter"] != json.Number("9007199254740993") {
					t.Fatalf("record view changed numerical evidence: %s", view.path)
				}
			}
			request, _ := http.NewRequest("POST", origin+"/api/v1/export", strings.NewReader(`{}`))
			request.Header.Set("Authorization", "Bearer "+restoredToken)
			export, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(export.Body)
			export.Body.Close()
			if err != nil || export.StatusCode != 200 || !strings.Contains(string(raw), "9007199254740993") || !strings.Contains(string(raw), "1.00000000000000000001") {
				t.Fatal("native export lost exact numerical evidence")
			}
			status, result = numericArchiveRequest(t, origin, "POST", "/import", string(raw))
			if status != 200 || result["imported"] != json.Number("0") {
				t.Fatalf("exact export replay conflicted: %d %+v", status, result)
			}
		})
	}
}

func TestNativeArchiveReplayComparesExactNumericalMeaning(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, response := numericArchiveRequest(t, origin, "POST", "/import", exactNumberArchive)
			if status != 200 {
				t.Fatalf("original archive failed: %d %+v", status, response)
			}
			for _, archive := range []string{
				strings.ReplaceAll(exactNumberArchive, "9007199254740993", "900719925474099300e-2"),
				strings.ReplaceAll(exactNumberArchive, "1.00000000000000000001", "100000000000000000001e-20"),
				strings.ReplaceAll(exactNumberArchive, `"version":1`, `"version":1.0`),
			} {
				status, response = numericArchiveRequest(t, origin, "POST", "/import", archive)
				if status != 200 || response["imported"] != json.Number("0") || response["skipped"] != json.Number("3") {
					t.Fatalf("equivalent numerical representation changed identity: %d %+v", status, response)
				}
			}
			for _, archive := range []string{
				strings.ReplaceAll(exactNumberArchive, "9007199254740993", "9007199254740992"),
				strings.ReplaceAll(exactNumberArchive, "1.00000000000000000001", "1.0"),
			} {
				status, response = numericArchiveRequest(t, origin, "POST", "/import", archive)
				if status != 409 || response["error"].(map[string]any)["code"] != "CONFLICT" {
					t.Fatalf("distinct numerical evidence silently reused identity: %d %+v", status, response)
				}
			}
			status, response = numericArchiveRequest(t, origin, "GET", "/records/"+exactArchiveRecordID, "")
			if status != 200 || response["record"].(map[string]any)["payload"].(map[string]any)["counter"] != json.Number("9007199254740993") {
				t.Fatal("conflicting archive changed the original capture")
			}
		})
	}
}

func TestNativeArchiveReplayKeepsExtremeDecimalExponentsBounded(t *testing.T) {
	// SQLite preserves the original JSON token; PostgreSQL's JSONB numeric
	// representation has a smaller exponent range and rejects this source input.
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	original := strings.ReplaceAll(exactNumberArchive, "9007199254740993", "1e-1000000000")
	status, result := numericArchiveRequest(t, origin, "POST", "/import", original)
	if status != 200 {
		t.Fatalf("extreme decimal source rejected: %d %+v", status, result)
	}
	status, result = numericArchiveRequest(t, origin, "POST", "/import", strings.ReplaceAll(original, "1e-1000000000", "10e-1000000001"))
	if status != 200 || result["imported"] != json.Number("0") {
		t.Fatal("equivalent extreme decimal expanded or changed identity")
	}
	status, result = numericArchiveRequest(t, origin, "POST", "/import", strings.ReplaceAll(original, "1e-1000000000", "2e-1000000000"))
	if status != 409 || result["error"].(map[string]any)["code"] != "CONFLICT" {
		t.Fatal("distinct extreme decimal silently became the same zero value")
	}
}
