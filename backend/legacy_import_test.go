package backend_test

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestLegacyJSONLMapsAllTypesWithoutDiscardingDisabledHistory(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Disabled historical types"}, restoredToken)
			if status != 200 {
				t.Fatal("project fixture failed")
			}
			value := project["project"].(map[string]any)
			projectID := value["id"].(string)
			settings := value["settings"].(map[string]any)
			settings["enabledRecordTypes"] = []any{}
			status, _ = projectRequest(t, origin, "PATCH", "/"+projectID+"/settings", map[string]any{"settings": settings, "expectedVersion": 1}, restoredToken)
			if status != 200 {
				t.Fatal("type disable fixture failed")
			}
			rows := []string{}
			for _, kind := range []string{"decision", "adr", "business_decision", "finding", "qa", "failure", "constraint", "project_state"} {
				row := map[string]any{"id": "legacy_" + kind, "type": kind, "date": "2025-01-01", "decision": "Legacy main content", "answer": "Historical answer", "unknown": map[string]any{"retained": true}}
				if kind == "finding" {
					row["finding"] = "\nHistorical finding"
					delete(row, "decision")
				}
				encoded, _ := json.Marshal(row)
				rows = append(rows, string(encoded))
			}
			status, result := curationRequest(t, origin, "POST", "/import", map[string]any{"format": "jsonl", "projectId": projectID, "jsonl": strings.Join(rows, "\n")})
			if status != 200 || result["imported"] != float64(8) {
				t.Fatal("disabled historical types were discarded")
			}
			for kind, primary := range map[string]string{"decision": "decision", "adr": "decision", "business_decision": "decision", "finding": "finding", "qa": "question", "failure": "observed", "constraint": "constraint", "project_state": "state"} {
				status, detail := curationRequest(t, origin, "GET", "/records/legacy_"+kind, nil)
				if status != 200 {
					t.Fatal("typed history missing")
				}
				record := detail["record"].(map[string]any)
				payload := record["payload"].(map[string]any)
				want := "Legacy main content"
				if kind == "finding" {
					want = "\nHistorical finding"
					if record["title"] != "" {
						t.Fatal("legacy title changed empty first prose line")
					}
				}
				if record["type"] != kind || payload[primary] != want || payload["legacyOriginal"].(map[string]any)["unknown"].(map[string]any)["retained"] != true {
					t.Fatal("type mapping changed source values")
				}
			}
		})
	}
}

func TestLegacyJSONLCollisionKeepsIndependentProjectHistory(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			ids := []string{}
			for _, name := range []string{"First history", "Other history"} {
				status, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": name}, restoredToken)
				if status != 200 {
					t.Fatal("project fixture failed")
				}
				ids = append(ids, project["project"].(map[string]any)["id"].(string))
			}
			row := `{"id":"SHARED","date":"2025-01-01","decision":"Keep each project's evidence"}`
			status, _ := curationRequest(t, origin, "POST", "/import", map[string]any{"format": "jsonl", "projectId": ids[0], "jsonl": row})
			if status != 200 {
				t.Fatal("first project import failed")
			}
			input := map[string]any{"format": "jsonl", "projectId": ids[1], "jsonl": row}
			status, result := curationRequest(t, origin, "POST", "/import", input)
			if status != 200 || result["imported"] != float64(1) {
				t.Fatal("colliding source was discarded")
			}
			mapped := result["records"].([]any)[0].(map[string]any)["recordId"].(string)
			if mapped == "SHARED" {
				t.Fatal("existing project history overwritten")
			}
			codes := map[string]bool{}
			for _, item := range result["warnings"].([]any) {
				codes[item.(map[string]any)["code"].(string)] = true
			}
			if !codes["ID_COLLISION"] || !codes["ID_NOT_PRESERVED"] {
				t.Fatal("collision provenance warnings missing")
			}
			status, detail := curationRequest(t, origin, "GET", "/records/SHARED", nil)
			if status != 200 || detail["record"].(map[string]any)["projectId"] != ids[0] {
				t.Fatal("original project history changed")
			}
			status, detail = curationRequest(t, origin, "GET", "/records/"+mapped, nil)
			if status != 200 || detail["record"].(map[string]any)["projectId"] != ids[1] {
				t.Fatal("mapped record lost project identity")
			}
			status, result = curationRequest(t, origin, "POST", "/import", input)
			if status != 200 || result["imported"] != float64(0) || result["records"].([]any)[0].(map[string]any)["recordId"] != mapped {
				t.Fatal("collision replay duplicated history")
			}
		})
	}
}

