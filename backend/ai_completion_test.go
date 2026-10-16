package backend_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAnalysisWorkerAuditFailureLeavesNoActivatedArtifacts(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			provider := completionService(t, func(input map[string]any) any {
				record := input["records"].([]any)[0].(map[string]any)
				return map[string]any{"artifacts": []any{map[string]any{"kind": "summary", "title": "Atomic interpretation", "content": map[string]any{"text": "Source-backed provisional interpretation"}, "sourceRecordIds": []any{record["id"]}}}}
			})
			defer provider.Close()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []any{"summary"}})
			if status != 200 {
				t.Fatal("settings failed")
			}
			projectID := jobProject(t, origin, "Atomic completion output", true, false)
			record := curationCapture(t, origin, projectID, "Immutable input")
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			fault := `CREATE TRIGGER completion_fault BEFORE INSERT ON entities WHEN NEW.kind='audit' AND json_extract(NEW.data,'$.action')='ai.job.completed' BEGIN SELECT RAISE(ABORT,'synthetic-private-fault'); END`
			if engine == "postgres" {
				fault = `CREATE FUNCTION completion_fault_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='audit' AND NEW.data->>'action'='ai.job.completed' THEN RAISE EXCEPTION 'synthetic-private-fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER completion_fault BEFORE INSERT ON entities FOR EACH ROW EXECUTE FUNCTION completion_fault_fn()`
			}
			if _, err = db.Exec(fault); err != nil {
				t.Fatal(err)
			}
			status, response := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "analyze", "projectId": projectID})
			if status != 202 {
				t.Fatal("enqueue failed")
			}
			job := waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "failed")
			if job["attempts"] != float64(3) || strings.Contains(job["lastError"].(string), "synthetic-private-fault") {
				t.Fatal("activation failure leaked private persistence output or skipped bounded retries")
			}
			status, response = curationRequest(t, origin, "GET", "/suggestions", nil)
			if status != 200 || len(response["suggestions"].([]any)) != 0 {
				t.Fatal("failed activation left a readable artifact")
			}
			status, response = curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 || len(response["data"].(map[string]any)["ai_artifact"].([]any)) != 0 {
				t.Fatal("failed activation left archived partial output")
			}
			status, response = curationRequest(t, origin, "GET", "/records/"+record["id"].(string), nil)
			if status != 200 || !reflect.DeepEqual(response["record"], record) {
				t.Fatal("failed activation changed raw evidence")
			}
		})
	}
}

func TestAnalysisWorkerDiscardsCompletionAfterConsentRevocation(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var signaled, unblocked sync.Once
			unblock := func() { unblocked.Do(func() { close(release) }) }
			provider := completionService(t, func(input map[string]any) any {
				signaled.Do(func() { close(started) })
				<-release
				record := input["records"].([]any)[0].(map[string]any)
				return map[string]any{"artifacts": []any{map[string]any{"kind": "summary", "title": "In-flight derived output", "content": map[string]any{"text": "Consent is required for activation"}, "sourceRecordIds": []any{record["id"]}}}}
			})
			defer func() { unblock(); provider.Close() }()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []any{"summary"}})
			if status != 200 {
				t.Fatal("settings failed")
			}
			projectID := jobProject(t, origin, "Revoked completion input", true, false)
			curationCapture(t, origin, projectID, "Keep raw history")
			status, response := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "analyze", "projectId": projectID})
			if status != 202 {
				t.Fatal("enqueue failed")
			}
			select {
			case <-started:
			case <-time.After(4 * time.Second):
				t.Fatal("completion not dispatched")
			}
			status, project := projectRequest(t, origin, "GET", "/"+projectID, nil, restoredToken)
			if status != 200 {
				t.Fatal("project missing")
			}
			settings := project["project"].(map[string]any)["settings"].(map[string]any)
			settings["aiProcessing"] = false
			status, _ = projectRequest(t, origin, "PATCH", "/"+projectID+"/settings", map[string]any{"expectedVersion": 2, "settings": settings}, restoredToken)
			if status != 200 {
				t.Fatal("provider wait blocked revocation")
			}
			unblock()
			waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "failed")
			status, response = curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 || len(response["data"].(map[string]any)["ai_artifact"].([]any)) != 0 {
				t.Fatal("revoked completion was activated")
			}
		})
	}
}

