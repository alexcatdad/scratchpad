package backend_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func agentEventRequest(t *testing.T, origin, method, query string, body any, token string) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	switch value := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case string:
		reader = bytes.NewReader([]byte(value))
	default:
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, origin+"/api/v1/agent-events"+query, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var result map[string]any
	if json.NewDecoder(res.Body).Decode(&result) != nil {
		t.Fatalf("non-JSON agent event response: %d", res.StatusCode)
	}
	return res.StatusCode, result
}

func syntheticAgentEvent(id, kind, remote string) map[string]any {
	event := map[string]any{"schemaVersion": 1, "eventId": id, "agent": "claude-code", "kind": kind, "sessionId": "session-1", "occurredAt": "2026-11-10T12:00:00.123456789Z", "text": "synthetic " + id, "exact": json.Number("1.2500")}
	if remote != "" {
		event["gitContext"] = map[string]any{"remote": remote}
	}
	return event
}

// agentEventProject resolves a project from a Git remote and, when asked, opts
// it in to agent events.
func agentEventProject(t *testing.T, origin, remote string, optIn bool) string {
	t.Helper()
	status, body := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": remote}}}, restoredToken)
	if status != 200 {
		t.Fatalf("project resolution failed: %d", status)
	}
	id := body["project"].(map[string]any)["id"].(string)
	if optIn {
		settings := map[string]any{"repoMirroring": map[string]any{"enabled": false, "recordTypes": []string{"decision"}}, "crossProjectAnalysis": true, "aiProcessing": false, "agentEvents": true}
		status, body = projectRequest(t, origin, "PATCH", "/"+id+"/settings", map[string]any{"settings": settings, "expectedVersion": 1}, restoredToken)
		if status != 200 || body["settings"].(map[string]any)["agentEvents"] != true {
			t.Fatalf("agent events could not be enabled: %d %v", status, body)
		}
	}
	return id
}

func TestAgentEventsAreStoredOnlyForOptedInProjects(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			const optedRemote, plainRemote = "https://git.invalid/owner/opted.git", "https://git.invalid/owner/plain.git"
			opted := agentEventProject(t, origin, optedRemote, true)
			agentEventProject(t, origin, plainRemote, false)

			status, settings := projectRequest(t, origin, "GET", "/"+opted+"/settings", nil, restoredToken)
			if status != 200 || settings["settings"].(map[string]any)["agentEvents"] != true {
				t.Fatalf("opt-in was not persisted: %d %v", status, settings)
			}

			batch := map[string]any{"events": []any{
				syntheticAgentEvent("event-1", "tool_failure", optedRemote),
				syntheticAgentEvent("event-2", "prompt", "git@git.invalid:owner/opted.git"),
				syntheticAgentEvent("event-3", "prompt", plainRemote),
				syntheticAgentEvent("event-4", "prompt", "https://git.invalid/owner/never-seen.git"),
				syntheticAgentEvent("event-5", "prompt", ""),
				syntheticAgentEvent("event-6", "not_a_kind", optedRemote),
				map[string]any{"eventId": "event-7"},
				"not an object",
			}}
			status, result := agentEventRequest(t, origin, "POST", "", batch, restoredToken)
			if status != 200 {
				t.Fatalf("batch rejected: %d %v", status, result)
			}
			discarded := result["discarded"].(map[string]any)
			if result["accepted"] != float64(2) || result["duplicate"] != float64(0) || discarded["notEnabled"] != float64(1) || discarded["unresolved"] != float64(2) || discarded["invalid"] != float64(3) {
				t.Fatalf("unexpected counts: %v", result)
			}

			// Sending the same events again stores nothing new.
			status, result = agentEventRequest(t, origin, "POST", "", batch, restoredToken)
			if status != 200 || result["accepted"] != float64(0) || result["duplicate"] != float64(2) {
				t.Fatalf("replay was not idempotent: %d %v", status, result)
			}

			status, listed := agentEventRequest(t, origin, "GET", "", nil, restoredToken)
			events := listed["events"].([]any)
			if status != 200 || len(events) != 2 || listed["hasMore"] != false {
				t.Fatalf("unexpected listing: %d %v", status, listed)
			}
			first := events[0].(map[string]any)
			if first["projectId"] != opted || first["kind"] != "tool_failure" || first["agent"] != "claude-code" || first["sessionId"] != "session-1" || first["occurredAt"] != "2026-11-10T12:00:00.123Z" || !strings.HasPrefix(first["id"].(string), "evt_") {
				t.Fatalf("unexpected stored event: %v", first)
			}
			if first["event"].(map[string]any)["text"] != "synthetic event-1" {
				t.Fatalf("event body was not preserved: %v", first["event"])
			}
			if events[1].(map[string]any)["projectId"] != opted {
				t.Fatal("an SCP-style remote did not resolve to the same project")
			}

			// No project was created for the unknown repository.
			status, projects := projectRequest(t, origin, "GET", "", nil, restoredToken)
			if status != 200 || len(projects["projects"].([]any)) != 2 {
				t.Fatalf("agent events changed the project list: %d", len(projects["projects"].([]any)))
			}
		})
	}
}