func TestLegacyJSONLRollsBackLateImportAuditFailure(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Late import failure"}, restoredToken)
			if status != 200 {
				t.Fatal("project fixture failed")
			}
			projectID := project["project"].(map[string]any)["id"].(string)
			status, before := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("export failed")
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
			fault := `CREATE TRIGGER legacy_fault BEFORE INSERT ON entities WHEN NEW.kind='audit' AND json_extract(NEW.data,'$.action')='data.imported' BEGIN SELECT RAISE(ABORT,'synthetic-private-fault'); END`
			if engine == "postgres" {
				fault = `CREATE FUNCTION legacy_fault_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='audit' AND NEW.data->>'action'='data.imported' THEN RAISE EXCEPTION 'synthetic-private-fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER legacy_fault BEFORE INSERT ON entities FOR EACH ROW EXECUTE FUNCTION legacy_fault_fn()`
			}
			if _, err = db.Exec(fault); err != nil {
				t.Fatal(err)
			}
			status, result := curationRequest(t, origin, "POST", "/import", map[string]any{"format": "jsonl", "projectId": projectID, "jsonl": "{\"id\":\"old\",\"decision\":\"Old\"}\n{\"id\":\"new\",\"decision\":\"New\",\"supersedes\":\"old\",\"evidence\":{\"synthetic\":true}}"})
			encoded, _ := json.Marshal(result)
			if status != 503 || strings.Contains(string(encoded), "synthetic-private-fault") {
				t.Fatal("late import failure not safe")
			}
			status, after := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 || !reflect.DeepEqual(before["data"], after["data"]) {
				t.Fatal("late failure retained partial source records, relationships or evidence")
			}
		})
	}
}

func TestLegacyJSONLWarnsForSparseAndUnsupportedClaimsWithoutInventingValues(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	status, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Sparse source"}, restoredToken)
	if status != 200 {
		t.Fatal("project fixture failed")
	}
	projectID := project["project"].(map[string]any)["id"].(string)
	line := `{"id":"SPARSE","type":"chat","decision":"Do not replace explicit null","payload":{"decision":null,"rationale":[]},"authority":"owner","confidence":"certain","date":"2025-02-03T08:15:00,123Z","gitContext":{"branch":"historical"},"unknown":{"preserved":true}}`
	status, result := curationRequest(t, origin, "POST", "/import", map[string]any{"format": "jsonl", "projectId": projectID, "jsonl": line + "\n{}\n[]\nnull"})
	if status != 200 || result["imported"] != float64(2) || result["skipped"] != float64(2) {
		t.Fatal("sparse or malformed source was mishandled")
	}
	codes := map[string]bool{}
	for _, item := range result["warnings"].([]any) {
		codes[item.(map[string]any)["code"].(string)] = true
	}
	for _, code := range []string{"UNSUPPORTED_RECORD_TYPE", "MISSING_CONTENT", "MISSING_RATIONALE", "UNVERIFIED_LEGACY_PROVENANCE", "UNSUPPORTED_DATE", "MISSING_DATE", "MISSING_ID", "INVALID_JSON"} {
		if !codes[code] {
			t.Fatalf("missing sparse warning %s", code)
		}
	}
	status, detail := curationRequest(t, origin, "GET", "/records/SPARSE", nil)
	if status != 200 {
		t.Fatal("sparse capture not retained")
	}
	record := detail["record"].(map[string]any)
	payload := record["payload"].(map[string]any)
	if record["type"] != "decision" || record["authority"] != nil || record["confidence"] != "unknown" || record["happenedAt"] != nil || payload["decision"] != nil || len(payload["rationale"].([]any)) != 0 {
		t.Fatal("unsupported claims became invented values")
	}
	var original map[string]any
	json.Unmarshal([]byte(line), &original)
	if !reflect.DeepEqual(payload["legacyOriginal"], original) || record["gitContext"].(map[string]any)["branch"] != "historical" {
		t.Fatal("sparse source evidence lost")
	}
}