func TestRealStdioAIWorkflowUsesGoWorkers(t *testing.T) {
	integration := filepath.Join(t.TempDir(), "ai-integration")
	build := exec.Command("go", "build", "-race", "-o", integration, "./ai-integration")
	build.Dir = "../mcp"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("AI integration build failed: %v %s", err, out)
	}
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					http.Error(w, "invalid request", 400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/embeddings" {
					data := []any{}
					for index := range body["input"].([]any) {
						data = append(data, map[string]any{"index": index, "embedding": []float64{1, 0}})
					}
					json.NewEncoder(w).Encode(map[string]any{"data": data})
					return
				}
				if r.URL.Path != "/v1/chat/completions" {
					http.Error(w, "unexpected path", 400)
					return
				}
				messages := body["messages"].([]any)
				var input map[string]any
				if json.Unmarshal([]byte(messages[1].(map[string]any)["content"].(string)), &input) != nil {
					http.Error(w, "invalid input", 400)
					return
				}
				ids := []any{}
				for _, item := range input["records"].([]any) {
					ids = append(ids, item.(map[string]any)["id"])
				}
				schema := body["response_format"].(map[string]any)["json_schema"].(map[string]any)["schema"].(map[string]any)
				kinds := schema["properties"].(map[string]any)["artifacts"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)["enum"].([]any)
				kind := "summary"
				content := map[string]any{"text": "Private durable source-backed memory"}
				if len(kinds) == 1 && kinds[0] == "export" {
					kind = "export"
					content["markdown"] = "## Private handoff\n\nPreserve original evidence."
				}
				encoded, _ := json.Marshal(map[string]any{"artifacts": []any{map[string]any{"kind": kind, "title": "Synthetic source-linked output", "content": content, "sourceRecordIds": ids}}})
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(encoded)}}}})
			}))
			defer provider.Close()
			path, databaseURL, local := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			private, key := githubSyntheticKey(t)
			enrollLocalSSH(t, origin, private, key, local)
			workspace := t.TempDir()
			for _, args := range [][]string{{"init", "-q", "-b", "main", workspace}, {"-C", workspace, "remote", "add", "origin", "https://github.com/synthetic/ai-workflow.git"}, {"-C", workspace, "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "commit", "-q", "--allow-empty", "-m", "Synthetic"}} {
				cmd := exec.Command("git", args...)
				cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
				if cmd.Run() != nil {
					t.Fatal("Git fixture failed")
				}
			}
			status, response := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": "https://github.com/synthetic/ai-workflow.git"}}}, restoredToken)
			if status != 200 {
				t.Fatal("local project resolution failed")
			}
			project := response["project"].(map[string]any)
			projectID := project["id"].(string)
			settings := project["settings"].(map[string]any)
			settings["aiProcessing"], settings["crossProjectAnalysis"] = true, true
			status, _ = projectRequest(t, origin, "PATCH", "/"+projectID+"/settings", map[string]any{"expectedVersion": 1, "settings": settings}, restoredToken)
			if status != 200 {
				t.Fatal("local consent failed")
			}
			other := jobProject(t, origin, "Other AI history", true, true)
			denied := jobProject(t, origin, "Denied history", false, false)
			curationCapture(t, origin, projectID, "Local private durable memory")
			curationCapture(t, origin, other, "Other private durable memory")
			status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2, "analysisModes": []any{"summary"}})
			if status != 200 {
				t.Fatal("provider settings failed")
			}
			serviceSocket, _ := startConfiguredDurableService(t, origin, t.TempDir(), workspace, private)
			cmd := exec.Command(integration, "-url", origin, "-public-key", private+".pub", "-signing-key", private, "-workspace", workspace, "-binary", mcpBinary, "-other-project", other, "-denied-project", denied)
			cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "SCRATCHPAD_SERVICE_SOCKET="+serviceSocket)
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "Verified all five optional AI tools") {
				t.Fatalf("real stdio AI workflow failed: %v %s", err, out)
			}
		})
	}
}

func completionService(t *testing.T, produce func(map[string]any) any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if r.URL.Path != "/v1/chat/completions" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Messages) != 2 {
			http.Error(w, "invalid completion", 400)
			return
		}
		var input map[string]any
		if json.Unmarshal([]byte(body.Messages[1].Content), &input) != nil {
			http.Error(w, "invalid input", 400)
			return
		}
		content, err := json.Marshal(produce(input))
		if err != nil {
			http.Error(w, "invalid output", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}}}})
	}))
}