func TestAgentEventNumbersSurviveExactly(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			const remote = "https://git.invalid/owner/numbers.git"
			agentEventProject(t, origin, remote, true)
			body := `{"events":[{"schemaVersion":1,"eventId":"n1","agent":"codex","kind":"turn_end","sessionId":"s","occurredAt":"2026-11-10T12:00:00Z","gitContext":{"remote":"` + remote + `"},"tool":{"durationMs":12345678901234567890}}]}`
			if status, result := agentEventRequest(t, origin, "POST", "", body, restoredToken); status != 200 || result["accepted"] != float64(1) {
				t.Fatalf("event rejected: %d %v", status, result)
			}
			res, err := http.NewRequest("GET", origin+"/api/v1/agent-events", nil)
			if err != nil {
				t.Fatal(err)
			}
			res.Header.Set("Authorization", "Bearer "+restoredToken)
			response, err := http.DefaultClient.Do(res)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var raw bytes.Buffer
			if _, err = raw.ReadFrom(response.Body); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(raw.String(), "12345678901234567890") {
				t.Fatalf("large integer was not preserved: %s", raw.String())
			}
		})
	}
}

func TestAgentEventCursorPagesInStoredOrder(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			const remoteA, remoteB = "https://git.invalid/owner/a.git", "https://git.invalid/owner/b.git"
			projectA := agentEventProject(t, origin, remoteA, true)
			agentEventProject(t, origin, remoteB, true)
			events := []any{}
			for i := range 7 {
				remote := remoteA
				if i%2 == 1 {
					remote = remoteB
				}
				events = append(events, syntheticAgentEvent(fmt.Sprintf("page-%d", i), "prompt", remote))
			}
			if status, result := agentEventRequest(t, origin, "POST", "", map[string]any{"events": events}, restoredToken); status != 200 || result["accepted"] != float64(7) {
				t.Fatalf("seed failed: %d %v", status, result)
			}

			var seen []string
			cursor := ""
			for range 4 {
				status, page := agentEventRequest(t, origin, "GET", "?limit=3&after="+url.QueryEscape(cursor), nil, restoredToken)
				if status != 200 {
					t.Fatalf("page failed: %d %v", status, page)
				}
				for _, item := range page["events"].([]any) {
					seen = append(seen, item.(map[string]any)["event"].(map[string]any)["eventId"].(string))
				}
				cursor = page["cursor"].(string)
				if page["hasMore"] != true {
					break
				}
			}
			if strings.Join(seen, ",") != "page-0,page-1,page-2,page-3,page-4,page-5,page-6" {
				t.Fatalf("pages out of order or incomplete: %v", seen)
			}

			// A reader at the end sees nothing, keeps its cursor, and then sees
			// exactly the events stored afterwards.
			status, empty := agentEventRequest(t, origin, "GET", "?after="+url.QueryEscape(cursor), nil, restoredToken)
			if status != 200 || len(empty["events"].([]any)) != 0 || empty["cursor"] != cursor || empty["hasMore"] != false {
				t.Fatalf("end of feed: %d %v", status, empty)
			}
			agentEventRequest(t, origin, "POST", "", map[string]any{"events": []any{syntheticAgentEvent("late", "prompt", remoteA)}}, restoredToken)
			_, later := agentEventRequest(t, origin, "GET", "?after="+url.QueryEscape(cursor), nil, restoredToken)
			if items := later["events"].([]any); len(items) != 1 || items[0].(map[string]any)["event"].(map[string]any)["eventId"] != "late" {
				t.Fatalf("event stored after the cursor was not delivered: %v", later)
			}

			_, filtered := agentEventRequest(t, origin, "GET", "?projectId="+url.QueryEscape(projectA), nil, restoredToken)
			if len(filtered["events"].([]any)) != 5 {
				t.Fatalf("project filter returned %d events", len(filtered["events"].([]any)))
			}
			_, bySession := agentEventRequest(t, origin, "GET", "?sessionId=other", nil, restoredToken)
			if len(bySession["events"].([]any)) != 0 {
				t.Fatal("session filter did not apply")
			}
		})
	}
}

