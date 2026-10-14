package backend_test

import (
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

func projectMemory(t *testing.T, origin, id string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest("GET", origin+"/api/v1/projects/"+id+"/context", nil)
	req.Header.Set("Authorization", "Bearer "+restoredToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&body) != nil {
		t.Fatalf("project context unavailable: %d", res.StatusCode)
	}
	return body
}
func TestProjectContextResumesCurrentStateAndKnowledge(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Context"}, restoredToken)
			id := project["project"].(map[string]any)["id"].(string)
			for _, record := range []map[string]any{{"type": "project_state", "title": "Current state", "authority": "observed", "payload": map[string]any{"state": "active"}}, {"type": "decision", "title": "Current decision", "authority": "explicit", "payload": map[string]any{"decision": "Use Go."}}, {"type": "constraint", "title": "Current constraint", "authority": "explicit", "payload": map[string]any{"constraint": "Keep history immutable."}}} {
				status, _ := captureRequest(t, origin, id, "", record)
				if status != 201 {
					t.Fatal("context capture failed")
				}
			}
			context := projectMemory(t, origin, id)
			if context["project"].(map[string]any)["id"] != id || context["currentState"].(map[string]any)["payload"].(map[string]any)["state"] != "active" || len(context["recentDecisions"].([]any)) != 1 || len(context["constraints"].([]any)) != 1 || context["counts"].(map[string]any)["current"] != float64(3) {
				t.Fatal("deterministic context omitted current knowledge")
			}
		})
	}
}

func TestProjectContextKeepsHistoricalAndUnverifiedKnowledgeSeparate(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Lifecycle"}, restoredToken)
			id := project["project"].(map[string]any)["id"].(string)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			rows := []struct {
				kind, id string
				value    map[string]any
			}{
				{"record", "rec_context_now", map[string]any{"type": "project_state", "payload": map[string]any{"state": "active"}}},
				{"record", "rec_context_old", map[string]any{"type": "project_state", "payload": map[string]any{"state": "paused"}}},
				{"record", "rec_context_partial", map[string]any{"type": "finding"}},
				{"record", "rec_context_archive", map[string]any{"type": "finding"}},
				{"record", "rec_context_legacy", map[string]any{"type": "constraint"}},
				{"record", "rec_context_review", map[string]any{"type": "finding"}},
				{"record", "rec_context_import", map[string]any{"type": "decision", "provenance": map[string]any{"import": map[string]any{"sourceName": "synthetic"}}}},
				{"record", "rec_context_proposed", map[string]any{"type": "finding"}},
				{"metadata", "rec_context_archive", map[string]any{"archived": true}},
				{"metadata", "rec_context_legacy", map[string]any{"legacyStatus": "superseded"}},
				{"metadata", "rec_context_review", map[string]any{"legacyStatus": "superseded_or_partial"}},
				{"relationship", "rel_context_replace", map[string]any{"fromRecordId": "rec_context_now", "toRecordId": "rec_context_old", "type": "replaces", "status": "accepted", "note": "Updated state"}},
				{"relationship", "rel_context_partial", map[string]any{"fromRecordId": "rec_context_now", "toRecordId": "rec_context_partial", "type": "partially_replaces", "status": "accepted"}},
				{"relationship", "rel_context_proposed", map[string]any{"fromRecordId": "rec_context_now", "toRecordId": "rec_context_proposed", "type": "replaces", "status": "proposed"}},
			}
			for _, row := range rows {
				row.value["id"], row.value["version"], row.value["createdAt"], row.value["recordedAt"] = row.id, 1, "2026-10-01T00:00:00.000Z", "2026-10-01T00:00:00.000Z"
				if row.kind == "record" {
					row.value["projectId"] = id
				}
				data, _ := json.Marshal(row.value)
				if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES($1,$2,$3,1,'2026-10-01T00:00:00.000Z')`, row.kind, row.id, string(data)); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			db.Close()
			context := projectMemory(t, origin, id)
			if context["counts"].(map[string]any)["total"] != float64(8) || context["counts"].(map[string]any)["current"] != float64(2) || context["currentState"].(map[string]any)["id"] != "rec_context_now" || len(context["historicalRecords"].([]any)) != 3 || len(context["requiresReview"].([]any)) != 2 || len(context["partiallySuperseded"].([]any)) != 1 || len(context["relationships"].([]any)) != 3 {
				t.Fatal("context promoted historical/unverified evidence or ignored accepted relationships")
			}
			for _, entry := range context["historicalRecords"].([]any) {
				record := entry.(map[string]any)
				if record["id"] == "rec_context_old" {
					links := record["replacedBy"].([]any)
					if len(links) != 1 || links[0].(map[string]any)["note"] != "Updated state" {
						t.Fatal("replacement provenance was lost")
					}
				}
			}
		})
	}
}

func TestProjectContextOrdersPreciseStateAndBoundsHistory(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			_, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "History"}, restoredToken)
			id := project["project"].(map[string]any)["id"].(string)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 25; i++ {
				rowID := fmt.Sprintf("rec_state_%02d", i)
				row := map[string]any{"id": rowID, "projectId": id, "version": 1, "createdAt": "2026-10-01T00:00:00.000Z", "recordedAt": "2026-10-01T00:00:00.000Z", "happenedAt": fmt.Sprintf("2026-10-01T12:00:00.000000000%02dZ", i), "type": "project_state", "payload": map[string]any{"state": rowID}}
				data, _ := json.Marshal(row)
				if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('record',$1,$2,1,'2026-10-01T00:00:00.000Z')`, rowID, string(data)); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			db.Close()
			memory := projectMemory(t, origin, id)
			if memory["currentState"].(map[string]any)["id"] != "rec_state_24" || len(memory["state"].([]any)) != 20 || len(memory["stateHistory"].([]any)) != 20 || memory["counts"].(map[string]any)["total"] != float64(25) {
				t.Fatal("context rounded state time or lost bounded history/counts")
			}
			for i, entry := range memory["stateHistory"].([]any) {
				if entry.(map[string]any)["id"] != fmt.Sprintf("rec_state_%02d", 24-i) {
					t.Fatal("state history was not deterministically ordered")
				}
			}
		})
	}
}

