package backend_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

type archivePadding struct{}

var archivePaddingBlock = strings.Repeat(" ", 64*1024)

func (archivePadding) Read(buffer []byte) (int, error) {
	return copy(buffer, archivePaddingBlock), nil
}

func TestNativeImportEnforcesStreaming64MiBLimit(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	body := io.MultiReader(strings.NewReader(`{"format":"scratchpad","version":1,"data":{}}`), io.LimitReader(archivePadding{}, 64*1024*1024))
	request, _ := http.NewRequest("POST", origin+"/api/v1/import", body)
	request.Header.Set("Authorization", "Bearer "+restoredToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if json.NewDecoder(response.Body).Decode(&result) != nil {
		t.Fatal("invalid limit response")
	}
	if response.StatusCode != 413 || result["error"].(map[string]any)["code"] != "VALIDATION_FAILED" {
		t.Fatalf("streaming limit contract changed: %d %+v", response.StatusCode, result)
	}
	status, exported := curationRequest(t, origin, "POST", "/export", map[string]any{})
	if status != 200 {
		t.Fatal("server unavailable after oversized request")
	}
	for kind, entries := range exported["data"].(map[string]any) {
		if len(entries.([]any)) != 0 {
			t.Fatalf("oversized request retained %s", kind)
		}
	}
}

func portableInstance(t *testing.T, engine string) (string, string) {
	t.Helper()
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
	if _, err = db.Exec(`DELETE FROM entities WHERE kind NOT IN ('credential','session')`); err != nil {
		t.Fatal(err)
	}
	return path, databaseURL
}

func TestNativeExportImportsAcrossDatabaseEngines(t *testing.T) {
	for _, engines := range [][2]string{{"sqlite", "postgres"}, {"postgres", "sqlite"}} {
		t.Run(engines[0]+"-to-"+engines[1], func(t *testing.T) {
			path, databaseURL := portableInstance(t, engines[0])
			source := startServer(t, path, databaseURL)
			status, result := projectRequest(t, source, "POST", "/resolve-explicit", map[string]any{"name": "Live portable project"}, restoredToken)
			if status != 200 {
				t.Fatal("project fixture failed")
			}
			projectID := result["project"].(map[string]any)["id"].(string)
			record := curationCapture(t, source, projectID, "Live portable capture")
			status, result = curationRequest(t, source, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("export failed")
			}
			path, databaseURL = portableInstance(t, engines[1])
			target := startServer(t, path, databaseURL)
			status, imported := curationRequest(t, target, "POST", "/import", result)
			if status != 200 {
				t.Fatalf("export not importable: %d %+v", status, imported)
			}
			status, detail := curationRequest(t, target, "GET", "/records/"+record["id"].(string), nil)
			if status != 200 || !reflect.DeepEqual(detail["record"], record) {
				t.Fatal("cross-engine import rewrote raw capture")
			}
		})
	}
}

func TestNativeImportRejectsArchivesAtomically(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			cases := []struct {
				name   string
				change func(map[string]any)
				status int
			}{
				{"unknown-private-section", func(data map[string]any) { data["credential"] = []any{} }, 400},
				{"unsafe-id", func(data map[string]any) { data["record"].([]any)[0].(map[string]any)["id"] = "../escape" }, 400},
				{"unsafe-reference", func(data map[string]any) { data["revision"].([]any)[0].(map[string]any)["recordId"] = "rec%2Fother" }, 400},
				{"missing-metadata", func(data map[string]any) { delete(data, "metadata") }, 400},
				{"late-missing-record", func(data map[string]any) { data["evidence"].([]any)[0].(map[string]any)["recordId"] = "rec_missing" }, 404},
				{"metadata-identity", func(data map[string]any) { data["metadata"].([]any)[0].(map[string]any)["id"] = "meta_other" }, 400},
				{"provider-secret-in-settings", func(data map[string]any) {
					data["settings"] = []any{map[string]any{"id": "global", "version": float64(1), "createdAt": "2026-10-01T00:00:00.000Z", "apiKey": "synthetic-provider-secret"}}
				}, 400},
			}
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					archive := portableArchive()
					test.change(archive["data"].(map[string]any))
					status, _ := curationRequest(t, origin, "POST", "/import", archive)
					if status != test.status {
						t.Fatalf("invalid archive status: %d", status)
					}
					status, exported := curationRequest(t, origin, "POST", "/export", map[string]any{})
					if status != 200 {
						t.Fatal("export failed")
					}
					for kind, entries := range exported["data"].(map[string]any) {
						if len(entries.([]any)) != 0 {
							t.Fatalf("failed import retained %s", kind)
						}
					}
				})
			}
		})
	}
}

