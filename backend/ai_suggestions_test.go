package backend_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestSuggestionListingAuthenticatesBeforeQueryValidation(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	response, err := http.Get(origin + "/api/v1/suggestions?crossProject=invalid")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("unauthenticated listing validated private API input first: %d", response.StatusCode)
	}
}

func TestSuggestionReviewAuditFailureRollsBackAcceptance(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			suggestionFixture(t, origin, "classification", map[string]any{"text": "Atomic derived classification", "tags": []any{"accepted-only-after-audit"}})
			status, before := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
			if status != 200 {
				t.Fatal("detail missing")
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
			fault := `CREATE TRIGGER suggestion_fault BEFORE INSERT ON entities WHEN NEW.kind='audit' AND json_extract(NEW.data,'$.action')='ai.suggestion.accepted' BEGIN SELECT RAISE(ABORT,'synthetic-private-fault'); END`
			if engine == "postgres" {
				fault = `CREATE FUNCTION suggestion_fault_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='audit' AND NEW.data->>'action'='ai.suggestion.accepted' THEN RAISE EXCEPTION 'synthetic-private-fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER suggestion_fault BEFORE INSERT ON entities FOR EACH ROW EXECUTE FUNCTION suggestion_fault_fn()`
			}
			if _, err = db.Exec(fault); err != nil {
				t.Fatal(err)
			}
			status, response := curationRequest(t, origin, "POST", "/suggestions/artifact_review/accept", map[string]any{"expectedVersion": 1})
			if status != 503 || response["error"].(map[string]any)["code"] != "DATABASE_NOT_READY" {
				t.Fatalf("late audit fault was accepted: %d %+v", status, response)
			}
			status, after := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
			if status != 200 || !reflect.DeepEqual(before, after) {
				t.Fatal("late failure left capture, metadata or audit side effects")
			}
			status, response = curationRequest(t, origin, "GET", "/suggestions?projectId=proj_portable", nil)
			if status != 200 || response["suggestions"].([]any)[0].(map[string]any)["status"] != "pending" || response["suggestions"].([]any)[0].(map[string]any)["version"] != float64(1) {
				t.Fatal("late failure consumed the pending suggestion")
			}
			status, response = curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 || len(response["data"].(map[string]any)["curated_artifact"].([]any)) != 0 {
				t.Fatal("late failure left a curated derivative")
			}
		})
	}
}

func TestSuggestionReviewRevalidatesMalformedHistoricalContent(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			artifact := suggestionFixture(t, origin, "classification", map[string]any{"text": "Historical derived evidence", "tags": []any{"valid"}})
			artifact["content"] = map[string]any{"text": "Malformed historical tags", "tags": []any{42}}
			raw, _ := json.Marshal(artifact)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(`UPDATE entities SET data=$1 WHERE kind='ai_artifact' AND id='artifact_review'`, string(raw))
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			status, response := curationRequest(t, origin, "GET", "/suggestions", nil)
			if status != 200 || len(response["suggestions"].([]any)) != 0 {
				t.Fatal("malformed historical content became readable")
			}
			status, response = curationRequest(t, origin, "POST", "/suggestions/artifact_review/accept", map[string]any{"expectedVersion": 1})
			if status != 400 || response["error"].(map[string]any)["code"] != "AI_ARTIFACT_INVALID" {
				t.Fatal("review did not independently validate stored content")
			}
			status, response = curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 || len(response["data"].(map[string]any)["curated_artifact"].([]any)) != 0 {
				t.Fatal("invalid review produced a curated derivative")
			}
		})
	}
}