func TestLegacyJSONLInvalidRequestsDoNotWriteKnowledge(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	status, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Invalid requests"}, restoredToken)
	if status != 200 {
		t.Fatal("project fixture failed")
	}
	projectID := project["project"].(map[string]any)["id"].(string)
	status, before := curationRequest(t, origin, "POST", "/export", map[string]any{})
	if status != 200 {
		t.Fatal("export failed")
	}
	for _, test := range []struct {
		field  string
		value  any
		status int
	}{{"jsonl", false, 400}, {"sourceName", "", 400}, {"sourceName", strings.Repeat("a", 1001), 400}, {"source", map[string]any{"filename": float64(7)}, 400}, {"projectId", "../unsafe", 400}, {"projectId", "proj_missing", 404}} {
		body := map[string]any{"format": "jsonl", "projectId": projectID, "jsonl": `{"id":"VALID","decision":"Should not be partially stored"}`}
		body[test.field] = test.value
		status, _ := curationRequest(t, origin, "POST", "/import", body)
		if status != test.status {
			t.Fatalf("invalid %s accepted: %d", test.field, status)
		}
		status, after := curationRequest(t, origin, "POST", "/export", map[string]any{})
		if status != 200 || !reflect.DeepEqual(before["data"], after["data"]) {
			t.Fatal("invalid request changed knowledge")
		}
	}
}

func TestLegacyJSONLRejectsTextAbove32MiBLimit(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	status, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Legacy size boundary"}, restoredToken)
	if status != 200 {
		t.Fatal("project fixture failed")
	}
	projectID := project["project"].(map[string]any)["id"].(string)
	status, before := curationRequest(t, origin, "POST", "/export", map[string]any{})
	if status != 200 {
		t.Fatal("export failed")
	}
	status, _ = curationRequest(t, origin, "POST", "/import", map[string]any{"format": "jsonl", "projectId": projectID, "jsonl": strings.Repeat(" ", 32*1024*1024+1)})
	if status != 400 {
		t.Fatalf("oversized JSONL text accepted: %d", status)
	}
	status, after := curationRequest(t, origin, "POST", "/export", map[string]any{})
	if status != 200 || !reflect.DeepEqual(before["data"], after["data"]) {
		t.Fatal("oversized JSONL text changed knowledge")
	}
}

