package backend_test

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func searchRecords(t *testing.T, origin, query string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", origin+"/api/v1/records?"+query, nil)
	req.Header.Set("Authorization", "Bearer "+restoredToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	if json.NewDecoder(res.Body).Decode(&body) != nil {
		t.Fatalf("non-JSON search response: %d", res.StatusCode)
	}
	return res.StatusCode, body
}

func TestLexicalSearchDecodesOnlyNativeMatchingRecords(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			projectID := jobProject(t, origin, "Focused lexical records", false, false)
			wanted := curationCapture(t, origin, projectID, "FOCUSED_MATCH_WITNESS")
			unrelated := curationCapture(t, origin, projectID, "UNRELATED_CORRUPTION_WITNESS")
			driver, dsn := "sqlite", path
			if engine == "postgres" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			// A fault input makes decoding unrelated history observably incorrect.
			if _, err = db.Exec(`UPDATE entities SET data='[]' WHERE kind='record' AND id=$1`, unrelated["id"]); err != nil {
				t.Fatal(err)
			}
			if engine == "sqlite" {
				// FTS5 has no uniqueness constraint on its stored record reference.
				if _, err = db.Exec(`INSERT INTO record_search SELECT record_id,title,content FROM record_search WHERE record_id=$1`, wanted["id"]); err != nil {
					t.Fatal(err)
				}
			}
			status, response := searchRecords(t, origin, "q=FOCUSED_MATCH_WITNESS")
			if status != 200 || len(response["records"].([]any)) != 1 || response["records"].([]any)[0].(map[string]any)["id"] != wanted["id"] {
				t.Fatalf("targeted search decoded unrelated record history: %d %v", status, response)
			}
			status, _ = searchRecords(t, origin, "q=UNRELATED_CORRUPTION_WITNESS")
			if status != 503 {
				t.Fatal("targeted search silently ignored corruption in a matching record")
			}
		})
	}
}
func TestLexicalSearchAndPaginationReadNewCaptures(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Search"}, restoredToken)
			id := project["project"].(map[string]any)["id"].(string)
			created := map[string]bool{}
			for _, title := range []string{"First LexicalCapture", "Second LexicalCapture", "Third LexicalCapture"} {
				_, body := captureRequest(t, origin, id, "", map[string]any{"type": "finding", "title": title, "authority": "observed", "payload": map[string]any{"finding": "Atomic capture remains searchable."}})
				created[body["record"].(map[string]any)["id"].(string)] = true
			}
			query := "projectId=" + id + "&q=LexicalCapture&limit=1"
			seen := map[string]bool{}
			for range 3 {
				status, body := searchRecords(t, origin, query)
				if status != 200 {
					t.Fatalf("search unavailable: %d", status)
				}
				records := body["records"].([]any)
				if len(records) != 1 {
					t.Fatal("search page has incorrect size")
				}
				record := records[0].(map[string]any)
				next := record["id"].(string)
				if !created[next] || seen[next] || record["metadata"] == nil {
					t.Fatal("search page lost or duplicated a capture")
				}
				seen[next] = true
				cursor, _ := body["nextCursor"].(string)
				if len(seen) < 3 && cursor == "" {
					t.Fatal("search omitted its continuation cursor")
				}
				query = "projectId=" + id + "&q=LexicalCapture&limit=1&cursor=" + url.QueryEscape(cursor)
			}
			status, body := searchRecords(t, origin, "projectId="+id+"&q=%22%29%20OR%20injection_literal&limit=100")
			if status != 200 || len(body["records"].([]any)) != 0 {
				t.Fatal("lexical query punctuation changed query semantics")
			}
		})
	}
}

