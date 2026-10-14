package backend_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestSearchDecodesOnlyItsSelectedPageAndFilterMatches(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			project := jobProject(t, origin, "Bounded header selection", false, false)
			old := curationCapture(t, origin, project, "Excluded old history")
			status, _ := curationRequest(t, origin, "PATCH", "/records/"+old["id"].(string)+"/metadata", map[string]any{"expectedVersion": 1, "archived": true})
			if status != 200 {
				t.Fatal("historical lifecycle fixture failed")
			}
			outside := jobProject(t, origin, "Outside relationship endpoint", false, false)
			peer := curationCapture(t, origin, outside, "Outside peer")
			good := curationCapture(t, origin, project, "Newest selected evidence")
			status, _ = curationRequest(t, origin, "PATCH", "/records/"+good["id"].(string)+"/metadata", map[string]any{"expectedVersion": 1, "tags": []any{"selected"}})
			if status != 200 {
				t.Fatal("selected tag fixture failed")
			}
			status, _ = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": good["id"], "toRecordId": peer["id"], "type": "related_to", "authority": "explicit"})
			if status != 201 {
				t.Fatal("selected relationship fixture failed")
			}
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			// Only the rebuildable header input is corrupted. Source identity,
			// project membership, raw capture and curated filters remain intact.
			if _, err = db.Exec(`UPDATE context_headers SET data=$1 WHERE record_id=$2`, `["synthetic-invalid-header"]`, old["id"]); err != nil {
				t.Fatal(err)
			}
			base := "projectId=" + url.QueryEscape(project)
			for _, check := range []struct {
				name, query string
				continued   bool
			}{
				{"page", "limit=1", true},
				{"tag", "tag=selected", false},
				{"lifecycle", "status=active", false},
				{"archived", "archived=false", false},
				{"relationship", "relationshipType=related_to&relatedTo=" + url.QueryEscape(peer["id"].(string)), false},
			} {
				t.Run(check.name, func(t *testing.T) {
					status, result := searchRecords(t, origin, base+"&"+check.query)
					if status != 200 || len(result["records"].([]any)) != 1 || result["records"].([]any)[0].(map[string]any)["id"] != good["id"] {
						t.Fatalf("unselected header was decoded before %s selection: HTTP %d %+v", check.name, status, result)
					}
					cursor, _ := result["nextCursor"].(string)
					if (cursor != "") != check.continued {
						t.Fatal("selection continuation was not truthful")
					}
				})
			}
			status, result := searchRecords(t, origin, base+"&limit=100")
			raw, _ := json.Marshal(result)
			if status != 503 || strings.Contains(string(raw), "synthetic-invalid-header") {
				t.Fatal("selected corrupt header did not fail safely")
			}
		})
	}
}

func TestSearchRetainsHistoricalOffsetFractionCursorOrder(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			project := jobProject(t, origin, "Historical offset cursor", false, false)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, fixture := range []struct{ id, at string }{{"rec_offset", "2026-10-01T00:00:00.001+00:00"}, {"rec_canonical", "2026-10-01T00:00:00.9Z"}} {
				seedSearchHistoricalRow(t, tx, project, fixture.id, fixture.at)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			cursor := ""
			// Preserve the admitted Go timestampParts ordering: historical offset
			// fractions use Unix milliseconds, unlike canonical separate fractions.
			for page, id := range []string{"rec_offset", "rec_canonical"} {
				status, result := searchRecords(t, origin, "projectId="+url.QueryEscape(project)+"&limit=1&cursor="+url.QueryEscape(cursor))
				if status != 200 || len(result["records"].([]any)) != 1 || result["records"].([]any)[0].(map[string]any)["id"] != id {
					t.Fatalf("historical offset cursor lost existing order atpage%d: HTTP%d %+v", page, status, result)
				}
				cursor, _ = result["nextCursor"].(string)
				if (cursor != "") != (page == 0) {
					t.Fatal("historical offset continuation lost or duplicated evidence")
				}
			}
		})
	}
}

func TestSearchEnglishTimestampTieGroupSpansNativeWindows(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			project := jobProject(t, origin, "Large English timestamp tie", false, false)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			ids := []string{}
			for i := 0; i < 300; i++ {
				id := fmt.Sprintf("rec_tie_%03d", i)
				if i%2 == 0 {
					id = strings.ToUpper(id)
				}
				ids = append(ids, id)
				seedSearchHistoricalRow(t, tx, project, id, "2026-10-01T00:00:00Z")
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			cursor := ""
			seen := map[string]bool{}
			position := 299
			for page := 0; page < 3; page++ {
				status, result := searchRecords(t, origin, "projectId="+url.QueryEscape(project)+"&limit=100&cursor="+url.QueryEscape(cursor))
				if status != 200 || len(result["records"].([]any)) != 100 {
					t.Fatalf("large timestamp tie lostpage%d: HTTP%d %+v", page, status, result)
				}
				for _, item := range result["records"].([]any) {
					id := item.(map[string]any)["id"].(string)
					if seen[id] || id != ids[position] {
						t.Fatalf("English tie window reordered/duplicated source%d: %s", position, id)
					}
					seen[id] = true
					position--
				}
				cursor, _ = result["nextCursor"].(string)
				if (cursor != "") != (page < 2) {
					t.Fatal("large timestamp tie continuation changed")
				}
			}
			if len(seen) != 300 || position != -1 {
				t.Fatal("large timestamp tie omitted source history")
			}
		})
	}
}

