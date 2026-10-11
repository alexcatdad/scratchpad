package backend_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func curationRequest(t *testing.T, origin, method, path string, body any) (int, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, origin+"/api/v1"+path, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+restoredToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var result map[string]any
	if json.NewDecoder(res.Body).Decode(&result) != nil {
		t.Fatalf("curation returned non-JSON: %d", res.StatusCode)
	}
	return res.StatusCode, result
}
func curationCapture(t *testing.T, origin, projectID, title string) map[string]any {
	t.Helper()
	status, body := captureRequest(t, origin, projectID, "", map[string]any{"type": "decision", "title": title, "authority": "explicit", "payload": map[string]any{"decision": "Retain raw history."}})
	if status != 201 {
		t.Fatal("curation fixture capture failed")
	}
	return body["record"].(map[string]any)
}
func TestMetadataAmendmentPreservesRawCapture(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Curation"}, restoredToken)
			projectID := project["project"].(map[string]any)["id"].(string)
			raw := curationCapture(t, origin, projectID, "Original title")
			id := raw["id"].(string)
			status, body := curationRequest(t, origin, "PATCH", "/records/"+id+"/metadata", map[string]any{"displayTitle": "Curated title", "tags": []string{"reviewed"}, "archived": true, "expectedVersion": 1})
			if status != 200 {
				t.Fatalf("metadata amendment unavailable: %d", status)
			}
			if body["metadata"].(map[string]any)["version"] != float64(2) || body["revision"].(map[string]any)["revisionNumber"] != float64(2) {
				t.Fatal("metadata revision did not share its version")
			}
			status, detail := curationRequest(t, origin, "GET", "/records/"+id, nil)
			if status != 200 || !reflect.DeepEqual(detail["record"], raw) || len(detail["revisions"].([]any)) != 1 || len(detail["audit"].([]any)) != 2 {
				t.Fatal("curation changed raw capture or lost its amendment history")
			}
			status, _ = curationRequest(t, origin, "PATCH", "/records/"+id+"/metadata", map[string]any{"tags": []string{"stale"}, "expectedVersion": 1})
			if status != 409 {
				t.Fatal("stale metadata update was accepted")
			}
			status, _ = curationRequest(t, origin, "PATCH", "/records/"+id+"/metadata", map[string]any{"content": "Rewritten", "expectedVersion": 2})
			if status != 400 {
				t.Fatal("metadata allowed raw content mutation")
			}
			status, body = curationRequest(t, origin, "POST", "/records/"+id+"/revisions", map[string]any{"changes": map[string]any{"curatedSummary": "Owner-reviewed summary"}, "reason": "Synthetic review", "expectedVersion": 2})
			if status != 200 || body["revision"].(map[string]any)["reason"] != "Synthetic review" {
				t.Fatal("explicit revision lost review provenance")
			}
		})
	}
}

func TestEvidenceAndMirrorReportsRetainProvenanceAndConsent(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Evidence"}, restoredToken)
			projectID := project["project"].(map[string]any)["id"].(string)
			raw := curationCapture(t, origin, projectID, "Evidence capture")
			id := raw["id"].(string)
			status, body := curationRequest(t, origin, "POST", "/records/"+id+"/evidence", map[string]any{"kind": "test", "reference": "synthetic test run", "description": "Observed validation", "revision": "abc123"})
			if status != 201 || body["evidence"].(map[string]any)["recordId"] != id {
				t.Fatalf("evidence creation unavailable: %d", status)
			}
			report := map[string]any{"attempted": true, "succeeded": false, "path": "docs/memory", "error": "Synthetic mirror failure"}
			status, _ = curationRequest(t, origin, "POST", "/records/"+id+"/mirror", report)
			if status != 403 {
				t.Fatal("mirror report bypassed disabled project permission")
			}
			settings := map[string]any{"repoMirroring": map[string]any{"enabled": true, "recordTypes": []string{"decision"}}, "crossProjectAnalysis": true, "aiProcessing": false}
			projectRequest(t, origin, "PATCH", "/"+projectID+"/settings", map[string]any{"settings": settings, "expectedVersion": 1}, restoredToken)
			status, body = curationRequest(t, origin, "POST", "/records/"+id+"/mirror", report)
			if status != 200 || body["mirror"].(map[string]any)["version"] != float64(1) {
				t.Fatal("allowed mirror report did not persist")
			}
			report["succeeded"] = true
			delete(report, "error")
			status, body = curationRequest(t, origin, "POST", "/records/"+id+"/mirror", report)
			if status != 200 || body["mirror"].(map[string]any)["version"] != float64(2) || body["mirror"].(map[string]any)["error"] != "Synthetic mirror failure" {
				t.Fatal("mirror update changed retained optional fields")
			}
			_, detail := curationRequest(t, origin, "GET", "/records/"+id, nil)
			if !reflect.DeepEqual(detail["record"], raw) || len(detail["evidence"].([]any)) != 1 || len(detail["audit"].([]any)) != 4 {
				t.Fatal("evidence/mirroring changed raw history or lost audit")
			}
		})
	}
}