func TestSearchPreservesCuratedAndTemporalFilters(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			rows := []struct {
				kind, id string
				value    map[string]any
			}{
				{"record", "rec_filter_active", map[string]any{"projectId": "proj_filter", "type": "finding", "authority": "observed", "confidence": "high", "happenedAt": "2026-10-01T12:00:00.0000000002Z", "actor": map[string]any{"client": "Synthetic agent"}, "gitContext": map[string]any{"branch": "feature", "rootPathHint": "/synthetic/repo"}}},
				{"record", "rec_filter_old", map[string]any{"projectId": "proj_filter", "type": "decision", "authority": "explicit", "confidence": "low", "happenedAt": "2026-10-01T12:00:00.0000000001Z"}},
				{"metadata", "rec_filter_active", map[string]any{"tags": []string{"curated"}, "archived": false}},
				{"metadata", "rec_filter_old", map[string]any{"tags": []string{}, "archived": false}},
				{"relationship", "rel_filter", map[string]any{"fromRecordId": "rec_filter_active", "toRecordId": "rec_filter_old", "type": "replaces", "status": "accepted"}},
			}
			for _, row := range rows {
				row.value["id"], row.value["version"], row.value["createdAt"], row.value["recordedAt"] = row.id, 1, "2026-10-02T00:00:00.000Z", "2026-10-02T00:00:00.000Z"
				data, _ := json.Marshal(row.value)
				if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES($1,$2,$3,1,'2026-10-02T00:00:00.000Z')`, row.kind, row.id, string(data)); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			db.Close()
			origin := startServer(t, path, databaseURL)
			for _, query := range []string{"type=finding&authority=observed&confidence=high", "tag=curated&branch=feature", "source=Synthetic+agent&gitPath=%2Fsynthetic%2Frepo", "status=active&archived=false", "from=2026-10-01T12%3A00%3A00.00000000015Z&to=2026-10-01T12%3A00%3A00.0000000002Z", "projectIds=proj_filter&types=finding"} {
				status, body := searchRecords(t, origin, "projectId=proj_filter&"+query)
				if status != 200 || len(body["records"].([]any)) != 1 || body["records"].([]any)[0].(map[string]any)["id"] != "rec_filter_active" {
					t.Fatalf("filter contract changed: %s", query)
				}
			}
			status, body := searchRecords(t, origin, "projectId=proj_filter&status=superseded")
			if status != 200 || len(body["records"].([]any)) != 1 || body["records"].([]any)[0].(map[string]any)["id"] != "rec_filter_old" {
				t.Fatal("accepted replacement did not define lifecycle")
			}
			status, body = searchRecords(t, origin, "projectId=proj_filter&relationship=replaces&relatedTo=rec_filter_old")
			if status != 200 || len(body["records"].([]any)) != 2 {
				t.Fatal("relationship filter lost an endpoint")
			}
		})
	}
}

func TestNewCaptureUsesCanonicalUUIDIdentity(t *testing.T) {
	path, _, _ := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Identity"}, restoredToken)
	id := project["project"].(map[string]any)["id"].(string)
	_, body := captureRequest(t, origin, id, "", map[string]any{"type": "finding", "title": "UUID", "authority": "observed", "payload": map[string]any{"finding": "Keep the established identifier strategy."}})
	if !regexp.MustCompile(`^rec_[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(body["record"].(map[string]any)["id"].(string)) {
		t.Fatal("new record identity differs from the established UUID namespace")
	}
}

func TestSearchRejectsMalformedCursorAndBounds(t *testing.T) {
	path, _, _ := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	bad := base64.RawURLEncoding.EncodeToString([]byte(`[null,null]`))
	for _, query := range []string{"cursor=" + bad, "cursor=invalid!", "limit=0", "limit=101", "from=2026-10-01T25%3A00%3A00Z", "from=2026-10-01T12%3A00%3A00,5Z"} {
		status, _ := searchRecords(t, origin, query)
		if status != 400 {
			t.Fatalf("malformed search input accepted: %s", query)
		}
	}
}
func TestPostSearchUsesTheSameLexicalPage(t *testing.T) {
	path, _, _ := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Post search"}, restoredToken)
	id := project["project"].(map[string]any)["id"].(string)
	_, capture := captureRequest(t, origin, id, "", map[string]any{"type": "constraint", "title": "PostQuery", "authority": "explicit", "payload": map[string]any{"constraint": "Use English."}})
	data, _ := json.Marshal(map[string]any{"query": "PostQuery", "projectIds": []string{id}, "types": []string{"constraint"}, "limit": 10})
	req, _ := http.NewRequest("POST", origin+"/api/v1/search", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+restoredToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	json.NewDecoder(res.Body).Decode(&body)
	if res.StatusCode != 200 || len(body["records"].([]any)) != 1 || body["records"].([]any)[0].(map[string]any)["id"] != capture["record"].(map[string]any)["id"] {
		t.Fatal("POST search changed retrieval contract")
	}
}

func TestCaptureFailureRollsBackPublicHistoryAndRetry(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			fault := `CREATE TRIGGER capture_fault BEFORE INSERT ON entities WHEN NEW.kind='audit' AND json_extract(NEW.data,'$.action')='record.created' BEGIN SELECT RAISE(ABORT,'synthetic-injected-secret'); END`
			if engine == "postgres" {
				fault = `CREATE FUNCTION capture_fault_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='audit' AND NEW.data->>'action'='record.created' THEN RAISE EXCEPTION 'synthetic-injected-secret'; END IF; RETURN NEW; END $$; CREATE TRIGGER capture_fault BEFORE INSERT ON entities FOR EACH ROW EXECUTE FUNCTION capture_fault_fn()`
			}
			if _, err = db.Exec(fault); err != nil {
				t.Fatal(err)
			}
			origin := startServer(t, path, databaseURL)
			_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Rollback"}, restoredToken)
			id := project["project"].(map[string]any)["id"].(string)
			record := map[string]any{"type": "finding", "title": "RollbackCapture", "authority": "observed", "payload": map[string]any{"finding": "The entire transaction must commit together."}}
			status, body := captureRequest(t, origin, id, "retry-after-fault", record)
			data, _ := json.Marshal(body)
			if status != 503 || strings.Contains(string(data), "synthetic-injected-secret") {
				t.Fatal("capture fault did not produce a safe failure")
			}
			status, body = searchRecords(t, origin, "projectId="+id)
			if status != 200 || len(body["records"].([]any)) != 0 {
				t.Fatal("failed capture left partial public history")
			}
			drop := `DROP TRIGGER capture_fault`
			if engine == "postgres" {
				drop = `DROP TRIGGER capture_fault ON entities; DROP FUNCTION capture_fault_fn()`
			}
			if _, err = db.Exec(drop); err != nil {
				t.Fatal(err)
			}
			status, _ = captureRequest(t, origin, id, "retry-after-fault", record)
			if status != 201 {
				t.Fatal("rolled-back retry identity prevented retry after fault removal")
			}
			status, body = searchRecords(t, origin, "projectId="+id+"&q=RollbackCapture")
			if status != 200 || len(body["records"].([]any)) != 1 {
				t.Fatal("corrected capture was not indexed once")
			}
		})
	}
}

