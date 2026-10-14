package backend_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"testing"
)

func TestSuggestionPagesOrderTimestampInstantsAndPreserveStoredTies(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			base := suggestionFixture(t, origin, "summary", map[string]any{"text": "Chronological historical suggestion"})
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
			dates := []string{"2026-10-01T00:00Z", "2026-10-01T00:00:59Z", "2026-10-01T00:00:00.000000000200000000000Z", "2026-10-01T00:00:00.0000000001000Z", "2026-10-01T00:00:00.0000000001Z", "2026-10-01T01:00:00+01:00", "2026-10-01T00:00:00.000Z", "2026-09-30T23:30:00-01:00", "2026-10-01T01:00:00.000000000100000+01:00", "0000-01-01T00:00:00.0000000001+01:00", "0000-01-01T01:00:00.0000000002+02:00", "0000-01-01T00:00Z", "2026-09-30T23:00:59-01:00"}
			for i, date := range dates {
				candidate := map[string]any{}
				for k, v := range base {
					candidate[k] = v
				}
				candidate["id"], candidate["createdAt"] = fmt.Sprintf("artifact_time_%d", i), date
				raw, _ := json.Marshal(candidate)
				stored := fmt.Sprintf("2026-10-02T00:00:00.%03dZ", i)
				if i == 4 {
					stored = "2026-10-02T00:00:00.003Z"
				}
				if _, err = db.Exec("INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_artifact',$1,$2,1,$3)", candidate["id"], string(raw), stored); err != nil {
					t.Fatal(err)
				}
			}
			// Hidden newer suggestions must not count as a continuation, including
			// enough rows to cross the native selection window.
			for i := 0; i < 70; i++ {
				hidden := map[string]any{}
				for k, v := range base {
					hidden[k] = v
				}
				hidden["id"], hidden["createdAt"] = fmt.Sprintf("artifact_hidden_%03d", i), "2026-10-03T00:00Z"
				hidden["privacyDependencies"] = map[string]any{"version": 1, "projectIds": []any{"proj_missing"}, "recordIds": []any{"rec:portable"}, "crossProject": false}
				raw, _ := json.Marshal(hidden)
				if _, err = db.Exec("INSERT INTO entities(kind,id,data,version,created_at) VALUES('ai_artifact',$1,$2,1,'2026-10-04T00:00:00Z')", hidden["id"], string(raw)); err != nil {
					t.Fatal(err)
				}
			}
			for _, scoped := range []bool{false, true} {
				query := url.Values{"limit": {"1"}}
				expected := []int{7, 1, 12, 2, 3, 4, 8, 0, 5, 6, 11, 10, 9}
				if scoped {
					query.Set("projectId", "proj_portable")
					expected = []int{7, 12, 1, 2, 8, 3, 4, 6, 5, 0, 11, 10, 9}
				}
				got := []int{}
				for i := 0; i < len(expected); i++ {
					status, page := curationRequest(t, origin, "GET", "/suggestions?"+query.Encode(), nil)
					if status != 200 {
						t.Fatalf("page%d scoped%v HTTP%d %v", i, scoped, status, page)
					}
					rows := page["suggestions"].([]any)
					if len(rows) != 1 {
						t.Fatalf("page%d has%d suggestions", i, len(rows))
					}
					row := rows[0].(map[string]any)
					var index int
					if _, err = fmt.Sscanf(row["id"].(string), "artifact_time_%d", &index); err != nil {
						t.Fatal(err)
					}
					if row["createdAt"] != dates[index] {
						t.Fatal("immutable timestamp changed")
					}
					got = append(got, index)
					if i+1 < len(expected) {
						if page["hasMore"] != true {
							t.Fatal("readable continuation missing")
						}
						query.Set("cursor", page["nextCursor"].(string))
					} else if page["hasMore"] != false || page["nextCursor"] != nil {
						t.Fatal("hidden history advertised continuation")
					}
				}
				if !reflect.DeepEqual(got, expected) {
					t.Fatalf("scoped%v order%v want%v", scoped, got, expected)
				}
			}
		})
	}
}
