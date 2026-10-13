package backend_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestRAGLexicalFallbackRechecksCurrentScopeSourcesAndCitations(t *testing.T) {
	for _, mode := range []string{"archived", "source", "citation", "consent", "endpoint_consent", "auth", "unrelated_tags"} {
		t.Run(mode, func(t *testing.T) {
			path, databaseURL := ragInstance(t)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0, "embeddingDimensions": 2})
			if status != 200 {
				t.Fatal("disabled provider fixture failed")
			}
			project := jobProject(t, origin, "Lexical snapshot participant", true, true)
			source := curationCapture(t, origin, project, "LEXICAL_SNAPSHOT_WITNESS")
			other := ""
			var related map[string]any
			if mode == "citation" || mode == "endpoint_consent" {
				other = jobProject(t, origin, "Lexical cited endpoint", true, true)
				endpoint := curationCapture(t, origin, other, "Different endpoint evidence")
				status, response := curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": source["id"], "toRecordId": endpoint["id"], "type": "supports", "authority": "explicit", "note": "Selected lexical citation."})
				if status != 201 {
					t.Fatal("cited edge fixture failed")
				}
				related = response["relationship"].(map[string]any)
			}
			db, err := sql.Open("pgx", databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			barrier, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer barrier.Rollback()
			// Disabled AI excludes background workers. The passage barrier pauses
			// fallback coverage after its auth/scope snapshot, without locking
			// source, lifecycle, project or cited-edge writes.
			if _, err = barrier.ExecContext(ctx, "LOCK TABLE rag_passages IN ACCESS EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}
			type response struct {
				status int
				body   map[string]any
			}
			done := make(chan response, 1)
			body := map[string]any{"projectId": project, "query": "LEXICAL_SNAPSHOT_WITNESS"}
			if other != "" {
				delete(body, "projectId")
				body["crossProject"] = true
			}
			go func() {
				status, result := curationRequest(t, origin, "POST", "/rag/retrieve", body)
				done <- response{status, result}
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var waiting bool
				if err = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE relation='rag_passages'::regclass AND NOT granted)").Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("fallback did not reach snapshot barrier")
				}
				time.Sleep(10 * time.Millisecond)
			}
			expected, code := 503, "AI_CONFIGURATION_CHANGED"
			switch mode {
			case "archived":
				status, _ = curationRequest(t, origin, "PATCH", "/records/"+source["id"].(string)+"/metadata", map[string]any{"expectedVersion": 1, "archived": true})
			case "unrelated_tags":
				status, _ = curationRequest(t, origin, "PATCH", "/records/"+source["id"].(string)+"/metadata", map[string]any{"expectedVersion": 1, "tags": []any{"unrelated"}})
				expected = 200
			case "source":
				// Controlled historical corruption is input; immutable captures are
				// never changed through an application source mutation API.
				_, err = db.ExecContext(ctx, `UPDATE entities SET data=jsonb_set(data,'{content}','"Changed source during lexical retrieval."') WHERE kind='record' AND id=$1`, source["id"])
				if err != nil {
					t.Fatal(err)
				}
				status = 200
			case "citation":
				status, _ = curationRequest(t, origin, "POST", "/relationships/"+related["id"].(string)+"/reject", map[string]any{"expectedVersion": related["version"]})
			case "consent", "endpoint_consent":
				withdrawn := project
				if mode == "endpoint_consent" {
					withdrawn = other
				}
				lookupStatus, response := projectRequest(t, origin, "GET", "/"+withdrawn, nil, restoredToken)
				if lookupStatus != 200 {
					t.Fatal("project lookup failed")
				}
				current := response["project"].(map[string]any)
				settings := current["settings"].(map[string]any)
				settings["aiProcessing"] = false
				status, _ = projectRequest(t, origin, "PATCH", "/"+withdrawn+"/settings", map[string]any{"expectedVersion": current["version"], "settings": settings}, restoredToken)
				expected, code = 403, "AI_NOT_ALLOWED"
			case "auth":
				// A supported revoked-session fixture tests the final public HTTP
				// authentication boundary without exposing real credentials.
				id := fmt.Sprintf("%x", sha256.Sum256([]byte(restoredToken)))
				if _, err = db.ExecContext(ctx, `DELETE FROM entities WHERE kind='session' AND id=$1`, id); err != nil {
					t.Fatal(err)
				}
				status, expected, code = 200, 401, "AUTH_INVALID"
			}
			if status != 200 {
				t.Fatalf("fixture change failed: HTTP%d", status)
			}
			if err = barrier.Rollback(); err != nil {
				t.Fatal(err)
			}
			result := <-done
			if result.status != expected {
				t.Fatalf("fallback returned stale snapshot evidence: mode%s HTTP%d %v", mode, result.status, result.body)
			}
			if expected == 200 {
				if len(result.body["results"].([]any)) != 1 || result.body["semantic"].(map[string]any)["available"] != false {
					t.Fatal("unrelated curation or disabled provider discarded healthy lexical evidence")
				}
			} else if result.body["results"] != nil || result.body["error"].(map[string]any)["code"] != code {
				t.Fatalf("fallback stale evidence did not fail safely: %v", result.body)
			}
		})
	}
}