func TestAgentEventRequestsAreValidatedAndAuthenticated(t *testing.T) {
	path, databaseURL, _ := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	if status, _ := agentEventRequest(t, origin, "POST", "", map[string]any{"events": []any{syntheticAgentEvent("e", "prompt", "")}}, ""); status != 401 {
		t.Fatalf("unauthenticated ingest: %d", status)
	}
	if status, _ := agentEventRequest(t, origin, "GET", "", nil, ""); status != 401 {
		t.Fatalf("unauthenticated listing: %d", status)
	}
	tooMany := make([]any, 201)
	for i := range tooMany {
		tooMany[i] = syntheticAgentEvent(fmt.Sprintf("many-%d", i), "prompt", "")
	}
	for name, body := range map[string]any{
		"empty batch":   map[string]any{"events": []any{}},
		"missing batch": map[string]any{},
		"unknown field": map[string]any{"events": []any{syntheticAgentEvent("e", "prompt", "")}, "extra": true},
		"oversized":     map[string]any{"events": tooMany},
		"not json":      "nope",
		"trailing data": `{"events":[{}]} {}`,
	} {
		if status, result := agentEventRequest(t, origin, "POST", "", body, restoredToken); status != 400 {
			t.Errorf("%s: status %d %v", name, status, result)
		}
	}
	for _, query := range []string{"?after=not-a-cursor", "?limit=0", "?limit=501", "?limit=abc"} {
		if status, _ := agentEventRequest(t, origin, "GET", query, nil, restoredToken); status != 400 {
			t.Errorf("listing %s: status %d", query, status)
		}
	}
}