func TestNativeImportPreservesHistoricalVersionsAndUTCTimestamps(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	archive := portableArchive()
	data := archive["data"].(map[string]any)
	data["record"].([]any)[0].(map[string]any)["recordedAt"] = "2026-10-01T00:00Z"
	data["profile"].([]any)[0].(map[string]any)["displayName"] = "Portable owner" + strings.Repeat(" ", 201)
	for _, kind := range []string{"record", "metadata"} {
		value := data[kind].([]any)[0].(map[string]any)
		value["version"], value["createdAt"] = float64(19), "2026-10-01T00:00Z"
	}
	status, result := curationRequest(t, origin, "POST", "/import", archive)
	if status != 200 {
		t.Fatalf("historical UTC timestamp rejected: %d %+v", status, result)
	}
	status, detail := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
	if status != 200 || detail["record"].(map[string]any)["version"] != float64(19) || detail["metadata"].(map[string]any)["version"] != float64(19) {
		t.Fatal("historical versions were reset")
	}
	status, exported := curationRequest(t, origin, "POST", "/export", map[string]any{})
	if status != 200 || !reflect.DeepEqual(exported["data"].(map[string]any)["profile"], data["profile"]) {
		t.Fatal("import trimmed original profile history")
	}
}

func TestNativeImportRejectsConflictsAndCyclesWithoutPartialWrites(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			archive := portableArchive()
			status, _ := curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 {
				t.Fatal("initial restore failed")
			}
			status, before := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("export failed")
			}
			for _, mode := range []string{"immutable-conflict", "source-conflict", "replacement-cycle"} {
				t.Run(mode, func(t *testing.T) {
					changed := portableArchive()
					data := changed["data"].(map[string]any)
					// The new project is inserted before the later error; it must roll back.
					project := data["project"].([]any)[0].(map[string]any)
					project["id"] = "proj_new"
					data["project"] = []any{project}
					switch mode {
					case "immutable-conflict":
						data["record"].([]any)[0].(map[string]any)["content"] = "Overwrite forbidden"
					case "source-conflict":
						source := data["source"].([]any)[0].(map[string]any)
						source["id"], source["projectId"] = "source_new", "proj_new"
					case "replacement-cycle":
						encoded, _ := json.Marshal(data["record"].([]any)[0])
						var record map[string]any
						json.Unmarshal(encoded, &record)
						record["id"] = "rec_second"
						data["record"] = []any{record}
						data["metadata"] = []any{map[string]any{"id": "rec_second", "recordId": "rec_second", "version": float64(1), "createdAt": "2026-10-01T00:00:00Z", "tags": []any{}}}
						links := []any{}
						for i, pair := range [][2]string{{"rec:portable", "rec_second"}, {"rec_second", "rec:portable"}} {
							kind := "replaces"
							id := "rel_forward"
							if i == 1 {
								kind, id = "partially_replaces", "rel_back"
							}
							links = append(links, map[string]any{"id": id, "version": float64(1), "createdAt": "2026-10-01T00:00:00Z", "fromRecordId": pair[0], "toRecordId": pair[1], "type": kind, "status": "accepted", "authority": "explicit"})
						}
						data["relationship"] = links
					}
					status, _ := curationRequest(t, origin, "POST", "/import", changed)
					if status != 409 {
						t.Fatalf("unsafe import accepted: %d", status)
					}
					status, after := curationRequest(t, origin, "POST", "/export", map[string]any{})
					if status != 200 || !reflect.DeepEqual(before["data"], after["data"]) {
						t.Fatal("failed import partially changed knowledge")
					}
				})
			}
		})
	}
}