func TestAnalysisWorkerStopsLaterBatchesAfterConfigurationChange(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var signaled, unblocked sync.Once
			unblock := func() { unblocked.Do(func() { close(release) }) }
			var calls atomic.Int32
			provider := completionService(t, func(input map[string]any) any {
				calls.Add(1)
				signaled.Do(func() { close(started) })
				<-release
				record := input["records"].([]any)[0].(map[string]any)
				return map[string]any{"artifacts": []any{map[string]any{"kind": "summary", "title": "In-flight interpretation", "content": map[string]any{"text": "Preserve source history"}, "sourceRecordIds": []any{record["id"]}}}}
			})
			defer func() { unblock(); provider.Close() }()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []any{"summary"}})
			if status != 200 {
				t.Fatal("settings failed")
			}
			projectID := jobProject(t, origin, "Changed completion snapshot", true, false)
			status, _ = captureRequest(t, origin, projectID, "", map[string]any{"type": "decision", "title": "Long immutable source", "authority": "explicit", "payload": map[string]any{"decision": strings.Repeat("evidence ", 1800)}})
			if status != 201 {
				t.Fatal("long capture failed")
			}
			status, response := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "analyze", "projectId": projectID})
			if status != 202 {
				t.Fatal("enqueue failed")
			}
			select {
			case <-started:
			case <-time.After(4 * time.Second):
				t.Fatal("completion not dispatched")
			}
			status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "model": "synthetic-new-model"})
			if status != 200 {
				t.Fatal("provider wait blocked configuration update")
			}
			unblock()
			waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "failed")
			status, response = curationRequest(t, origin, "GET", "/suggestions?projectId="+projectID, nil)
			if status != 200 || len(response["suggestions"].([]any)) != 0 || calls.Load() != 1 {
				t.Fatal("configuration change admitted a later batch or activated stale output")
			}
		})
	}
}

func TestExportWorkerCombinesCompleteHistoryWithServerCitations(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			provider := completionService(t, func(input map[string]any) any {
				record := input["records"].([]any)[0].(map[string]any)
				return map[string]any{"artifacts": []any{map[string]any{"kind": "export", "title": "Private draft", "content": map[string]any{"text": "Evidence-backed export", "markdown": "## Synthetic batch section\n\nPreserve the cited evidence."}, "sourceRecordIds": []any{record["id"]}}}}
			})
			defer provider.Close()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []any{}})
			if status != 200 {
				t.Fatal("provider settings failed")
			}
			projectID := jobProject(t, origin, "Complete private export", true, false)
			ids := []any{}
			for _, title := range []string{"First long decision", "Second long decision"} {
				status, response := captureRequest(t, origin, projectID, "", map[string]any{"type": "decision", "title": title, "authority": "explicit", "payload": map[string]any{"decision": strings.Repeat("evidence ", 1800)}})
				if status != 201 {
					t.Fatal("long capture failed")
				}
				ids = append(ids, response["record"].(map[string]any)["id"])
			}
			status, response := curationRequest(t, origin, "POST", "/summaries/export", map[string]any{"projectId": projectID, "format": "decisions"})
			if status != 202 {
				t.Fatalf("document generation enqueue failed: %d %+v", status, response)
			}
			job := waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "completed")
			status, response = curationRequest(t, origin, "GET", "/suggestions?projectId="+projectID, nil)
			if status != 200 || len(response["suggestions"].([]any)) != 1 {
				t.Fatal("complete export was not consolidated into one artifact")
			}
			artifact := response["suggestions"].([]any)[0].(map[string]any)
			markdown := artifact["content"].(map[string]any)["markdown"].(string)
			if artifact["kind"] != "export" || artifact["format"] != "decisions" || artifact["private"] != true || artifact["authority"] != "derived" || !reflect.DeepEqual(artifact["sourceRecordIds"], ids) || job["sourceCount"] != float64(2) || strings.Count(markdown, "## Synthetic batch section") < 2 || !strings.Contains(markdown, "AI-generated draft") || !strings.Contains(markdown, "## Source records") {
				t.Fatal("document lost complete source coverage, draft status or batch sections")
			}
			for _, id := range ids {
				if !strings.Contains(markdown, origin+"/?recordId="+id.(string)) {
					t.Fatal("server-owned source citation missing")
				}
			}
		})
	}
}

