package backend_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func captureRequest(t *testing.T, origin, projectID, key string, record any) (int, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"projectId": projectID, "record": record, "gitContext": map[string]any{"branch": "main"}})
	req, _ := http.NewRequest("POST", origin+"/api/v1/records", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+restoredToken)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	if json.NewDecoder(res.Body).Decode(&body) != nil {
		t.Fatalf("capture returned non-JSON: %d", res.StatusCode)
	}
	return res.StatusCode, body
}
func TestCapturePersistsImmutableRecordAndProvenance(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Capture"}, restoredToken)
			projectID := project["project"].(map[string]any)["id"].(string)
			clientSavedAt := "2026-10-15T08:09:10.123456789Z"
			record := map[string]any{"type": "decision", "title": "  Choose Go  ", "authority": "explicit", "clientSavedAt": clientSavedAt, "payload": map[string]any{"decision": "Keep the central API authoritative."}, "actor": map[string]any{"kind": "user", "client": "synthetic-agent"}}
			status, body := captureRequest(t, origin, projectID, "", record)
			if status != 201 {
				t.Fatalf("capture unavailable: %d", status)
			}
			saved := body["record"].(map[string]any)
			if saved["title"] != "Choose Go" || saved["content"] != "decision: Keep the central API authoritative." || saved["confidence"] != "unknown" || saved["payloadVersion"] != float64(1) {
				t.Fatal("capture content or defaults differ")
			}
			if saved["clientSavedAt"] != clientSavedAt || saved["recordedAt"] == clientSavedAt {
				t.Fatal("capture did not preserve separate client and server timestamps")
			}
			actor := saved["actor"].(map[string]any)
			if actor["kind"] != "agent" || actor["client"] != "synthetic-agent" || actor["credentialFingerprint"] != "credential_restored" {
				t.Fatal("capture did not retain authenticated provenance")
			}
			req, _ := http.NewRequest("GET", origin+"/api/v1/records/"+saved["id"].(string), nil)
			req.Header.Set("Authorization", "Bearer "+restoredToken)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			var detail map[string]any
			json.NewDecoder(res.Body).Decode(&detail)
			detailRecord := detail["record"].(map[string]any)
			if res.StatusCode != 200 || detailRecord["content"] != saved["content"] || detailRecord["clientSavedAt"] != clientSavedAt || detailRecord["recordedAt"] != saved["recordedAt"] || detail["metadata"].(map[string]any)["archived"] != false || len(detail["audit"].([]any)) != 1 {
				t.Fatal("capture detail did not preserve raw history and audit")
			}
		})
	}
}

func TestCaptureRetryKeepsOneImmutableRecord(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			var projectID, recordID string
			record := map[string]any{"type": "finding", "title": "Stable retry", "authority": "observed", "clientSavedAt": "2026-10-15T08:09:10.123456789+02:00", "payload": map[string]any{"finding": "Retry safely."}}
			t.Run("first", func(t *testing.T) {
				origin := startServer(t, path, databaseURL)
				_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Retry"}, restoredToken)
				projectID = project["project"].(map[string]any)["id"].(string)
				status, body := captureRequest(t, origin, projectID, "synthetic-request", record)
				if status != 201 {
					t.Fatal("first capture failed")
				}
				recordID = body["record"].(map[string]any)["id"].(string)
			})
			t.Run("restart", func(t *testing.T) {
				origin := startServer(t, path, databaseURL)
				status, body := captureRequest(t, origin, projectID, "synthetic-request", record)
				if status != 201 || body["record"].(map[string]any)["id"] != recordID {
					t.Fatal("retry created another capture after restart")
				}
				record["clientSavedAt"] = "2026-10-15T08:09:10.123456788+02:00"
				status, _ = captureRequest(t, origin, projectID, "synthetic-request", record)
				if status != 409 {
					t.Fatal("retry identity accepted a changed client save timestamp")
				}
				record["title"] = "Different"
				status, _ = captureRequest(t, origin, projectID, "synthetic-request", record)
				if status != 409 {
					t.Fatal("retry identity accepted a different capture")
				}
			})
		})
	}
}

func TestCaptureClientSavedAtValidation(t *testing.T) {
	path, databaseURL, _ := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Timestamp validation"}, restoredToken)
	projectID := project["project"].(map[string]any)["id"].(string)
	for _, invalid := range []any{"yesterday", "2026-10-15T08:09:10", float64(42), nil} {
		record := map[string]any{"type": "finding", "title": "Invalid local time", "authority": "observed", "clientSavedAt": invalid, "payload": map[string]any{"finding": "Synthetic invalid timestamp."}}
		status, _ := captureRequest(t, origin, projectID, "", record)
		if status != http.StatusBadRequest {
			t.Fatalf("invalid clientSavedAt %v returned %d", invalid, status)
		}
	}
	status, saved := captureRequest(t, origin, projectID, "", map[string]any{"type": "finding", "title": "Legacy caller", "authority": "observed", "payload": map[string]any{"finding": "Timestamp omitted."}})
	if status != http.StatusCreated {
		t.Fatalf("legacy capture without clientSavedAt returned %d", status)
	}
	if _, exists := saved["record"].(map[string]any)["clientSavedAt"]; exists {
		t.Fatal("server invented a client save timestamp for an older caller")
	}
}