func TestNativeImportRollsBackLateAuditFailure(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			fault := `CREATE TRIGGER import_fault BEFORE INSERT ON entities WHEN NEW.kind='audit' AND json_extract(NEW.data,'$.action')='data.imported' BEGIN SELECT RAISE(ABORT,'synthetic-private-fault'); END`
			if engine == "postgres" {
				fault = `CREATE FUNCTION import_fault_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='audit' AND NEW.data->>'action'='data.imported' THEN RAISE EXCEPTION 'synthetic-private-fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER import_fault BEFORE INSERT ON entities FOR EACH ROW EXECUTE FUNCTION import_fault_fn()`
			}
			if _, err = db.Exec(fault); err != nil {
				t.Fatal(err)
			}
			db.Close()
			origin := startServer(t, path, databaseURL)
			status, result := curationRequest(t, origin, "POST", "/import", portableArchive())
			encoded, _ := json.Marshal(result)
			if status != 503 || strings.Contains(string(encoded), "synthetic-private-fault") {
				t.Fatal("late failure not safe")
			}
			status, result = curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("export failed")
			}
			for kind, entries := range result["data"].(map[string]any) {
				if len(entries.([]any)) != 0 {
					t.Fatalf("late failure retained %s", kind)
				}
			}
		})
	}
}

func portableArchive() map[string]any {
	entity := func(id string, fields map[string]any) map[string]any {
		fields["id"], fields["version"], fields["createdAt"] = id, float64(1), "2026-10-01T00:00:00.000Z"
		return fields
	}
	return map[string]any{"format": "scratchpad", "version": float64(1), "data": map[string]any{
		"project":  []any{entity("proj_portable", map[string]any{"name": "Portable", "kind": "normal", "slug": "portable", "settings": map[string]any{"enabledRecordTypes": []any{"finding"}, "repoMirroring": map[string]any{"enabled": false, "recordTypes": []any{"finding"}}, "crossProjectAnalysis": true, "aiProcessing": false}})},
		"source":   []any{entity("source_portable", map[string]any{"projectId": "proj_portable", "identity": "github.com/synthetic/portable"})},
		"record":   []any{entity("rec:portable", map[string]any{"projectId": "proj_portable", "type": "finding", "title": "Portable evidence", "content": "Immutable transferable evidence", "payload": map[string]any{"finding": "Preserve raw capture.", "unknown": []any{float64(7), true}}, "authority": nil, "recordedAt": "2026-10-01T00:00:00.000Z"})},
		"metadata": []any{entity("rec:portable", map[string]any{"recordId": "rec:portable", "tags": []any{"portable"}, "archived": false, "displayTitle": "Reviewed presentation"})},
		"revision": []any{entity("rev_portable", map[string]any{"recordId": "rec:portable", "previous": map[string]any{"tags": []any{}}, "changes": map[string]any{"tags": []any{"portable"}}})},
		"evidence": []any{entity("ev_portable", map[string]any{"recordId": "rec:portable", "kind": "url", "reference": "https://example.com/evidence"})},
		"mirror":   []any{entity("mirror_portable", map[string]any{"recordId": "rec:portable", "attempted": true, "succeeded": false, "error": "Synthetic historical mirror failure"})},
		"audit":    []any{entity("audit_portable", map[string]any{"entityId": "rec:portable", "entityType": "record", "action": "record.created", "actor": map[string]any{"kind": "user"}})},
		"profile":  []any{entity("owner-profile", map[string]any{"displayName": "Portable owner"})},
	}}
}