func TestAnalysisWorkerCreatesReviewableSourceBoundClassification(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			provider := completionService(t, func(input map[string]any) any {
				record := input["records"].([]any)[0].(map[string]any)
				return map[string]any{"artifacts": []any{map[string]any{"kind": "classification", "title": "Preserved knowledge", "content": map[string]any{"text": "Source-backed classification", "tags": []any{"knowledge"}, "classification": "architecture", "markdown": nil, "fromRecordId": nil, "toRecordId": nil, "relationshipType": nil}, "sourceRecordIds": []any{record["id"]}}}}
			})
			defer provider.Close()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []any{"classification"}})
			if status != 200 {
				t.Fatal("completion settings failed")
			}
			projectID := jobProject(t, origin, "Generated interpretation", true, false)
			record := curationCapture(t, origin, projectID, "Preserve raw knowledge")
			status, response := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "analyze", "projectId": projectID})
			if status != 202 {
				t.Fatal("analysis enqueue failed")
			}
			job := waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "completed")
			status, response = curationRequest(t, origin, "GET", "/suggestions?projectId="+projectID, nil)
			if status != 200 || len(response["suggestions"].([]any)) != 1 {
				t.Fatal("completed analysis did not publish its validated suggestion")
			}
			artifact := response["suggestions"].([]any)[0].(map[string]any)
			if artifact["kind"] != "classification" || artifact["status"] != "pending" || artifact["authority"] != "derived" || !reflect.DeepEqual(artifact["sourceRecordIds"], []any{record["id"]}) || !reflect.DeepEqual(artifact["privacyDependencies"], map[string]any{"version": float64(1), "recordIds": []any{record["id"]}, "projectIds": []any{projectID}, "crossProject": false}) || !reflect.DeepEqual(job["artifactIds"], []any{artifact["id"]}) {
				t.Fatal("generated suggestion lost source, privacy or job provenance")
			}
			status, _ = curationRequest(t, origin, "POST", "/suggestions/"+artifact["id"].(string)+"/accept", map[string]any{"expectedVersion": 1})
			if status != 200 {
				t.Fatal("generated valid artifact was not reviewable")
			}
			status, detail := curationRequest(t, origin, "GET", "/records/"+record["id"].(string), nil)
			if status != 200 || !reflect.DeepEqual(detail["record"], record) || !reflect.DeepEqual(detail["metadata"].(map[string]any)["tags"], []any{"knowledge"}) {
				t.Fatal("generated acceptance rewrote capture or lost curated classification")
			}
		})
	}
}

func TestAnalysisWorkerOwnsCompletePrivacyBeyondModelCitations(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			provider := completionService(t, func(input map[string]any) any {
				record := input["records"].([]any)[0].(map[string]any)
				return map[string]any{"artifacts": []any{map[string]any{"kind": "summary", "title": "Cross-project interpretation", "content": map[string]any{"text": "Derived from the complete supplied context"}, "sourceRecordIds": []any{record["id"]}, "privacyDependencies": map[string]any{"version": 1, "recordIds": []any{record["id"]}, "projectIds": []any{record["projectId"]}, "crossProject": false}, "authority": "explicit"}}}
			})
			defer provider.Close()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []any{"summary"}})
			if status != 200 {
				t.Fatal("settings failed")
			}
			first := jobProject(t, origin, "First privacy input", true, true)
			second := jobProject(t, origin, "Second privacy input", true, true)
			curationCapture(t, origin, first, "First source")
			curationCapture(t, origin, second, "Second source")
			status, response := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "analyze", "projectIds": []any{first, second}, "crossProject": true})
			if status != 202 {
				t.Fatal("cross analysis enqueue failed")
			}
			waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "completed")
			status, response = curationRequest(t, origin, "GET", "/suggestions?crossProject=true&projectIds="+first+"&projectIds="+second, nil)
			if status != 200 || len(response["suggestions"].([]any)) != 1 {
				t.Fatal("authorized cross output missing")
			}
			artifact := response["suggestions"].([]any)[0].(map[string]any)
			deps := artifact["privacyDependencies"].(map[string]any)
			if artifact["authority"] != "derived" || deps["crossProject"] != true || len(deps["recordIds"].([]any)) != 2 || len(deps["projectIds"].([]any)) != 2 || len(artifact["sourceRecordIds"].([]any)) != 1 {
				t.Fatal("model narrowed privacy inputs or promoted authority")
			}
			cited := artifact["projectIds"].([]any)[0].(string)
			uncited := first
			if cited == first {
				uncited = second
			}
			status, response = curationRequest(t, origin, "GET", "/suggestions?projectId="+cited, nil)
			if status != 200 || len(response["suggestions"].([]any)) != 0 {
				t.Fatal("cited-only scope leaked uncited project influence")
			}
			status, project := projectRequest(t, origin, "GET", "/"+uncited, nil, restoredToken)
			if status != 200 {
				t.Fatal("input project missing")
			}
			settings := project["project"].(map[string]any)["settings"].(map[string]any)
			settings["aiProcessing"] = false
			status, _ = projectRequest(t, origin, "PATCH", "/"+uncited+"/settings", map[string]any{"expectedVersion": 2, "settings": settings}, restoredToken)
			if status != 200 {
				t.Fatal("revocation failed")
			}
			status, response = curationRequest(t, origin, "GET", "/suggestions", nil)
			if status != 200 || len(response["suggestions"].([]any)) != 0 {
				t.Fatal("revoked uncited inputs remained readable")
			}
		})
	}
}

