package backend_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestRAGRawLexicalScopeDistinguishesRecordMatchesFromCitedExcerpt(t *testing.T) {
	path, databaseURL := ragInstance(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "synthetic outage", 503) }))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	if status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2}); status != 200 {
		t.Fatal("provider fixture failed")
	}
	for _, test := range []struct {
		name, title, content, query, scope string
		budget                             int
	}{
		{"split chunks", "Split record", "FIRST_SCOPE_TOKEN " + strings.Repeat("x", 7000) + " SECOND_SCOPE_TOKEN", "FIRST_SCOPE_TOKEN SECOND_SCOPE_TOKEN", "record", 12000},
		{"distant excerpt", "Distant record", "FIRST_SCOPE_TOKEN " + strings.Repeat("y", 3000) + " SECOND_SCOPE_TOKEN", "FIRST_SCOPE_TOKEN SECOND_SCOPE_TOKEN", "record", 12000},
		{"cited passage", "Together record", "FIRST_SCOPE_TOKEN and SECOND_SCOPE_TOKEN belong together.", "FIRST_SCOPE_TOKEN SECOND_SCOPE_TOKEN", "passage", 12000},
		{"title only", "TITLE_SCOPE_TOKEN", "The content has no matching identifier.", "TITLE_SCOPE_TOKEN", "title", 12000},
		{"bounded passage", "Bounded record", "FIRST_SCOPE_TOKEN " + strings.Repeat("界", 950) + " SECOND_SCOPE_TOKEN", "FIRST_SCOPE_TOKEN SECOND_SCOPE_TOKEN", "record", 4096},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := jobProject(t, origin, "Lexical scope "+test.name, true, false)
			status, captured := captureRequest(t, origin, project, "", map[string]any{"type": "finding", "title": test.title, "authority": "observed", "payload": map[string]any{"finding": test.content}})
			if status != 201 {
				t.Fatal("source capture failed")
			}
			record := captured["record"].(map[string]any)
			if test.name == "bounded passage" {
				// A valid historical lifecycle label consumes response bytes while
				// the original source excerpt still contains both lexical terms.
				db, err := sql.Open("pgx", databaseURL)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec(`UPDATE entities SET data=jsonb_set(data,'{legacyStatus}',to_jsonb($1::text)) WHERE kind='metadata' AND id=$2`, strings.Repeat("Historical", 120), record["id"])
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			status, response := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": project, "query": test.query, "maxChars": test.budget, "limit": 100})
			if status != 200 {
				t.Fatal("lexical retrieval failed")
			}
			found := false
			for _, value := range response["results"].([]any) {
				item := value.(map[string]any)
				if item["recordId"] != record["id"] {
					continue
				}
				found = true
				if item["queryMatchScope"] != test.scope {
					t.Fatalf("lexical evidence scope = %v; want %s", item["queryMatchScope"], test.scope)
				}
				if test.name == "bounded passage" && strings.Contains(item["text"].(string), "SECOND_SCOPE_TOKEN") {
					t.Fatal("budget fixture did not shorten the matching excerpt")
				}
				location := item["sourceLocation"].(map[string]any)
				units := utf16.Encode([]rune(record[location["field"].(string)].(string)))
				start, end := int(location["start"].(float64)), int(location["end"].(float64))
				if item["text"] != string(utf16.Decode(units[start:end])) {
					t.Fatal("scope metadata changed exact immutable citation")
				}
			}
			if !found {
				t.Fatal("matching lexical source omitted")
			}
		})
	}
}