func TestRelationshipAcceptanceChangesApplicabilityWithoutRewritingHistory(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Relationships"}, restoredToken)
			projectID := project["project"].(map[string]any)["id"].(string)
			old := curationCapture(t, origin, projectID, "Old decision")
			next := curationCapture(t, origin, projectID, "New decision")
			status, body := curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": next["id"], "toRecordId": old["id"], "type": "replaces", "authority": "suggested", "note": "Synthetic replacement"})
			if status != 201 {
				t.Fatalf("relationship creation unavailable: %d", status)
			}
			link := body["relationship"].(map[string]any)
			if link["status"] != "suggested" {
				t.Fatal("suggestion was promoted automatically")
			}
			id := link["id"].(string)
			if len(projectMemory(t, origin, projectID)["historicalRecords"].([]any)) != 0 {
				t.Fatal("unaccepted suggestion changed applicability")
			}
			status, body = curationRequest(t, origin, "POST", "/relationships/"+id+"/accept", map[string]any{"expectedVersion": 1})
			if status != 200 || body["relationship"].(map[string]any)["authority"] != "explicit" {
				t.Fatal("acceptance did not preserve owner authority")
			}
			if len(projectMemory(t, origin, projectID)["historicalRecords"].([]any)) != 1 {
				t.Fatal("accepted replacement did not change applicability")
			}
			_, detail := curationRequest(t, origin, "GET", "/records/"+old["id"].(string), nil)
			if !reflect.DeepEqual(detail["record"], old) || len(detail["relationships"].([]any)) != 1 || len(detail["audit"].([]any)) != 3 {
				t.Fatal("replacement rewrote capture or lost relationship audit")
			}
			status, _ = curationRequest(t, origin, "POST", "/relationships/"+id+"/reject", map[string]any{"expectedVersion": 1})
			if status != 409 {
				t.Fatal("stale relationship update was accepted")
			}
			status, _ = curationRequest(t, origin, "POST", "/relationships/"+id+"/reject", map[string]any{"expectedVersion": 2})
			if status != 200 || len(projectMemory(t, origin, projectID)["historicalRecords"].([]any)) != 0 {
				t.Fatal("rejected replacement remained applicable")
			}
		})
	}
}