func TestAnalysisWorkerValidatesMandatoryFieldsBeforeModeFiltering(t *testing.T) {
	provider := completionService(t, func(input map[string]any) any {
		record := input["records"].([]any)[0].(map[string]any)
		return map[string]any{"artifacts": []any{map[string]any{"kind": "summary", "title": "Malformed unselected mode", "content": map[string]any{"text": nil}, "sourceRecordIds": []any{record["id"]}}}}
	})
	defer provider.Close()
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []any{"classification"}})
	if status != 200 {
		t.Fatal("settings failed")
	}
	projectID := jobProject(t, origin, "Malformed model output", true, false)
	curationCapture(t, origin, projectID, "Keep processing trustworthy")
	status, response := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "analyze", "projectId": projectID})
	if status != 202 {
		t.Fatal("enqueue failed")
	}
	job := waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "failed")
	if job["attempts"] != float64(3) || job["lastError"] == nil {
		t.Fatal("malformed output did not exhaust bounded attempts with a safe failure")
	}
	status, response = curationRequest(t, origin, "GET", "/suggestions?projectId="+projectID, nil)
	if status != 200 || len(response["suggestions"].([]any)) != 0 {
		t.Fatal("malformed output produced a suggestion")
	}
}

func TestAnalysisWorkerSupportsExistingKindsAndReportsInsufficientEvidence(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			kinds := []string{"summary", "classification", "duplicate_candidate", "relationship_candidate", "contradiction", "cluster", "pattern", "recommendation"}
			provider := completionService(t, func(input map[string]any) any {
				records := input["records"].([]any)
				first := records[0].(map[string]any)["id"]
				second := records[1].(map[string]any)["id"]
				artifacts := []any{}
				for _, kind := range kinds {
					content := map[string]any{"text": "Source-backed " + kind}
					ids := []any{first, second}
					if kind == "classification" {
						ids = []any{first}
						content["tags"] = []any{"classified"}
					}
					if kind == "relationship_candidate" || kind == "contradiction" {
						content["fromRecordId"], content["toRecordId"] = first, second
						content["relationshipType"] = "supports"
						if kind == "contradiction" {
							content["relationshipType"] = "contradicts"
						}
					}
					if kind == "export" {
						content["markdown"] = "## Source-backed document"
					}
					artifacts = append(artifacts, map[string]any{"kind": kind, "title": "Supported " + kind, "content": content, "sourceRecordIds": ids})
				}
				artifacts = append(artifacts, map[string]any{"kind": "pattern", "title": "Unsupported singleton pattern", "content": map[string]any{"text": "One source is insufficient"}, "sourceRecordIds": []any{first}})
				return map[string]any{"artifacts": artifacts}
			})
			defer provider.Close()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": kinds})
			if status != 200 {
				t.Fatal("settings failed")
			}
			projectID := jobProject(t, origin, "All supported interpretations", true, false)
			curationCapture(t, origin, projectID, "First independent source")
			curationCapture(t, origin, projectID, "Second independent source")
			status, response := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "analyze", "projectId": projectID})
			if status != 202 {
				t.Fatal("enqueue failed")
			}
			job := waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "completed")
			if !reflect.DeepEqual(job["warnings"], []any{map[string]any{"code": "AI_INSUFFICIENT_EVIDENCE", "kind": "pattern", "count": float64(1)}}) {
				t.Fatal("unsupported evidence was not reported")
			}
			status, response = curationRequest(t, origin, "GET", "/suggestions?projectId="+projectID, nil)
			if status != 200 || len(response["suggestions"].([]any)) != len(kinds) {
				t.Fatal("valid kinds were lost or unsupported output activated")
			}
			seen := map[string]bool{}
			for _, item := range response["suggestions"].([]any) {
				artifact := item.(map[string]any)
				seen[artifact["kind"].(string)] = true
				if artifact["authority"] != "derived" || artifact["status"] != "pending" || artifact["private"] != true {
					t.Fatal("analysis promoted authority or lost pending/private status")
				}
			}
			for _, kind := range kinds {
				if !seen[kind] {
					t.Fatalf("missing supported kind %s", kind)
				}
			}
		})
	}
}