func TestAgentEventsExpireAndAreNotExported(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			t.Setenv("SCRATCHPAD_AGENT_EVENT_RETENTION_DAYS", "2")
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin, stop := startServerWithStop(t, path, databaseURL, "")
			const remote = "https://git.invalid/owner/retention.git"
			projectID := agentEventProject(t, origin, remote, true)
			if status, result := agentEventRequest(t, origin, "POST", "", map[string]any{"events": []any{syntheticAgentEvent("fresh", "prompt", remote)}}, restoredToken); status != 200 || result["accepted"] != float64(1) {
				t.Fatalf("seed failed: %d %v", status, result)
			}

			status, exported := postJSONWithToken(t, origin, "/api/v1/export", map[string]any{}, restoredToken)
			if status != 200 || strings.Contains(exported, "agent_events") || strings.Contains(exported, "synthetic fresh") {
				t.Fatalf("agent events leaked into the export: %d", status)
			}
			stop()

			driver, source := "sqlite", path
			if engine == "postgres" {
				driver, source = "pgx", databaseURL
			}
			db, err := sql.Open(driver, source)
			if err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-72 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
			if _, err = db.Exec(`INSERT INTO agent_events(id,project_id,credential_id,session_id,agent,kind,occurred_at,received_at,data) VALUES($1,$2,'credential','session','codex','prompt',$3,$3,'{"eventId":"expired"}')`, "evt_expired", projectID, old); err != nil {
				t.Fatal(err)
			}
			db.Close()

			origin = startServer(t, path, databaseURL)
			deadline := time.Now().Add(5 * time.Second)
			for {
				_, listed := agentEventRequest(t, origin, "GET", "", nil, restoredToken)
				events := listed["events"].([]any)
				if len(events) == 1 && events[0].(map[string]any)["event"].(map[string]any)["eventId"] == "fresh" {
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("expired event was not pruned: %v", listed)
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
}

func postJSONWithToken(t *testing.T, origin, path string, body any, token string) (int, string) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", origin+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var raw bytes.Buffer
	if _, err = raw.ReadFrom(res.Body); err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, raw.String()
}

// Records created before and after migration 4 appear once each in
// record_changes, in creation order, so the record change feed can page on
// its sequence.
func TestRecordChangesTrackEveryRecordInOrder(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin, stop := startServerWithStop(t, path, databaseURL, "")
			_, body := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Changes"}, restoredToken)
			projectID := body["project"].(map[string]any)["id"].(string)
			var created []string
			for i := range 3 {
				status, captured := captureRequest(t, origin, projectID, fmt.Sprintf("change-%d", i), map[string]any{"type": "finding", "title": fmt.Sprintf("Change %d", i), "authority": "observed", "confidence": "high", "payload": map[string]any{"finding": "synthetic"}})
				if status != 201 && status != 200 {
					t.Fatalf("capture failed: %d %v", status, captured)
				}
				created = append(created, captured["record"].(map[string]any)["id"].(string))
			}
			stop()

			driver, source := "sqlite", path
			if engine == "postgres" {
				driver, source = "pgx", databaseURL
			}
			db, err := sql.Open(driver, source)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var records, changes, distinct int
			if err = db.QueryRow(`SELECT (SELECT count(*) FROM entities WHERE kind='record'),(SELECT count(*) FROM record_changes),(SELECT count(DISTINCT record_id) FROM record_changes)`).Scan(&records, &changes, &distinct); err != nil {
				t.Fatal(err)
			}
			if records == 0 || changes != records || distinct != records {
				t.Fatalf("records=%d changes=%d distinct=%d", records, changes, distinct)
			}
			rows, err := db.Query(`SELECT record_id FROM record_changes ORDER BY seq DESC LIMIT 3`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var latest []string
			for rows.Next() {
				var id string
				if err = rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				latest = append([]string{id}, latest...)
			}
			if strings.Join(latest, ",") != strings.Join(created, ",") {
				t.Fatalf("latest changes %v do not match creation order %v", latest, created)
			}
		})
	}
}

