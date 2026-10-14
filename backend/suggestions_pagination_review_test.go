package backend_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSuggestionPagesBoundReadableHistoryAndPreserveScopeOrdering(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			base := suggestionFixture(t, origin, "summary", map[string]any{"text": "Readable historical interpretation"})
			driver, dsn := "sqlite", path
			if engine == "postgres" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.Exec("DELETE FROM entities WHERE kind='ai_artifact'"); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 207; i++ {
				copy := map[string]any{}
				for k, v := range base {
					copy[k] = v
				}
				id := fmt.Sprintf("artifact_%03d", i)
				copy["id"] = id
				// Malformed historical content must be filtered before deciding
				// whether a readable continuation exists, including across windows.
				if i >= 200 {
					copy["content"] = map[string]any{"text": ""}
				}
				raw, _ := json.Marshal(copy)
				stored := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Millisecond).Format("2006-01-02T15:04:05.000Z")
				if _, err = db.Exec("INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_artifact',$1,$2,1,$3)", id, string(raw), stored); err != nil {
					t.Fatal(err)
				}
			}
			status, first := curationRequest(t, origin, "GET", "/suggestions", nil)
			if status != 200 || len(first["suggestions"].([]any)) != 100 || first["hasMore"] != true {
				t.Fatalf("suggestion history is not bounded: HTTP%d %v", status, first["hasMore"])
			}
			cursor, ok := first["nextCursor"].(string)
			if !ok || cursor == "" {
				t.Fatal("readable continuation missing")
			}
			status, last := curationRequest(t, origin, "GET", "/suggestions?cursor="+url.QueryEscape(cursor), nil)
			if status != 200 || len(last["suggestions"].([]any)) != 100 || last["hasMore"] != false || last["nextCursor"] != nil {
				t.Fatal("hidden tail advertised a continuation or lost readable history")
			}
			for pageIndex, page := range []map[string]any{first, last} {
				for i, raw := range page["suggestions"].([]any) {
					if raw.(map[string]any)["id"] != fmt.Sprintf("artifact_%03d", pageIndex*100+i) {
						t.Fatal("unscoped stable tie ordering changed")
					}
				}
			}
			status, scoped := curationRequest(t, origin, "GET", "/suggestions?projectId=proj_portable&limit=200", nil)
			if status != 200 || len(scoped["suggestions"].([]any)) != 200 || scoped["hasMore"] != false {
				t.Fatal("scoped maximum page unavailable")
			}
			for i, raw := range scoped["suggestions"].([]any) {
				if raw.(map[string]any)["id"] != fmt.Sprintf("artifact_%03d", 199-i) {
					t.Fatal("scoped stable tie ordering changed")
				}
			}
			for _, query := range []string{"limit=0", "limit=201", "limit=", "limit=1.5", "cursor=", "cursor=invalid", "projectId=proj_portable&cursor=" + url.QueryEscape(cursor)} {
				status, _ = curationRequest(t, origin, "GET", "/suggestions?"+query, nil)
				if status != 400 {
					t.Fatalf("invalid suggestion query accepted: %s HTTP%d", query, status)
				}
			}
		})
	}
}

func TestSuggestionContinuationReplaysLongImportedScope(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			artifact := suggestionFixture(t, origin, "summary", map[string]any{"text": "Long scoped historical interpretation"})
			status, response := projectRequest(t, origin, "GET", "/proj_portable", nil, restoredToken)
			if status != 200 {
				t.Fatal("project lookup failed")
			}
			baseProject := response["project"].(map[string]any)
			settings := baseProject["settings"].(map[string]any)
			settings["crossProjectAnalysis"] = true
			status, _ = projectRequest(t, origin, "PATCH", "/proj_portable/settings", map[string]any{"expectedVersion": baseProject["version"], "settings": settings}, restoredToken)
			if status != 200 {
				t.Fatal("participation failed")
			}
			status, archive := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("archive unavailable")
			}
			data := archive["data"].(map[string]any)
			projects := data["project"].([]any)
			query := url.Values{"crossProject": {"true"}, "limit": {"1"}, "projectIds": {"proj_portable"}}
			for i := 0; i < 99; i++ {
				id := fmt.Sprintf("proj_long_%03d_%s", i, strings.Repeat("x", 100))
				project := map[string]any{}
				for key, value := range baseProject {
					project[key] = value
				}
				project["id"], project["name"], project["version"] = id, fmt.Sprintf("Long scope participant %d", i), 1
				project["settings"] = settings
				projects = append(projects, project)
				query.Add("projectIds", id)
			}
			data["project"] = projects
			artifacts := []any{}
			for _, id := range []string{"artifact_long_a", "artifact_long_b"} {
				copy := map[string]any{}
				for key, value := range artifact {
					copy[key] = value
				}
				copy["id"] = id
				artifacts = append(artifacts, copy)
			}
			data["ai_artifact"] = artifacts
			status, response = curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 {
				t.Fatalf("long project scope import failed: HTTP%d %v", status, response)
			}
			status, first := curationRequest(t, origin, "GET", "/suggestions?"+query.Encode(), nil)
			if status != 200 || len(first["suggestions"].([]any)) != 1 || first["hasMore"] != true {
				t.Fatal("long readable scope missing")
			}
			query.Set("cursor", first["nextCursor"].(string))
			status, next := curationRequest(t, origin, "GET", "/suggestions?"+query.Encode(), nil)
			if status != 200 || len(next["suggestions"].([]any)) != 1 || next["suggestions"].([]any)[0].(map[string]any)["id"] == first["suggestions"].([]any)[0].(map[string]any)["id"] {
				t.Fatalf("server rejected its own long-scope continuation: HTTP%d %v", status, next)
			}
		})
	}
}

func TestSQLiteSuggestionPageDoesNotDecodeMalformedOffPageHistory(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	base := suggestionFixture(t, origin, "summary", map[string]any{"text": "Selected healthy evidence"})
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base["id"] = "artifact_second"
	raw, _ := json.Marshal(base)
	if _, err = db.Exec("INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_artifact',$1,$2,1,'2026-10-01T00:00:00.000Z')", "artifact_second", string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_artifact','artifact_corrupt','synthetic-private-malformed',1,'2020-01-01T00:00:00.000Z')"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"limit=1", "projectId=proj_portable&limit=1"} {
		status, response := curationRequest(t, origin, "GET", "/suggestions?"+query, nil)
		if status != 200 || len(response["suggestions"].([]any)) != 1 || response["hasMore"] != true {
			t.Fatalf("malformed off-page history blocked healthy page: HTTP%d %v", status, response)
		}
	}
}