func TestRealStdioFullResumptionSurvivesGoRestart(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, local := githubHistoricalBrowser(t, engine)
			private, key := githubSyntheticKey(t)
			workspace := t.TempDir()
			for _, args := range [][]string{{"init", "-q", "-b", "main", workspace}, {"-C", workspace, "remote", "add", "origin", "https://github.com/synthetic/resume.git"}, {"-C", workspace, "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "commit", "-q", "--allow-empty", "-m", "Synthetic"}} {
				cmd := exec.Command("git", args...)
				cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
				if cmd.Run() != nil {
					t.Fatal("synthetic Git fixture failed")
				}
			}
			// Keep this fixture's capture provenance stable across its own mirror
			// writes; unrelated workspace changes must still change retry identity.
			if err := os.WriteFile(filepath.Join(workspace, ".git", "info", "exclude"), []byte("/scratchpad/decisions.jsonl\n"), 0600); err != nil {
				t.Fatal("synthetic mirror exclusion failed")
			}
			var origin string
			state := filepath.Join(t.TempDir(), "resume.json")
			for _, phase := range []string{"capture", "verify"} {
				t.Run(phase, func(t *testing.T) {
					if origin == "" {
						origin = startServer(t, path, databaseURL)
						enrollLocalSSH(t, origin, private, key, local)
						_, project := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": "https://github.com/synthetic/resume.git"}}}, restoredToken)
						settings := map[string]any{"repoMirroring": map[string]any{"enabled": true, "recordTypes": []string{"decision", "adr", "business_decision"}}, "crossProjectAnalysis": true, "aiProcessing": false}
						status, _ := projectRequest(t, origin, "PATCH", "/"+project["project"].(map[string]any)["id"].(string)+"/settings", map[string]any{"settings": settings, "expectedVersion": 1}, restoredToken)
						if status != 200 {
							t.Fatal("mirror fixture configuration failed")
						}
					} else {
						origin = startServer(t, path, databaseURL, strings.TrimPrefix(origin, "http://"))
					}
					cmd := exec.Command(integrationBinary, "-url", origin, "-public-key", private+".pub", "-signing-key", private, "-workspace", workspace, "-binary", mcpBinary, "-phase", phase, "-state", state, "-mirror")
					cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "SCRATCHPAD_MIRROR_PATH=scratchpad/decisions.jsonl")
					out, err := cmd.CombinedOutput()
					if err != nil || !strings.Contains(string(out), "PASS "+phase) {
						t.Fatalf("full stdio resumption failed: %v %s", err, out)
					}
					mirrored, err := os.ReadFile(filepath.Join(workspace, "scratchpad", "decisions.jsonl"))
					if err != nil || len(strings.Split(strings.TrimSpace(string(mirrored)), "\n")) != 3 {
						t.Fatal("local mirror failed or duplicated records across retry/restart")
					}
				})
			}
		})
	}
}