func TestCaptureRenderingPreservesNestedPayloadOrderAndNull(t *testing.T) {
	path, _, _ := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Rendering"}, restoredToken)
	id := project["project"].(map[string]any)["id"].(string)
	raw := `{"projectId":"` + id + `","record":{"type":"decision","title":"JSON payload","authority":"explicit","payload":{"decision":"Choice","details":null,"nested":{"z":1,"a":[true,null]}}}}`
	req, _ := http.NewRequest("POST", origin+"/api/v1/records", bytes.NewBufferString(raw))
	req.Header.Set("Authorization", "Bearer "+restoredToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	json.NewDecoder(res.Body).Decode(&body)
	if res.StatusCode != 201 || body["record"].(map[string]any)["content"] != `decision: Choice

details: null

nested: {"z":1,"a":[true,null]}` {
		t.Fatal("capture rendering changed payload meaning or object order")
	}
}

func TestConcurrentRetriesUseOneCaptureAndCurrentMirrorConsent(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Concurrent captures"}, restoredToken)
			id := project["project"].(map[string]any)["id"].(string)
			record := map[string]any{"type": "decision", "title": "One capture", "authority": "explicit", "payload": map[string]any{"decision": "Keep retries stable."}}
			data, _ := json.Marshal(map[string]any{"projectId": id, "record": record, "gitContext": map[string]any{"branch": "main"}})
			results := make(chan map[string]any, 8)
			failures := make(chan error, 8)
			for range 8 {
				go func() {
					req, _ := http.NewRequest("POST", origin+"/api/v1/records", bytes.NewReader(data))
					req.Header.Set("Authorization", "Bearer "+restoredToken)
					req.Header.Set("Idempotency-Key", "concurrent-request")
					res, err := http.DefaultClient.Do(req)
					if err != nil {
						failures <- err
						return
					}
					defer res.Body.Close()
					var body map[string]any
					if err = json.NewDecoder(res.Body).Decode(&body); err != nil {
						failures <- err
						return
					}
					if res.StatusCode != 201 {
						failures <- fmt.Errorf("capture failed: %d", res.StatusCode)
						return
					}
					results <- body
				}()
			}
			var recordID, createdAt string
			for range 8 {
				select {
				case result := <-results:
					saved := result["record"].(map[string]any)
					next := saved["id"].(string)
					if recordID != "" && next != recordID {
						t.Fatal("concurrent retry created another record")
					}
					recordID = next
					createdAt = saved["createdAt"].(string)
				case err := <-failures:
					t.Fatal(err)
				}
			}
			settings := map[string]any{"enabledRecordTypes": []string{}, "repoMirroring": map[string]any{"enabled": true, "recordTypes": []string{"decision"}}, "crossProjectAnalysis": true, "aiProcessing": false}
			status, _ := projectRequest(t, origin, "PATCH", "/"+id+"/settings", map[string]any{"settings": settings, "expectedVersion": 1}, restoredToken)
			if status != 200 {
				t.Fatal("settings update failed")
			}
			status, body := captureRequest(t, origin, id, "concurrent-request", record)
			if status != 201 || body["record"].(map[string]any)["id"] != recordID || body["record"].(map[string]any)["createdAt"] != createdAt || body["mirror"].(map[string]any)["eligible"] != true {
				t.Fatal("retry changed history or used stale mirror consent")
			}
			status, _ = captureRequest(t, origin, id, "new-request", record)
			if status != 403 {
				t.Fatal("new capture bypassed disabled record type")
			}
			req, _ := http.NewRequest("GET", origin+"/api/v1/records/"+recordID, nil)
			req.Header.Set("Authorization", "Bearer "+restoredToken)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			var detail map[string]any
			json.NewDecoder(res.Body).Decode(&detail)
			if len(detail["audit"].([]any)) != 1 {
				t.Fatal("retry duplicated the creation audit")
			}
		})
	}
}

func TestCaptureConsentGatePrecedesPayloadRendering(t *testing.T) {
	path, _, _ := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Disabled"}, restoredToken)
	id := project["project"].(map[string]any)["id"].(string)
	settings := map[string]any{"enabledRecordTypes": []string{}, "repoMirroring": map[string]any{"enabled": false, "recordTypes": []string{}}, "crossProjectAnalysis": false, "aiProcessing": false}
	projectRequest(t, origin, "PATCH", "/"+id+"/settings", map[string]any{"settings": settings, "expectedVersion": 1}, restoredToken)
	status, _ := captureRequest(t, origin, id, "disabled", map[string]any{"type": "decision", "title": "Incomplete", "authority": "explicit", "payload": map[string]any{}})
	if status != 403 {
		t.Fatal("disabled record type did not retain its consent error contract")
	}
}