func TestLexicalSearchLoadsOnlyCandidateMetadata(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			projectID := jobProject(t, origin, "Focused search metadata", false, false)
			wanted := curationCapture(t, origin, projectID, "MATCHING_METADATA_WITNESS")
			unrelated := curationCapture(t, origin, projectID, "OTHER_METADATA_WITNESS")
			driver, dsn := "sqlite", path
			if engine == "postgres" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.Exec(`UPDATE entities SET data='[]' WHERE kind='metadata' AND id=$1`, unrelated["id"]); err != nil {
				t.Fatal(err)
			}
			status, response := searchRecords(t, origin, "q=MATCHING_METADATA_WITNESS")
			if status != 200 || len(response["records"].([]any)) != 1 {
				t.Fatalf("unrelated metadata disabled targeted search: %d %v", status, response)
			}
			item := response["records"].([]any)[0].(map[string]any)
			if item["id"] != wanted["id"] || item["metadata"].(map[string]any)["id"] != wanted["id"] {
				t.Fatal("candidate metadata was lost or attached to the wrong source")
			}
			if _, err = db.Exec(`UPDATE entities SET data='[]' WHERE kind='metadata' AND id=$1`, wanted["id"]); err != nil {
				t.Fatal(err)
			}
			status, _ = searchRecords(t, origin, "q=MATCHING_METADATA_WITNESS")
			if status != 503 {
				t.Fatal("matching corrupt metadata was silently ignored")
			}
		})
	}
}