func TestNativeImportPreservesDerivedArtifactsWithAIProcessingDisabled(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			archive := portableArchive()
			data := archive["data"].(map[string]any)
			artifact := map[string]any{"id": "artifact_portable", "version": float64(1), "createdAt": "2026-10-01T00:00:00.000Z", "authority": "derived", "kind": "summary", "content": map[string]any{"text": "Source-backed historical summary" + strings.Repeat(" ", 301)}, "generator": map[string]any{"model": "synthetic"}, "sourceRecordIds": []any{"rec:portable"}, "projectIds": []any{"proj_portable"}, "status": "accepted", "privacyDependencies": map[string]any{"version": float64(1), "recordIds": []any{"rec:portable"}, "projectIds": []any{"proj_portable"}, "crossProject": false}}
			encoded, _ := json.Marshal(artifact)
			var curated map[string]any
			json.Unmarshal(encoded, &curated)
			curated["id"], curated["artifactId"] = "curated_portable", "artifact_portable"
			delete(curated, "status")
			data["ai_artifact"], data["curated_artifact"] = []any{artifact}, []any{curated}
			status, result := curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 || result["imported"] != float64(11) {
				t.Fatalf("valid historical AI archive rejected: %d %+v", status, result)
			}
			status, result = curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("export unavailable")
			}
			for _, kind := range []string{"ai_artifact", "curated_artifact"} {
				if !reflect.DeepEqual(result["data"].(map[string]any)[kind], data[kind]) {
					t.Fatal("derived provenance rewritten")
				}
			}
			status, result = curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 || result["skipped"] != float64(11) {
				t.Fatal("derived replay failed")
			}
		})
	}
}

func TestNativeImportRejectsInvalidDerivedEvidenceAtomically(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			for _, mode := range []string{"unknown-content", "duplicate-source", "uncited-project", "invalid-privacy", "curated-mismatch", "unaccepted-curation", "replacement-candidate"} {
				t.Run(mode, func(t *testing.T) {
					archive := portableArchive()
					data := archive["data"].(map[string]any)
					artifact := map[string]any{"id": "artifact_portable", "version": float64(1), "createdAt": "2026-10-01T00:00:00Z", "authority": "derived", "kind": "summary", "content": map[string]any{"text": "Synthetic summary"}, "generator": map[string]any{"model": "synthetic"}, "sourceRecordIds": []any{"rec:portable"}, "projectIds": []any{"proj_portable"}, "status": "accepted"}
					data["ai_artifact"] = []any{artifact}
					switch mode {
					case "unknown-content":
						artifact["content"].(map[string]any)["unknown"] = "not in schema"
					case "duplicate-source":
						artifact["sourceRecordIds"] = []any{"rec:portable", "rec:portable"}
					case "uncited-project":
						artifact["projectIds"] = []any{"proj_missing"}
					case "invalid-privacy":
						artifact["privacyDependencies"] = map[string]any{"version": float64(1), "recordIds": []any{"rec_missing"}, "projectIds": []any{"proj_portable"}, "crossProject": false}
					case "curated-mismatch", "unaccepted-curation":
						encoded, _ := json.Marshal(artifact)
						var curated map[string]any
						json.Unmarshal(encoded, &curated)
						curated["id"], curated["artifactId"] = "curated_portable", "artifact_portable"
						delete(curated, "status")
						data["curated_artifact"] = []any{curated}
						if mode == "curated-mismatch" {
							curated["generator"] = map[string]any{"model": "different"}
						} else {
							artifact["status"] = "pending"
						}
					case "replacement-candidate":
						encoded, _ := json.Marshal(data["record"].([]any)[0])
						var record map[string]any
						json.Unmarshal(encoded, &record)
						record["id"] = "rec_second"
						data["record"] = append(data["record"].([]any), record)
						data["metadata"] = append(data["metadata"].([]any), map[string]any{"id": "rec_second", "recordId": "rec_second", "version": float64(1), "createdAt": "2026-10-01T00:00:00Z", "tags": []any{}})
						artifact["kind"], artifact["sourceRecordIds"] = "relationship_candidate", []any{"rec:portable", "rec_second"}
						artifact["content"] = map[string]any{"text": "Unsafe replacement", "fromRecordId": "rec:portable", "toRecordId": "rec_second", "relationshipType": "replaces"}
					}
					status, _ := curationRequest(t, origin, "POST", "/import", archive)
					expected := 400
					if mode == "uncited-project" {
						expected = 404
					}
					if status != expected {
						t.Fatalf("invalid derived evidence accepted: %d", status)
					}
					status, result := curationRequest(t, origin, "POST", "/export", map[string]any{})
					if status != 200 {
						t.Fatal("export failed")
					}
					for kind, entries := range result["data"].(map[string]any) {
						if len(entries.([]any)) != 0 {
							t.Fatalf("invalid artifact retained %s", kind)
						}
					}
				})
			}
		})
	}
}