func TestInvalidCaptureDoesNotReserveRetryIdentity(t *testing.T) {
	path, _, _ := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Validation"}, restoredToken)
	id := project["project"].(map[string]any)["id"].(string)
	record := map[string]any{"type": "qa", "title": "Question", "authority": "observed", "payload": map[string]any{"answer": "The owner chose Go."}}
	status, _ := captureRequest(t, origin, id, "corrected-request", record)
	if status != 400 {
		t.Fatal("missing required payload was accepted")
	}
	record["payload"].(map[string]any)["question"] = "What changed?"
	status, body := captureRequest(t, origin, id, "corrected-request", record)
	if status != 201 || body["record"] == nil {
		t.Fatal("failed validation reserved the retry identity")
	}
}
func TestCaptureRetryIdentityIsScopedToCredential(t *testing.T) {
	path, _, local := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Agents"}, restoredToken)
	id := project["project"].(map[string]any)["id"].(string)
	record := map[string]any{"type": "finding", "title": "Independent retries", "authority": "observed", "payload": map[string]any{"finding": "Two credentials share one owner."}}
	_, first := captureRequest(t, origin, id, "shared-request-key", record)
	private, key := githubSyntheticKey(t)
	enrollLocalSSH(t, origin, private, key, local)
	_, proof := signedChallenge(t, origin, private, key)
	status, session := postJSON(t, origin, "/api/v1/auth/mcp/verify", proof)
	if status != 200 {
		t.Fatal("second credential could not authenticate")
	}
	data, _ := json.Marshal(map[string]any{"projectId": id, "record": record})
	req, _ := http.NewRequest("POST", origin+"/api/v1/records", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+session["accessToken"].(string))
	req.Header.Set("Idempotency-Key", "shared-request-key")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var second map[string]any
	json.NewDecoder(res.Body).Decode(&second)
	if res.StatusCode != 201 || first["record"].(map[string]any)["id"] == second["record"].(map[string]any)["id"] {
		t.Fatal("different credential reused another credential's capture identity")
	}
}

func TestRealStdioCapturesAllRecordTypesThroughGo(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, local := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			private, key := githubSyntheticKey(t)
			enrollLocalSSH(t, origin, private, key, local)
			serviceSocket, stopService := startConfiguredDurableService(t, origin, t.TempDir(), filepath.Dir(private), private)
			defer stopService()
			workspace := t.TempDir()
			for _, args := range [][]string{{"init", "-q", "-b", "main", workspace}, {"-C", workspace, "remote", "add", "origin", "https://github.com/synthetic/capture.git"}, {"-C", workspace, "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "commit", "-q", "--allow-empty", "-m", "Synthetic"}} {
				cmd := exec.Command("git", args...)
				cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
				if cmd.Run() != nil {
					t.Fatal("synthetic Git fixture failed")
				}
			}
			cmd := exec.Command(integrationBinary, "-url", origin, "-public-key", private+".pub", "-signing-key", private, "-workspace", workspace, "-binary", mcpBinary, "-service-socket", serviceSocket, "-phase", "records", "-state", filepath.Join(t.TempDir(), "capture-state.json"))
			cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "PASS records") || !strings.Contains(string(out), "lexical search") {
				t.Fatalf("real stdio capture workflow failed: %v %s", err, out)
			}
		})
	}
}

func TestHistoricalRetryStateReplaysThroughGo(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			projectID := "proj_historical_retry"
			recordID := "rec_historical_retry"
			at := "2026-10-01T00:00:00.000Z"
			settings := map[string]any{"repoMirroring": map[string]any{"enabled": false, "recordTypes": []string{"finding"}}, "crossProjectAnalysis": true, "aiProcessing": false}
			project := map[string]any{"id": projectID, "name": "Historical", "slug": "historical", "kind": "normal", "settings": settings, "version": 1, "createdAt": at, "updatedAt": at}
			record := map[string]any{"type": "finding", "title": "Historical retry", "authority": "observed", "confidence": "unknown", "payload": map[string]any{"finding": "Keep the original capture."}}
			saved := map[string]any{}
			for key, value := range record {
				saved[key] = value
			}
			saved["id"], saved["projectId"], saved["version"], saved["createdAt"], saved["recordedAt"], saved["content"] = recordID, projectID, 1, at, at, "finding: Keep the original capture."
			for _, row := range []struct {
				kind, id string
				value    map[string]any
			}{{"project", projectID, project}, {"record", recordID, saved}} {
				data, _ := json.Marshal(row.value)
				if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES($1,$2,$3,1,$4)`, row.kind, row.id, string(data), at); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			request, _ := json.Marshal(map[string]any{"record": record, "projectId": projectID})
			response, _ := json.Marshal(map[string]any{"record": saved, "mirror": map[string]any{"eligible": false}})
			if _, err = db.Exec(`INSERT INTO retries(scope,key,request,response) VALUES('capture:credential_restored','historical-request',$1,$2)`, string(request), string(response)); err != nil {
				db.Close()
				t.Fatal(err)
			}
			db.Close()
			origin := startServer(t, path, databaseURL)
			delete(record, "confidence")
			status, body := captureRequest(t, origin, projectID, "historical-request", record)
			if status != 201 || body["record"].(map[string]any)["id"] != recordID || body["record"].(map[string]any)["createdAt"] != at {
				t.Fatal("restored historical retry did not return the original capture")
			}
		})
	}
}