func TestLegacyJSONLRetainsSourceEvidenceAndUnverifiedAuthority(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Legacy history"}, restoredToken)
			if status != 200 {
				t.Fatal("project fixture failed")
			}
			projectID := project["project"].(map[string]any)["id"].(string)
			first := `  {"id":"D001","date":"2025-01-02","recordType":"decision","title":"Retain original evidence","decision":["Keep public metadata.","Separate measurements from claims."],"why":["Allows later verification."],"status":"superseded_or_partial","extraSections":{"Evidence":["Synthetic transcript"]},"actor":{"kind":"system","credentialFingerprint":"untrusted-source"}}`
			second := `{"id":"F002","type":"finding","finding":"A synthetic observation.","happenedAt":"2025-02-03T08:15:00+02:00","authority":"observed","confidence":"high","evidence":{"build":"passed","review":"pending"}}`
			input := map[string]any{"format": "jsonl", "projectId": projectID, "source": map[string]any{"filename": "historical.jsonl"}, "jsonl": first + "\r\n\r\nnot-json\r\n" + second}
			status, result := curationRequest(t, origin, "POST", "/import", input)
			if status != 200 || result["imported"] != float64(2) || result["skipped"] != float64(1) {
				t.Fatalf("legacy import failed: %d %+v", status, result)
			}
			status, detail := curationRequest(t, origin, "GET", "/records/D001", nil)
			if status != 200 {
				t.Fatal("imported record unavailable")
			}
			record := detail["record"].(map[string]any)
			payload := record["payload"].(map[string]any)
			var original map[string]any
			json.Unmarshal([]byte(first), &original)
			if !reflect.DeepEqual(payload["legacyOriginal"], original) || payload["decision"] != "Keep public metadata.\nSeparate measurements from claims." || payload["rationale"] != "Allows later verification." {
				t.Fatal("legacy payload lost original fields or array prose")
			}
			if record["authority"] != nil || record["confidence"] != "unknown" || record["happenedAt"] != "2025-01-02T00:00:00.000Z" {
				t.Fatal("legacy source was promoted or its date changed")
			}
			provenance := record["provenance"].(map[string]any)["import"].(map[string]any)
			if provenance["rawLine"] != first || provenance["sourceName"] != "historical.jsonl" || provenance["line"] != float64(1) || provenance["authorityVerified"] != false || provenance["datePrecision"] != "day" {
				t.Fatal("legacy provenance not preserved")
			}
			actor := record["actor"].(map[string]any)
			if actor["kind"] != "import" || actor["displayName"] != "historical.jsonl" || actor["credentialFingerprint"] != "credential_restored" {
				t.Fatal("untrusted source actor replaced authenticated provenance")
			}
			if detail["metadata"].(map[string]any)["legacyStatus"] != "superseded_or_partial" || len(detail["audit"].([]any)) != 1 {
				t.Fatal("historical lifecycle or import audit missing")
			}
			status, detail = curationRequest(t, origin, "GET", "/records/F002", nil)
			if status != 200 || detail["record"].(map[string]any)["happenedAt"] != "2025-02-03T06:15:00.000Z" || len(detail["evidence"].([]any)) != 1 {
				t.Fatal("offset date or original evidence missing")
			}
			codes := map[string]bool{}
			for _, warning := range result["warnings"].([]any) {
				codes[warning.(map[string]any)["code"].(string)] = true
			}
			if !codes["INVALID_JSON"] || !codes["UNVERIFIED_LEGACY_PROVENANCE"] {
				t.Fatal("legacy warnings missing")
			}
			status, result = searchRecords(t, origin, "q=measurements")
			if status != 200 || len(result["records"].([]any)) != 1 {
				t.Fatal("legacy source not searchable")
			}
			status, result = curationRequest(t, origin, "POST", "/import", input)
			if status != 200 || result["imported"] != float64(0) || result["skipped"] != float64(3) {
				t.Fatal("identical JSONL replay duplicated captures")
			}
			status, result = curationRequest(t, origin, "POST", "/import", map[string]any{"format": "jsonl", "projectId": projectID, "jsonl": strings.Replace(first, "Retain original evidence", "Changed evidence", 1)})
			if status != 200 || result["imported"] != float64(0) || result["records"].([]any)[0].(map[string]any)["status"] != "conflict" {
				t.Fatal("changed legacy ID was overwritten")
			}
		})
	}
}

func TestLegacyJSONLAnonymousIdentityMatchesEstablishedCanonicalHashes(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			first := `{"decision":"Keep duplicate evidence.","date":"2025-02-03","Z":{"é":1,"a":2},"a":"<source>"}`
			second := `{"a":"<source>","Z":{"a":2,"é":1},"date":"2025-02-03","decision":"Keep duplicate evidence."}` + "\r"
			input := map[string]any{"format": "jsonl", "projectId": "proj_legacy", "sourceName": "history.jsonl", "jsonl": first + "\r\n" + second}
			wantIDs := []string{"rec_import_a17e2e2e6266484ce046f56d95b26d62", "rec_import_1fcf34eb0bf2102465747988c0ce6655"}
			for _, phase := range []string{"initial", "restart"} {
				t.Run(phase, func(t *testing.T) {
					origin := startServer(t, path, databaseURL)
					if phase == "initial" {
						project := portableArchive()["data"].(map[string]any)["project"].([]any)[0].(map[string]any)
						project["id"] = "proj_legacy"
						status, _ := curationRequest(t, origin, "POST", "/import", map[string]any{"format": "scratchpad", "version": 1, "data": map[string]any{"project": []any{project}}})
						if status != 200 {
							t.Fatal("fixed project fixture failed")
						}
					}
					status, result := curationRequest(t, origin, "POST", "/import", input)
					wantImported := float64(2)
					if phase == "restart" {
						wantImported = 0
					}
					if status != 200 || result["imported"] != wantImported {
						t.Fatal("stable source import/replay failed")
					}
					for index, entry := range result["records"].([]any) {
						if entry.(map[string]any)["recordId"] != wantIDs[index] {
							t.Fatal("canonical identity differs from established TypeScript vector")
						}
					}
					for index, id := range wantIDs {
						status, detail := curationRequest(t, origin, "GET", "/records/"+id, nil)
						if status != 200 {
							t.Fatal("anonymous capture missing")
						}
						provenance := detail["record"].(map[string]any)["provenance"].(map[string]any)["import"].(map[string]any)
						if provenance["sha256"] != "1f289dcd75a091120e1919707ee3940191a2559800b8f067463bcbaf760c91b1" {
							t.Fatal("source fingerprint differs from established vector")
						}
						wantLine := first
						if index == 1 {
							wantLine = second
						}
						if provenance["rawLine"] != wantLine {
							t.Fatal("raw line was rewritten")
						}
					}
				})
			}
			origin := startServer(t, path, databaseURL)
			status, archive := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("native export failed")
			}
			path, databaseURL = portableInstance(t, engine)
			destination := startServer(t, path, databaseURL)
			status, _ = curationRequest(t, destination, "POST", "/import", archive)
			if status != 200 {
				t.Fatal("legacy native round trip failed")
			}
			status, result := curationRequest(t, destination, "POST", "/import", input)
			if status != 200 || result["imported"] != float64(0) || result["skipped"] != float64(2) {
				t.Fatal("native restore lost JSONL retry identity")
			}
		})
	}
}

