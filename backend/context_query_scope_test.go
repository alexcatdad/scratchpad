package backend_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// The optional stress case keeps its increasing-history fixture out of routine
// test runs. It exercises the existing HTTP deadline, not an implementation's
// query count, allocation count, or internal record representation.
func TestCompactContextResumesAmongLongHistoricalRecords(t *testing.T) {
	count := 40
	if os.Getenv("SCRATCHPAD_CONTEXT_HISTORY_STRESS") == "1" {
		count = 2000
	}
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	status, response := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Long history"}, restoredToken)
	if status != 200 {
		t.Fatal("project resolution failed")
	}
	id := response["project"].(map[string]any)["id"].(string)
	status, response = captureRequest(t, origin, id, "", map[string]any{"type": "project_state", "title": "Resume here", "authority": "observed", "payload": map[string]any{"state": "active", "reason": "Current state must precede history."}})
	if status != 201 {
		t.Fatal("state capture failed")
	}
	stateID := response["record"].(map[string]any)["id"].(string)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	long := strings.Repeat("Synthetic historical evidence. ", 3200)
	for i := range count {
		record := map[string]any{"id": fmt.Sprintf("rec_long_history_%06d", i), "version": 1, "projectId": id, "type": "finding", "title": "Historical finding", "authority": "observed", "confidence": "high", "createdAt": "2026-01-01T00:00:00.000Z", "recordedAt": "2026-01-01T00:00:00.000Z", "payload": map[string]any{"finding": long}, "content": "finding: " + long}
		raw, _ := json.Marshal(record)
		if _, err = tx.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('record',$1,$2,1,'2026-01-01T00:00:00.000Z')`, record["id"], string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", origin+"/api/v1/projects/"+id+"/context/compact?limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+restoredToken)
	started := time.Now()
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("compact resume exceeded the existing HTTP deadline: %v", err)
	}
	defer res.Body.Close()
	var memory map[string]any
	if err = json.NewDecoder(res.Body).Decode(&memory); err != nil || res.StatusCode != 200 {
		t.Fatalf("compact resume unavailable after %s: HTTP %d, decode %v", time.Since(started), res.StatusCode, err)
	}
	t.Logf("compact resume from %d long records: %s", count, time.Since(started))
	if memory["counts"].(map[string]any)["total"] != float64(count+1) || memory["truncation"].(map[string]any)["recordsOmitted"] != float64(count) || memory["sections"].(map[string]any)["currentState"] != stateID {
		t.Fatal("compact resume lost current state or reported missing history as absent")
	}
	previews := memory["records"].([]any)
	if len(previews) != 1 || previews[0].(map[string]any)["id"] != stateID {
		t.Fatal("historical evidence displaced the current state")
	}
}

func TestContextQuestionStatusPreservesUnicodeAnswerEvidence(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, response := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Unicode answer evidence"}, restoredToken)
			if status != 200 {
				t.Fatal("project resolution failed")
			}
			projectID := response["project"].(map[string]any)["id"].(string)
			status, response = captureRequest(t, origin, projectID, "", map[string]any{"type": "qa", "title": "Historical answer", "authority": "explicit", "payload": map[string]any{"question": "Does the historical answer contain evidence?"}})
			if status != 201 {
				t.Fatal("question capture failed")
			}
			question := response["record"].(map[string]any)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			scenarios := []struct {
				answer string
				open   int
			}{
				{"\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000", 1},
				{"\u3000Synthetic answer evidence.\u0085", 0},
				{"\u200b", 0}, // Zero-width space is not Unicode whitespace.
			}
			if engine == "sqlite" {
				scenarios = append(scenarios, struct {
					answer string
					open   int
				}{"\x00", 0}) // SQLite accepts NUL; PostgreSQL JSONB rejects it.
			}
			for _, scenario := range scenarios {
				question["payload"].(map[string]any)["answer"] = scenario.answer
				raw, _ := json.Marshal(question)
				if _, err = db.Exec(`UPDATE entities SET data=$1 WHERE kind='record' AND id=$2`, string(raw), question["id"]); err != nil {
					t.Fatal(err)
				}
				legacy := projectMemory(t, origin, projectID)
				if len(legacy["openQuestions"].([]any)) != scenario.open {
					t.Fatal("legacy question status changed for Unicode answer evidence")
				}
				found := false
				for _, item := range legacy["applicableRecords"].([]any) {
					record := item.(map[string]any)
					if record["id"] == question["id"] {
						found = record["payload"].(map[string]any)["answer"] == scenario.answer
						if _, leaked := record["contextInlineAnswer"]; leaked {
							t.Fatal("internal header appeared in legacy record")
						}
					}
				}
				if !found {
					t.Fatal("legacy context changed the original answer payload")
				}
				status, compact := curationRequest(t, origin, "GET", "/projects/"+projectID+"/context/compact", nil)
				if status != 200 || compact["counts"].(map[string]any)["openQuestions"] != float64(scenario.open) {
					t.Fatal("compact count changed for Unicode answer evidence")
				}
				want := "answered"
				if scenario.open == 1 {
					want = "unanswered"
				}
				if compact["records"].([]any)[0].(map[string]any)["questionStatus"] != want {
					t.Fatal("source preview disagrees with question count")
				}
			}
		})
	}
}

func TestContextAcceptedAnswerKeepsHistoricalRenderingFallback(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, response := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Historical rendering"}, restoredToken)
			if status != 200 {
				t.Fatal("project failed")
			}
			projectID := response["project"].(map[string]any)["id"].(string)
			status, response = captureRequest(t, origin, projectID, "", map[string]any{"type": "qa", "title": "Question", "authority": "explicit", "payload": map[string]any{"question": "Where is the accepted evidence?"}})
			if status != 201 {
				t.Fatal("question failed")
			}
			questionID := response["record"].(map[string]any)["id"].(string)
			status, response = captureRequest(t, origin, projectID, "", map[string]any{"type": "finding", "title": "Separate evidence", "authority": "observed", "payload": map[string]any{"finding": "Synthetic answer evidence."}})
			if status != 201 {
				t.Fatal("evidence failed")
			}
			answer := response["record"].(map[string]any)
			status, _ = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": answer["id"], "toRecordId": questionID, "type": "answers", "authority": "explicit"})
			if status != 201 {
				t.Fatal("accepted answer failed")
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
			for _, scenario := range []struct {
				content any
				finding string
				open    int
			}{
				{"Synthetic rendered evidence.", "", 0},
				{nil, "Synthetic payload-only evidence.", 0},
				{"\u3000\u0085", "", 1},
				{"\u3000\u0085", "Synthetic historical fallback evidence.", 0},
			} {
				answer["content"] = scenario.content
				answer["payload"].(map[string]any)["finding"] = scenario.finding
				raw, _ := json.Marshal(answer)
				if _, err = db.Exec(`UPDATE entities SET data=$1 WHERE kind='record' AND id=$2`, string(raw), answer["id"]); err != nil {
					t.Fatal(err)
				}
				if len(projectMemory(t, origin, projectID)["openQuestions"].([]any)) != scenario.open {
					t.Fatal("accepted evidence lost historical content/rendering semantics")
				}
				status, compact := curationRequest(t, origin, "GET", "/projects/"+projectID+"/context/compact?limit=1", nil)
				if status != 200 || compact["counts"].(map[string]any)["openQuestions"] != float64(scenario.open) {
					t.Fatal("omitted question status lost accepted historical evidence")
				}
			}
		})
	}
}
