package backend_test

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCompactContextIsVersionedBoundedAndKeepsCurrentState(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, response := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Compact resumption"}, restoredToken)
			if status != 200 {
				t.Fatal("project failed")
			}
			projectID := response["project"].(map[string]any)["id"].(string)
			status, response = captureRequest(t, origin, projectID, "", map[string]any{"type": "project_state", "title": "Current paused state", "authority": "observed", "payload": map[string]any{"state": "paused", "reason": "Wait for the owner review.", "followUp": "Resume after reviewing the cited evidence."}})
			if status != 201 {
				t.Fatal("state failed")
			}
			stateID := response["record"].(map[string]any)["id"].(string)
			for index := 0; index < 15; index++ {
				status, _ = captureRequest(t, origin, projectID, "", map[string]any{"type": "decision", "title": "Long decision", "authority": "explicit", "payload": map[string]any{"decision": strings.Repeat("Synthetic evidence. ", 500)}})
				if status != 201 {
					t.Fatal("decision failed")
				}
			}
			status, response = curationRequest(t, origin, "GET", "/projects/"+projectID+"/context?version=2&limit=4&maxChars=6000", nil)
			if status != 200 || response["version"] != float64(2) {
				t.Fatalf("compact context unavailable: %d", status)
			}
			encoded, _ := json.Marshal(response)
			if len(encoded) > 6000 {
				t.Fatalf("context exceeded its response budget: %d", len(encoded))
			}
			records := response["records"].([]any)
			if len(records) != 4 {
				t.Fatal("compact record limit not respected")
			}
			seen := map[string]bool{}
			for _, item := range records {
				record := item.(map[string]any)
				id := record["id"].(string)
				if seen[id] {
					t.Fatal("full record repeated across compact sections")
				}
				seen[id] = true
				if _, exists := record["payload"]; exists {
					t.Fatal("compact view returned a full payload")
				}
				if _, exists := record["content"]; exists {
					t.Fatal("compact view returned full content")
				}
				if _, exists := record["preview"]; !exists {
					t.Fatal("source preview missing")
				}
			}
			if !seen[stateID] || response["sections"].(map[string]any)["currentState"] != stateID {
				t.Fatal("history limit displaced current state")
			}
			if response["counts"].(map[string]any)["total"] != float64(16) || response["truncation"].(map[string]any)["recordsOmitted"] != float64(12) {
				t.Fatal("truncation concealed omitted evidence")
			}
			status, alias := curationRequest(t, origin, "GET", "/projects/"+projectID+"/context/compact?limit=4&maxChars=6000", nil)
			if status != 200 || !reflect.DeepEqual(alias, response) {
				t.Fatal("compact SDK endpoint differs from explicit version-2 context")
			}
			legacy := projectMemory(t, origin, projectID)
			if legacy["currentState"].(map[string]any)["id"] != stateID || legacy["recentDecisions"] == nil || legacy["records"] != nil {
				t.Fatal("default context contract silently changed")
			}
		})
	}
}

func TestCompactContextSelectsCurrentStateBeforeUnverifiedHistoryLimit(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, response := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Current before limits"}, restoredToken)
			if status != 200 {
				t.Fatal("project failed")
			}
			projectID := response["project"].(map[string]any)["id"].(string)
			status, response = captureRequest(t, origin, projectID, "", map[string]any{"type": "project_state", "title": "Known state", "authority": "observed", "payload": map[string]any{"state": "active"}})
			if status != 201 {
				t.Fatal("state failed")
			}
			current := response["record"].(map[string]any)["id"].(string)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			for index := 0; index < 25; index++ {
				id := "rec_unverified_" + strings.Repeat("x", index+1)
				row := map[string]any{"id": id, "version": 1, "createdAt": "2090-01-01T00:00:00Z", "recordedAt": "2090-01-01T00:00:00Z", "projectId": projectID, "type": "project_state", "title": "Imported newer state", "payload": map[string]any{"state": "paused"}, "provenance": map[string]any{"import": map[string]any{"sourceName": "synthetic"}}}
				raw, _ := json.Marshal(row)
				if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('record',$1,$2,1,'2090-01-01T00:00:00Z')`, id, string(raw)); err != nil {
					t.Fatal(err)
				}
			}
			db.Close()
			status, response = curationRequest(t, origin, "GET", "/projects/"+projectID+"/context?version=2&limit=1", nil)
			if status != 200 || response["sections"].(map[string]any)["currentState"] != current || response["records"].([]any)[0].(map[string]any)["id"] != current {
				t.Fatal("unverified history displaced known current state")
			}
			if projectMemory(t, origin, projectID)["currentState"].(map[string]any)["id"] != current {
				t.Fatal("legacy current state still selected after truncation")
			}
		})
	}
}

func TestCompactContextBudgetPreservesUnicodeAndReportsLimits(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	status, response := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Budgeted context"}, restoredToken)
	if status != 200 {
		t.Fatal("project failed")
	}
	projectID := response["project"].(map[string]any)["id"].(string)
	status, response = captureRequest(t, origin, projectID, "", map[string]any{"type": "project_state", "title": strings.Repeat("<", 400), "authority": "observed", "payload": map[string]any{"state": "paused", "reason": strings.Repeat("🦊<", 1000), "followUp": strings.Repeat("<", 1500)}})
	if status != 201 {
		t.Fatal("state failed")
	}
	id := response["record"].(map[string]any)["id"]
	status, response = curationRequest(t, origin, "GET", "/projects/"+projectID+"/context?version=2&maxChars=4096&previewChars=2000", nil)
	if status != 200 {
		t.Fatal("tight context unavailable")
	}
	encoded, _ := json.Marshal(response)
	if len(encoded)+1 > 4096 || strings.Contains(string(encoded), "�") || response["sections"].(map[string]any)["currentState"] != id || response["truncation"].(map[string]any)["budgetLimited"] != true {
		t.Fatal("tight budget corrupted Unicode or lost current state/truncation")
	}
	for _, query := range []string{"version=3", "version=2&limit=0", "version=2&maxChars=10", "version=2&previewChars=0"} {
		status, _ = curationRequest(t, origin, "GET", "/projects/"+projectID+"/context?"+query, nil)
		if status != 400 {
			t.Fatal("invalid compact limits accepted")
		}
	}
}