func seedSearchHistoricalRow(t *testing.T, tx *sql.Tx, project, id, at string) {
	t.Helper()
	record, _ := json.Marshal(map[string]any{"id": id, "projectId": project, "version": 1, "createdAt": at, "recordedAt": "2026-10-01T00:00:00Z", "type": "finding", "title": "Historical source", "content": "Historical source evidence.", "authority": "observed", "payload": map[string]any{"finding": "Historical source evidence."}})
	if _, err := tx.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('record',$1,$2,1,'2026-10-01T00:00:00Z')`, id, string(record)); err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(map[string]any{"id": id, "recordId": id, "version": 1, "createdAt": "2026-10-01T00:00:00Z", "tags": []any{}, "archived": false})
	if _, err := tx.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('metadata',$1,$2,1,'2026-10-01T00:00:00Z')`, id, string(metadata)); err != nil {
		t.Fatal(err)
	}
}

func TestSearchNativeSelectionPreservesHistoricalCalendarAndPrecision(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			project := jobProject(t, origin, "Historical timestamp selection", false, false)
			base := curationCapture(t, origin, project, "Healthy newest evidence")
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			fixtures := []struct{ id, at string }{
				{"rec_fraction_c", "2026-01-01T00:00:00.000000000200000000000Z"},
				{"rec_fraction_b", "2026-01-01T00:00:00.0000000001000Z"},
				{"rec_fraction_a", "2026-01-01T00:00:00.0000000001Z"},
				{"rec_zero_b", "2026-01-01T00:00Z"},
				{"rec_zero_a", "2026-01-01T00:00:00.00000Z"},
				{"rec_year_zero", "0000-02-29T00:00Z"},
				{"rec_bad_calendar", "2026-02-29T00:00Z"},
				{"rec_bad_century", "1900-02-29T00:00Z"},
				{"rec_bad_hour", "2026-01-01T24:00Z"},
				{"rec_bad_second", "2026-01-01T00:00:60Z"},
				{"rec_bad_tail", "2026-01-01T00:00Ztrailing"},
				{"rec_bad_unicode", "2026-01-01T00:00Ｚ"},
			}
			// PostgreSQL JSONB refuses NUL strings at storage, so this historical
			// SQLite text case is exercised only where it can actually exist.
			if engine == "sqlite" {
				fixtures = append(fixtures, struct{ id, at string }{"rec_bad_nul", "2026-01-01T00:00Z\x00"})
			}
			for _, fixture := range fixtures {
				record := map[string]any{}
				for key, value := range base {
					record[key] = value
				}
				record["id"], record["createdAt"], record["happenedAt"] = fixture.id, fixture.at, fixture.at
				raw, _ := json.Marshal(record)
				if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('record',$1,$2,1,'2026-01-01T00:00Z')`, fixture.id, string(raw)); err != nil {
					t.Fatal(err)
				}
				metadata, _ := json.Marshal(map[string]any{"id": fixture.id, "recordId": fixture.id, "version": 1, "createdAt": "2026-01-01T00:00Z", "tags": []any{}, "archived": false})
				if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('metadata',$1,$2,1,'2026-01-01T00:00Z')`, fixture.id, string(metadata)); err != nil {
					t.Fatal(err)
				}
			}
			query := "projectId=" + url.QueryEscape(project)
			status, result := searchRecords(t, origin, query+"&limit=100")
			if status != 200 || len(result["records"].([]any)) != len(fixtures)+1 {
				t.Fatalf("historical calendar input was lost or caused SQL failure: HTTP%d %+v", status, result)
			}
			for index, id := range []string{base["id"].(string), "rec_fraction_c", "rec_fraction_b", "rec_fraction_a", "rec_zero_b", "rec_zero_a", "rec_year_zero"} {
				if result["records"].([]any)[index].(map[string]any)["id"] != id {
					t.Fatalf("native selection changed exact chronological or English tie order at%d: %+v", index, result)
				}
			}
			seen := map[string]string{}
			for _, item := range result["records"].([]any) {
				record := item.(map[string]any)
				seen[record["id"].(string)] = record["createdAt"].(string)
			}
			for _, fixture := range fixtures {
				if seen[fixture.id] != fixture.at {
					t.Fatal("historical source date was normalized or omitted")
				}
			}
			status, result = searchRecords(t, origin, query+"&from=2026-01-01T00%3A00Z&to=2026-01-01T00%3A00%3A01Z")
			if status != 200 || len(result["records"].([]any)) != 5 {
				t.Fatalf("date bounds admitted invalid calendar/Unicode tails or lost valid precision: HTTP%d %+v", status, result)
			}
		})
	}
}