// The whole path with real processes: the `scratchpad hook` command hands a
// hook payload to the local capture service, which redacts it, queues it and
// delivers it to the server, where it is stored for an opted-in project only.
func TestAgentHookEventReachesTheServerThroughTheLocalService(t *testing.T) {
	cli := filepath.Join(t.TempDir(), "scratchpad")
	build := exec.Command("go", "build", "-race", "-o", cli, "./cmd/scratchpad")
	build.Dir = "../mcp"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("scratchpad command build failed: %v %s", err, out)
	}
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, local := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			private, key := githubSyntheticKey(t)
			enrollLocalSSH(t, origin, private, key, local)
			serviceSocket, stopService := startConfiguredDurableService(t, origin, t.TempDir(), filepath.Dir(private), private)
			defer stopService()

			const remote = "https://git.invalid/synthetic/hooked.git"
			projectID := agentEventProject(t, origin, remote, true)
			workspace, elsewhere := t.TempDir(), t.TempDir()
			createDisposableGitRepo(t, workspace, remote)
			createDisposableGitRepo(t, elsewhere, "https://git.invalid/synthetic/not-opted-in.git")
			if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("synthetic rules\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			token := "ghp_" + strings.Repeat("a1B2", 9)
			hook := func(agent string, payload map[string]any) {
				t.Helper()
				data, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(cli, "hook", "--agent", agent)
				cmd.Stdin = bytes.NewReader(data)
				cmd.Env = append(os.Environ(), "SCRATCHPAD_SERVICE_SOCKET="+serviceSocket, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "HOME="+t.TempDir())
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				if err = cmd.Run(); err != nil || stdout.Len() != 0 || stderr.Len() != 0 {
					t.Fatalf("hook command: err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
				}
			}
			hook("claude-code", map[string]any{"hook_event_name": "SessionStart", "session_id": "live-1", "cwd": workspace, "source": "startup", "model": "synthetic-model"})
			hook("claude-code", map[string]any{"hook_event_name": "PostToolUseFailure", "session_id": "live-1", "prompt_id": "p1", "cwd": workspace, "tool_name": "Bash",
				"tool_input": map[string]any{"command": "git push https://x:" + token + "@git.invalid/synthetic/hooked.git"}, "error": "Exit code 128\nremote: denied for " + token, "is_interrupt": false})
			hook("claude-code", map[string]any{"hook_event_name": "PostToolUse", "session_id": "live-1", "cwd": workspace, "tool_name": "Bash", "tool_input": map[string]any{"command": "echo ok"}, "tool_response": map[string]any{"stdout": "ok"}})
			hook("codex", map[string]any{"hook_event_name": "Interrupt", "session_id": "live-2", "turn_id": "t1", "cwd": workspace, "model": "synthetic-codex"})
			hook("codex", map[string]any{"hook_event_name": "UserPromptSubmit", "session_id": "live-3", "cwd": elsewhere, "prompt": "this project never opted in"})

			var events []any
			deadline := time.Now().Add(15 * time.Second)
			for {
				_, listed := agentEventRequest(t, origin, "GET", "", nil, restoredToken)
				events = listed["events"].([]any)
				if len(events) == 3 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("server holds %d agent events, want 3: %v", len(events), listed)
				}
				time.Sleep(100 * time.Millisecond)
			}
			kinds := map[string]map[string]any{}
			for _, item := range events {
				entry := item.(map[string]any)
				if entry["projectId"] != projectID {
					t.Fatalf("event stored under the wrong project: %v", entry)
				}
				kinds[entry["kind"].(string)] = entry["event"].(map[string]any)
			}
			start, failure, interrupt := kinds["session_start"], kinds["tool_failure"], kinds["interrupt"]
			if start == nil || failure == nil || interrupt == nil {
				t.Fatalf("missing kinds: %v", kinds)
			}
			files, _ := start["instructionFiles"].([]any)
			if len(files) != 1 || !strings.HasSuffix(files[0].(map[string]any)["path"].(string), "AGENTS.md") || len(files[0].(map[string]any)["sha256"].(string)) != 64 {
				t.Fatalf("session start did not record the instruction file: %v", start["instructionFiles"])
			}
			encoded, err := json.Marshal(failure)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), token) || !strings.Contains(string(encoded), "secret://redacted/") {
				t.Fatalf("credential reached the server: %s", encoded)
			}
			if interrupt["agent"] != "codex" || interrupt["turnId"] != "t1" || interrupt["model"] != "synthetic-codex" {
				t.Fatalf("unexpected interrupt event: %v", interrupt)
			}
		})
	}
}