func TestSuggestionRejectionIsVersionedAndPersistsAcrossRestart(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			for _, phase := range []string{"reject", "restart"} {
				t.Run(phase, func(t *testing.T) {
					origin := startServer(t, path, databaseURL)
					if phase == "reject" {
						suggestionFixture(t, origin, "summary", map[string]any{"text": "Derived output awaiting human review"})
						status, _ := curationRequest(t, origin, "POST", "/suggestions/artifact_review/reject", map[string]any{})
						if status != 428 {
							t.Fatal("review did not require a version")
						}
						status, response := curationRequest(t, origin, "POST", "/suggestions/artifact_review/reject", map[string]any{"expectedVersion": 2})
						if status != 409 || response["error"].(map[string]any)["details"].(map[string]any)["currentVersion"] != float64(1) {
							t.Fatal("stale rejection overwrote current state")
						}
						status, response = curationRequest(t, origin, "POST", "/suggestions/artifact_review/reject", map[string]any{"expectedVersion": 1})
						if status != 200 || response["suggestion"].(map[string]any)["status"] != "rejected" {
							t.Fatal("rejection failed")
						}
					}
					status, response := curationRequest(t, origin, "GET", "/suggestions?projectId=proj_portable", nil)
					if status != 200 {
						t.Fatal("suggestions unavailable")
					}
					suggestion := response["suggestions"].([]any)[0].(map[string]any)
					if suggestion["status"] != "rejected" || suggestion["version"] != float64(2) || suggestion["reviewedAt"] == nil || suggestion["reviewedBy"] == nil {
						t.Fatal("review provenance lost across restart")
					}
					status, response = curationRequest(t, origin, "POST", "/export", map[string]any{})
					if status != 200 || len(response["data"].(map[string]any)["curated_artifact"].([]any)) != 0 {
						t.Fatal("rejection created curated evidence")
					}
				})
			}
		})
	}
}

func TestSuggestionClassificationEffectsFollowActualPrivacyProjects(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		for _, mode := range []string{"one_project", "two_projects"} {
			t.Run(engine+"/"+mode, func(t *testing.T) {
				path, databaseURL := portableInstance(t, engine)
				origin := startServer(t, path, databaseURL)
				original := suggestionFixture(t, origin, "classification", map[string]any{"text": "Derived classification influenced by all inputs", "tags": []any{"derived-context"}})
				artifact := map[string]any{}
				for key, value := range original {
					artifact[key] = value
				}
				artifact["id"] = "artifact_cross"
				records, projects := []any{"rec:portable"}, []any{"proj_portable"}
				if mode == "two_projects" {
					other := jobProject(t, origin, "Cross input context", true, true)
					record := curationCapture(t, origin, other, "Independent input evidence")
					records = append(records, record["id"])
					projects = append(projects, other)
				}
				artifact["crossProject"] = true
				artifact["privacyDependencies"] = map[string]any{"version": 1, "recordIds": records, "projectIds": projects, "crossProject": true}
				status, archive := curationRequest(t, origin, "POST", "/export", map[string]any{})
				if status != 200 {
					t.Fatal("export failed")
				}
				data := archive["data"].(map[string]any)
				data["ai_artifact"] = append(data["ai_artifact"].([]any), artifact)
				status, response := curationRequest(t, origin, "POST", "/import", archive)
				if status != 200 {
					t.Fatalf("cross artifact import failed: %d %+v", status, response)
				}
				status, response = curationRequest(t, origin, "POST", "/suggestions/artifact_cross/accept", map[string]any{"expectedVersion": 1})
				if status != 200 {
					t.Fatalf("cross review failed: %d %+v", status, response)
				}
				status, detail := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
				if status != 200 {
					t.Fatal("source detail missing")
				}
				metadata := detail["metadata"].(map[string]any)
				if mode == "two_projects" {
					if !reflect.DeepEqual(metadata["tags"], []any{"portable"}) || metadata["classificationArtifactId"] != nil {
						t.Fatal("cross-project interpretation copied into local metadata")
					}
				} else if !reflect.DeepEqual(metadata["tags"], []any{"derived-context"}) || metadata["classificationArtifactId"] != "artifact_cross" {
					t.Fatal("cross request with one actual project lost local classification effects")
				}
			})
		}
	}
}