func TestNativeImportRestoresImmutableKnowledgeAndReplay(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			archive := portableArchive()
			status, result := curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 || result["imported"] != float64(9) || result["skipped"] != float64(0) {
				t.Fatalf("native import failed: %d %+v", status, result)
			}
			status, detail := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
			if status != 200 || !reflect.DeepEqual(detail["record"], archive["data"].(map[string]any)["record"].([]any)[0]) {
				t.Fatal("import rewrote raw evidence")
			}
			if len(detail["revisions"].([]any)) != 1 || len(detail["evidence"].([]any)) != 1 || len(detail["audit"].([]any)) != 1 {
				t.Fatal("import dropped history")
			}
			status, result = curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 || result["imported"] != float64(0) || result["skipped"] != float64(9) {
				t.Fatal("identical replay was not skipped")
			}
			status, result = searchRecords(t, origin, "q=transferable&projectId=proj_portable")
			if status != 200 || len(result["records"].([]any)) != 1 {
				t.Fatal("import did not restore the lexical index")
			}
			status, result = projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": "https://github.com/synthetic/portable.git"}}}, restoredToken)
			if status != 200 || result["project"].(map[string]any)["id"] != "proj_portable" {
				t.Fatal("import did not restore source identity")
			}
		})
	}
}

func TestNativeExportPreservesKnowledgeAndExcludesPrivateState(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, local := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			status, detail := fetchRecord(t, origin, restoredToken)
			if status != 200 {
				t.Fatal("restored record unavailable")
			}
			status, body := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatalf("export unavailable: %d", status)
			}
			if body["format"] != "scratchpad" || body["version"] != float64(1) {
				t.Fatal("portable format changed")
			}
			if _, err := time.Parse(time.RFC3339Nano, body["exportedAt"].(string)); err != nil {
				t.Fatal(err)
			}
			data := body["data"].(map[string]any)
			kinds := []string{"project", "source", "record", "metadata", "revision", "relationship", "evidence", "mirror", "audit", "settings", "profile", "ai_artifact", "curated_artifact"}
			if len(data) != len(kinds) {
				t.Fatal("archive includes private sections or omits knowledge sections")
			}
			for _, kind := range kinds {
				if _, ok := data[kind].([]any); !ok {
					t.Fatalf("missing array: %s", kind)
				}
			}
			if !reflect.DeepEqual(data["record"].([]any)[0], detail["record"]) {
				t.Fatal("export rewrote raw history")
			}
			for _, pair := range [][2]string{{"revision", "revisions"}, {"evidence", "evidence"}, {"audit", "audit"}} {
				if !reflect.DeepEqual(data[pair[0]], detail[pair[1]]) {
					t.Fatalf("lost immutable %s history", pair[0])
				}
			}
			encoded, _ := json.Marshal(body)
			for _, private := range []string{restoredToken, "synthetic_legacy_secret", "credential_restored", "synthetic-independent-browser-session"} {
				if strings.Contains(string(encoded), private) {
					t.Fatalf("archive leaked private fixture: %s", private)
				}
			}
			request, _ := http.NewRequest("POST", origin+"/api/v1/export", nil)
			request.AddCookie(local)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 403 {
				t.Fatal("browser export did not require Origin")
			}
			request, _ = http.NewRequest("POST", origin+"/api/v1/export", nil)
			response, err = http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 401 {
				t.Fatal("unauthenticated export allowed")
			}
		})
	}
}