func TestLegacyJSONLRelationshipsRemainSuggestionsUntilReviewed(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Historical links"}, restoredToken)
			if status != 200 {
				t.Fatal("project fixture failed")
			}
			projectID := project["project"].(map[string]any)["id"].(string)
			lines := []string{
				`{"id":"OLD","decision":"Original choice","date":"2025-01-01","supersededBy":"../NEW","authority":"explicit"}`,
				`{"id":"../NEW","decision":"Later source claim","date":"2025-01-02","supersedes":["OLD","../NEW","missing",{"unknown":"reference"}]}`,
				`{"id":"STATE","type":"project_state","state":"paused","reason":"Awaiting synthetic evidence","date":"2025-01-03","supersession":{"notes":["Review needed"]}}`,
			}
			input := map[string]any{"format": "jsonl", "projectId": projectID, "sourceName": "links.jsonl", "jsonl": strings.Join(lines, "\n")}
			status, result := curationRequest(t, origin, "POST", "/import", input)
			if status != 200 || result["imported"] != float64(3) {
				t.Fatal("historical link fixture failed")
			}
			newID := result["records"].([]any)[1].(map[string]any)["recordId"].(string)
			if newID == "../NEW" || !strings.HasPrefix(newID, "rec_import_") {
				t.Fatal("unsafe source ID not mapped")
			}
			codes := map[string]bool{}
			for _, item := range result["warnings"].([]any) {
				codes[item.(map[string]any)["code"].(string)] = true
			}
			for _, code := range []string{"ID_NOT_PRESERVED", "INVALID_RELATIONSHIP", "UNRESOLVED_RELATIONSHIP", "SUPERSESSION_REQUIRES_REVIEW"} {
				if !codes[code] {
					t.Fatalf("missing warning %s", code)
				}
			}
			status, detail := curationRequest(t, origin, "GET", "/records/OLD", nil)
			if status != 200 || len(detail["relationships"].([]any)) != 1 {
				t.Fatal("historical replacement suggestion missing or duplicated")
			}
			link := detail["relationships"].([]any)[0].(map[string]any)
			if link["fromRecordId"] != newID || link["toRecordId"] != "OLD" || link["authority"] != "suggested" || link["status"] != "suggested" {
				t.Fatal("import promoted an unverified relationship")
			}
			status, memory := curationRequest(t, origin, "GET", "/projects/"+projectID+"/context", nil)
			if status != 200 || memory["currentState"] != nil || len(memory["requiresReview"].([]any)) != 3 {
				t.Fatal("historical state became current authority")
			}
			status, result = curationRequest(t, origin, "POST", "/import", input)
			if status != 200 || result["imported"] != float64(0) {
				t.Fatal("suggestion replay duplicated captures")
			}
			status, detail = curationRequest(t, origin, "GET", "/records/OLD", nil)
			if status != 200 || len(detail["relationships"].([]any)) != 1 || len(detail["audit"].([]any)) != 2 {
				t.Fatal("replay duplicated relationship audit")
			}
			status, result = curationRequest(t, origin, "POST", "/relationships/"+link["id"].(string)+"/accept", map[string]any{"expectedVersion": 1})
			if status != 200 || result["relationship"].(map[string]any)["authority"] != "explicit" {
				t.Fatal("owner could not review imported suggestion")
			}
		})
	}
}
