package backend_test

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"testing"
)

func TestBlankListingDecodesOnlyScopedPageRecords(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			wantedProject := jobProject(t, origin, "Scoped listing", false, false)
			otherProject := jobProject(t, origin, "Other history", false, false)
			wanted := curationCapture(t, origin, wantedProject, "SCOPED_LIST_WITNESS")
			other := curationCapture(t, origin, otherProject, "OTHER_LIST_WITNESS")
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			// Fault input in another project's immutable body must not be decoded
			// to produce this project's first page.
			if _, err = db.Exec(`UPDATE entities SET data='[]' WHERE kind='record' AND id=$1`, other["id"]); err != nil {
				t.Fatal(err)
			}
			status, response := searchRecords(t, origin, "projectId="+wantedProject+"&limit=1")
			if status != 200 || len(response["records"].([]any)) != 1 || response["records"].([]any)[0].(map[string]any)["id"] != wanted["id"] {
				t.Fatalf("scoped listing decoded other-project history: HTTP %d", status)
			}
			status, _ = searchRecords(t, origin, "q=OTHER_LIST_WITNESS")
			if status != 503 {
				t.Fatal("native matching corrupt record was silently ignored")
			}
		})
	}
}

func TestListingReadsFullCurationOnlyForItsPage(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			project := jobProject(t, origin, "Curation pages", false, false)
			older := curationCapture(t, origin, project, "CURATION_PAGE_WITNESS older")
			wanted := curationCapture(t, origin, project, "CURATION_PAGE_WITNESS current")
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			older["createdAt"] = "2000-01-01T00:00:00.000Z"
			raw, _ := json.Marshal(older)
			if _, err = db.Exec(`UPDATE entities SET data=$1 WHERE kind='record' AND id=$2`, string(raw), older["id"]); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`UPDATE entities SET data='[]' WHERE kind='metadata' AND id=$1`, older["id"]); err != nil {
				t.Fatal(err)
			}
			status, body := searchRecords(t, origin, "projectId="+project+"&limit=1")
			if status != 200 || len(body["records"].([]any)) != 1 || body["records"].([]any)[0].(map[string]any)["id"] != wanted["id"] {
				t.Fatalf("page decoded omitted full curation: HTTP %d", status)
			}
			cursor, ok := body["nextCursor"].(string)
			if !ok || cursor == "" {
				t.Fatal("omitted curated result disappeared")
			}
			status, _ = searchRecords(t, origin, "projectId="+project+"&limit=1&cursor="+url.QueryEscape(cursor))
			if status != 503 {
				t.Fatal("selected-page curation corruption was silently ignored")
			}
		})
	}
}

func TestLexicalListingHydratesOnlyItsPage(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			project := jobProject(t, origin, "Lexical pages", false, false)
			older := curationCapture(t, origin, project, "PAGE_SCOPE_WITNESS older")
			wanted := curationCapture(t, origin, project, "PAGE_SCOPE_WITNESS current")
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.Exec(`UPDATE entities SET data='[]' WHERE kind='record' AND id=$1`, older["id"]); err != nil {
				t.Fatal(err)
			}
			status, body := searchRecords(t, origin, "q=PAGE_SCOPE_WITNESS&limit=1")
			if status != 200 || len(body["records"].([]any)) != 1 || body["records"].([]any)[0].(map[string]any)["id"] != wanted["id"] {
				t.Fatalf("lexical page decoded omitted body: HTTP %d", status)
			}
			cursor, ok := body["nextCursor"].(string)
			if !ok || cursor == "" {
				t.Fatal("omitted lexical result disappeared from pagination")
			}
			status, _ = searchRecords(t, origin, "q=PAGE_SCOPE_WITNESS&limit=1&cursor="+url.QueryEscape(cursor))
			if status != 503 {
				t.Fatal("selected-page corruption was silently ignored")
			}
		})
	}
}

func TestRelationshipCycleCheckIgnoresDisconnectedHistory(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			project := jobProject(t, origin, "Focused graph", false, false)
			first := curationCapture(t, origin, project, "First graph record")
			second := curationCapture(t, origin, project, "Second graph record")
			third := curationCapture(t, origin, project, "Third graph record")
			status, body := curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": third["id"], "toRecordId": first["id"], "type": "supports", "authority": "explicit"})
			if status != 201 {
				t.Fatal("history fixture relationship failed")
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
			if _, err = db.Exec(`UPDATE entities SET data='[]' WHERE kind='relationship' AND id=$1`, body["relationship"].(map[string]any)["id"]); err != nil {
				t.Fatal(err)
			}
			status, _ = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": first["id"], "toRecordId": second["id"], "type": "depends_on", "authority": "explicit"})
			if status != 201 {
				t.Fatalf("native cycle check decoded disconnected history: HTTP %d", status)
			}
			status, _ = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": second["id"], "toRecordId": first["id"], "type": "depends_on", "authority": "explicit"})
			if status != 409 {
				t.Fatal("cycle protection disappeared after narrowing the query")
			}
		})
	}
}

func TestScopedSearchKeepsIncomingCrossProjectApplicability(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			project := jobProject(t, origin, "Selected scope", false, false)
			outside := jobProject(t, origin, "Other scope", false, false)
			old := curationCapture(t, origin, project, "Old guidance")
			newer := curationCapture(t, origin, outside, "Replacement guidance")
			status, _ := curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": newer["id"], "toRecordId": old["id"], "type": "replaces", "authority": "explicit"})
			if status != 201 {
				t.Fatal("cross-project replacement failed")
			}
			status, body := searchRecords(t, origin, "projectId="+project+"&status=superseded")
			if status != 200 || len(body["records"].([]any)) != 1 || body["records"].([]any)[0].(map[string]any)["id"] != old["id"] {
				t.Fatal("scoped search omitted an applicable incoming replacement")
			}
			status, body = searchRecords(t, origin, "projectId="+project+"&status=active")
			if status != 200 || len(body["records"].([]any)) != 0 {
				t.Fatal("out-of-scope replacement was ignored when deriving active status")
			}
			status, _ = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": old["id"], "toRecordId": newer["id"], "type": "partially_replaces", "authority": "explicit"})
			if status != 409 {
				t.Fatal("project boundaries bypassed mixed replacement-cycle protection")
			}
		})
	}
}