func TestRelationshipCyclesFailBeforeAuthorityChanges(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Cycles"}, restoredToken)
			projectID := project["project"].(map[string]any)["id"].(string)
			a := curationCapture(t, origin, projectID, "A")["id"].(string)
			b := curationCapture(t, origin, projectID, "B")["id"].(string)
			c := curationCapture(t, origin, projectID, "C")["id"].(string)
			for _, pair := range [][2]string{{a, b}, {b, c}} {
				status, _ := curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": pair[0], "toRecordId": pair[1], "type": "depends_on"})
				if status != 201 {
					t.Fatal("acyclic dependency rejected")
				}
			}
			status, body := curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": c, "toRecordId": a, "type": "depends_on"})
			if status != 409 || body["error"].(map[string]any)["code"] != "RELATIONSHIP_CYCLE" {
				t.Fatal("accepted dependency cycle was not rejected")
			}
			status, body = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": c, "toRecordId": a, "type": "depends_on", "authority": "suggested"})
			if status != 201 {
				t.Fatal("cycle evidence could not remain a suggestion")
			}
			id := body["relationship"].(map[string]any)["id"].(string)
			status, _ = curationRequest(t, origin, "POST", "/relationships/"+id+"/accept", map[string]any{"expectedVersion": 1})
			if status != 409 {
				t.Fatal("suggestion acceptance bypassed cycle protection")
			}
			_, detail := curationRequest(t, origin, "GET", "/records/"+c, nil)
			found := false
			for _, value := range detail["relationships"].([]any) {
				link := value.(map[string]any)
				if link["id"] == id {
					found = link["status"] == "suggested" && link["authority"] == "suggested" && link["version"] == float64(1)
				}
			}
			if !found {
				t.Fatal("failed acceptance changed relationship authority or version")
			}
			status, _ = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": a, "toRecordId": a, "type": "related_to"})
			if status != 400 {
				t.Fatal("self-relationship accepted")
			}
		})
	}
}

func TestConcurrentReplacementCannotCreateCycle(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Concurrent links"}, restoredToken)
			projectID := project["project"].(map[string]any)["id"].(string)
			a := curationCapture(t, origin, projectID, "A")["id"].(string)
			b := curationCapture(t, origin, projectID, "B")["id"].(string)
			codes := make(chan int, 2)
			failures := make(chan error, 2)
			for _, pair := range [][2]string{{a, b}, {b, a}} {
				go func() {
					data, _ := json.Marshal(map[string]any{"fromRecordId": pair[0], "toRecordId": pair[1], "type": "partially_replaces"})
					req, _ := http.NewRequest("POST", origin+"/api/v1/relationships", bytes.NewReader(data))
					req.Header.Set("Authorization", "Bearer "+restoredToken)
					res, err := http.DefaultClient.Do(req)
					if err != nil {
						failures <- err
						return
					}
					res.Body.Close()
					codes <- res.StatusCode
				}()
			}
			counts := map[int]int{}
			for range 2 {
				select {
				case code := <-codes:
					counts[code]++
				case err := <-failures:
					t.Fatal(err)
				}
			}
			if counts[201] != 1 || counts[409] != 1 {
				t.Fatal("concurrent graph writes bypassed cycle protection", counts)
			}
		})
	}
}
func TestFailedCurationKeepsMetadataVersionAndHistory(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Atomic amendment"}, restoredToken)
			projectID := project["project"].(map[string]any)["id"].(string)
			record := curationCapture(t, origin, projectID, "Immutable")
			id := record["id"].(string)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			fault := `CREATE TRIGGER curation_fault BEFORE INSERT ON entities WHEN NEW.kind='audit' AND json_extract(NEW.data,'$.action')='record.curated' BEGIN SELECT RAISE(ABORT,'synthetic-private-fault'); END`
			if engine == "postgres" {
				fault = `CREATE FUNCTION curation_fault_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='audit' AND NEW.data->>'action'='record.curated' THEN RAISE EXCEPTION 'synthetic-private-fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER curation_fault BEFORE INSERT ON entities FOR EACH ROW EXECUTE FUNCTION curation_fault_fn()`
			}
			if _, err = db.Exec(fault); err != nil {
				t.Fatal(err)
			}
			status, body := curationRequest(t, origin, "PATCH", "/records/"+id+"/metadata", map[string]any{"archived": true, "expectedVersion": 1})
			data, _ := json.Marshal(body)
			if status != 503 || strings.Contains(string(data), "synthetic-private-fault") {
				t.Fatal("curation failure exposed fault details")
			}
			_, detail := curationRequest(t, origin, "GET", "/records/"+id, nil)
			if detail["metadata"].(map[string]any)["version"] != float64(1) || detail["metadata"].(map[string]any)["archived"] != false || len(detail["revisions"].([]any)) != 0 || len(detail["audit"].([]any)) != 1 {
				t.Fatal("failed curation left partial amendment state")
			}
		})
	}
}