func TestSuggestionMissingHistoricalSourceFailsAsInvalidArtifact(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	artifact := suggestionFixture(t, origin, "summary", map[string]any{"text": "Dangling historical citation"})
	artifact["sourceRecordIds"] = []any{"rec_missing"}
	artifact["privacyDependencies"] = map[string]any{"version": 1, "recordIds": []any{"rec_missing"}, "projectIds": []any{"proj_portable"}, "crossProject": false}
	raw, _ := json.Marshal(artifact)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE entities SET data=$1 WHERE kind='ai_artifact' AND id='artifact_review'`, string(raw))
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	status, response := curationRequest(t, origin, "POST", "/suggestions/artifact_review/accept", map[string]any{"expectedVersion": 1})
	if status != 400 || response["error"].(map[string]any)["code"] != "AI_ARTIFACT_INVALID" {
		t.Fatalf("dangling source did not preserve the review error contract: %d %+v", status, response)
	}
}

func suggestionFixture(t *testing.T, origin, kind string, content map[string]any) map[string]any {
	t.Helper()
	archive := portableArchive()
	artifact := map[string]any{"id": "artifact_review", "version": 1, "createdAt": "2026-10-01T00:00:00.000Z", "authority": "derived", "kind": kind, "title": "Synthetic suggestion", "content": content, "generator": map[string]any{"model": "synthetic"}, "sourceRecordIds": []any{"rec:portable"}, "projectIds": []any{"proj_portable"}, "status": "pending", "privacyDependencies": map[string]any{"version": 1, "recordIds": []any{"rec:portable"}, "projectIds": []any{"proj_portable"}, "crossProject": false}}
	archive["data"].(map[string]any)["ai_artifact"] = []any{artifact}
	status, response := curationRequest(t, origin, "POST", "/import", archive)
	if status != 200 {
		t.Fatalf("suggestion import failed: %d %+v", status, response)
	}
	status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0})
	if status != 200 {
		t.Fatal("AI settings failed")
	}
	status, project := projectRequest(t, origin, "GET", "/proj_portable", nil, restoredToken)
	if status != 200 {
		t.Fatal("project missing")
	}
	settings := project["project"].(map[string]any)["settings"].(map[string]any)
	settings["aiProcessing"] = true
	status, _ = projectRequest(t, origin, "PATCH", "/proj_portable/settings", map[string]any{"expectedVersion": 1, "settings": settings}, restoredToken)
	if status != 200 {
		t.Fatal("consent settings failed")
	}
	return artifact
}

func TestSuggestionClassificationAcceptancePreservesCaptureAndCuratedProvenance(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			suggestionFixture(t, origin, "classification", map[string]any{"text": "Classify preserved knowledge", "tags": []any{"knowledge"}, "classification": "architecture"})
			status, before := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
			if status != 200 {
				t.Fatal("record detail missing")
			}
			status, response := curationRequest(t, origin, "GET", "/suggestions?projectId=proj_portable", nil)
			if status != 200 || len(response["suggestions"].([]any)) != 1 {
				t.Fatalf("valid suggestion missing: %d %+v", status, response)
			}
			status, response = curationRequest(t, origin, "POST", "/suggestions/artifact_review/accept", map[string]any{"expectedVersion": 1})
			if status != 200 {
				t.Fatalf("acceptance failed: %d %+v", status, response)
			}
			accepted := response["suggestion"].(map[string]any)
			if accepted["status"] != "accepted" || accepted["version"] != float64(2) || accepted["authority"] != "derived" {
				t.Fatal("review promoted authority or lost status/version")
			}
			status, after := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
			if status != 200 || !reflect.DeepEqual(before["record"], after["record"]) {
				t.Fatal("review changed the original capture")
			}
			metadata := after["metadata"].(map[string]any)
			if !reflect.DeepEqual(metadata["tags"], []any{"knowledge"}) || metadata["classification"] != "architecture" || metadata["classificationArtifactId"] != "artifact_review" {
				t.Fatal("classification was not linked to curated metadata")
			}
			status, response = curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("export failed")
			}
			curated := response["data"].(map[string]any)["curated_artifact"].([]any)
			if len(curated) != 1 || curated[0].(map[string]any)["artifactId"] != "artifact_review" || curated[0].(map[string]any)["authority"] != "derived" || !reflect.DeepEqual(curated[0].(map[string]any)["privacyDependencies"], accepted["privacyDependencies"]) {
				t.Fatal("curated acceptance lost privacy provenance")
			}
			status, _ = curationRequest(t, origin, "POST", "/suggestions/artifact_review/accept", map[string]any{"expectedVersion": 2})
			if status != 409 {
				t.Fatal("reviewed suggestion accepted twice")
			}
		})
	}
}

func TestSuggestionRelationshipAcceptanceRetainsSuggestedAuthority(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			suggestionFixture(t, origin, "summary", map[string]any{"text": "Source-backed summary"})
			status, captured := captureRequest(t, origin, "proj_portable", "", map[string]any{"type": "finding", "title": "Supporting evidence", "authority": "observed", "payload": map[string]any{"finding": "This supports the original evidence."}})
			if status != 201 {
				t.Fatalf("second source capture failed: %d %+v", status, captured)
			}
			other := captured["record"].(map[string]any)["id"].(string)
			status, archive := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("export failed")
			}
			artifact := map[string]any{"id": "artifact_relationship", "version": 1, "createdAt": "2026-10-01T01:00:00.000Z", "authority": "derived", "kind": "relationship_candidate", "title": "Supporting link", "content": map[string]any{"text": "The new evidence supports the original.", "fromRecordId": other, "toRecordId": "rec:portable", "relationshipType": "supports"}, "generator": map[string]any{"model": "synthetic"}, "sourceRecordIds": []any{other, "rec:portable"}, "projectIds": []any{"proj_portable"}, "status": "pending", "privacyDependencies": map[string]any{"version": 1, "recordIds": []any{other, "rec:portable"}, "projectIds": []any{"proj_portable"}, "crossProject": false}}
			data := archive["data"].(map[string]any)
			data["ai_artifact"] = append(data["ai_artifact"].([]any), artifact)
			status, response := curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 {
				t.Fatalf("relationship suggestion import failed: %d %+v", status, response)
			}
			status, response = curationRequest(t, origin, "POST", "/suggestions/artifact_relationship/accept", map[string]any{"expectedVersion": 1})
			if status != 200 {
				t.Fatalf("relationship review failed: %d %+v", status, response)
			}
			status, detail := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
			if status != 200 {
				t.Fatal("source detail unavailable")
			}
			links := detail["relationships"].([]any)
			if len(links) != 1 {
				t.Fatal("acceptance did not create the source-linked relationship")
			}
			link := links[0].(map[string]any)
			if link["fromRecordId"] != other || link["toRecordId"] != "rec:portable" || link["type"] != "supports" || link["authority"] != "suggested" || link["status"] != "accepted" || link["artifactId"] != "artifact_relationship" || link["derivation"].(map[string]any)["authority"] != "derived" {
				t.Fatal("relationship lost derived provenance or promoted authority")
			}
		})
	}
}

func TestSuggestionPrivacyIncludesUncitedInputProjectsAndUnknownLegacyProvenance(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			original := suggestionFixture(t, origin, "summary", map[string]any{"text": "Preserved derived evidence"})
			other := jobProject(t, origin, "Private input project", true, true)
			record := curationCapture(t, origin, other, "Independent input evidence")["id"].(string)
			unknown := map[string]any{}
			broader := map[string]any{}
			for key, value := range original {
				unknown[key] = value
				broader[key] = value
			}
			unknown["id"] = "artifact_unknown"
			delete(unknown, "privacyDependencies")
			broader["id"] = "artifact_broader"
			broader["privacyDependencies"] = map[string]any{"version": 1, "recordIds": []any{"rec:portable", record}, "projectIds": []any{"proj_portable", other}, "crossProject": true}
			status, archive := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("export failed")
			}
			data := archive["data"].(map[string]any)
			data["ai_artifact"] = append(data["ai_artifact"].([]any), unknown, broader)
			status, response := curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 {
				t.Fatalf("privacy archive failed: %d %+v", status, response)
			}
			status, response = curationRequest(t, origin, "GET", "/suggestions?projectId=proj_portable", nil)
			if status != 200 || len(response["suggestions"].([]any)) != 1 || response["suggestions"].([]any)[0].(map[string]any)["id"] != "artifact_review" {
				t.Fatal("narrow scope exposed uncited input dependencies or unknown provenance")
			}
			status, response = curationRequest(t, origin, "GET", "/suggestions", nil)
			if status != 200 || len(response["suggestions"].([]any)) != 2 {
				t.Fatal("eligible broad suggestion missing or unknown provenance exposed")
			}
			status, _ = curationRequest(t, origin, "POST", "/suggestions/artifact_unknown/accept", map[string]any{"expectedVersion": 1})
			if status != 403 {
				t.Fatal("unknown historical provenance became reviewable")
			}
			status, project := projectRequest(t, origin, "GET", "/"+other, nil, restoredToken)
			if status != 200 {
				t.Fatal("input project missing")
			}
			settings := project["project"].(map[string]any)["settings"].(map[string]any)
			settings["aiProcessing"] = false
			status, _ = projectRequest(t, origin, "PATCH", "/"+other+"/settings", map[string]any{"expectedVersion": 2, "settings": settings}, restoredToken)
			if status != 200 {
				t.Fatal("revocation failed")
			}
			status, response = curationRequest(t, origin, "GET", "/suggestions", nil)
			if status != 200 || len(response["suggestions"].([]any)) != 1 {
				t.Fatal("revoking uncited project consent left its derived output readable")
			}
			status, _ = curationRequest(t, origin, "POST", "/suggestions/artifact_broader/reject", map[string]any{"expectedVersion": 1})
			if status != 403 {
				t.Fatal("revoked input dependencies remained reviewable")
			}
		})
	}
}
