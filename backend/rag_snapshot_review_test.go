package backend_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRAGSuccessfulReadKeepsIndependentCaptureAndCurationResponsive(t *testing.T) {
	path, databaseURL := ragInstance(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("settings failed")
	}
	project := jobProject(t, origin, "Snapshot RAG participant", true, false)
	ordinary := jobProject(t, origin, "Ordinary independent memory", false, false)
	source := curationCapture(t, origin, project, "RAG_SNAPSHOT_EVIDENCE")
	for _, job := range waitRAGJobs(t, origin, 1) {
		waitAIJob(t, origin, job.(map[string]any)["id"].(string), "completed")
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
	// Optional passage reads wait; entities/context_headers remain writable.
	if _, err = barrier.ExecContext(ctx, "LOCK TABLE rag_passages IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	type response struct {
		status int
		body   map[string]any
	}
	done := make(chan response, 1)
	go func() {
		status, body := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": project, "query": "RAG_SNAPSHOT_EVIDENCE"})
		done <- response{status, body}
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
			t.Fatal("successful RAG query did not reach passage coverage barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	write := make(chan bool, 1)
	go func() {
		status, _ := curationRequest(t, origin, "PATCH", "/records/"+source["id"].(string)+"/metadata", map[string]any{"expectedVersion": 1, "tags": []any{"responsive"}})
		if status != 200 {
			write <- false
			return
		}
		status, _ = captureRequest(t, origin, ordinary, "", map[string]any{"type": "finding", "title": "Independent memory capture", "authority": "observed", "payload": map[string]any{"finding": "Capture remains available during optional retrieval."}})
		write <- status == 201
	}()
	responsive, blocked := false, false
	select {
	case responsive = <-write:
	case <-time.After(3 * time.Second):
		blocked = true
	}
	if err = barrier.Rollback(); err != nil {
		t.Fatal(err)
	}
	if blocked {
		responsive = <-write
	}
	result := <-done
	if blocked {
		t.Fatal("paused read-only RAG work blocked independent memory writes")
	}
	if !responsive {
		t.Fatal("independent capture/curation failed")
	}
	if result.status != 200 || result.body["semantic"].(map[string]any)["available"] != true || len(result.body["results"].([]any)) == 0 {
		t.Fatalf("successful RAG read lost evidence after unrelated edits: HTTP%d %v", result.status, result.body)
	}
	for _, item := range result.body["results"].([]any) {
		if item.(map[string]any)["recordId"] != source["id"] {
			t.Fatal("independent project capture leaked into scoped evidence")
		}
	}
}

func TestRAGSnapshotRejectsEvidenceChangedBeforeResponse(t *testing.T) {
	for _, mode := range []string{"archived", "source", "citation", "unscoped_consent", "endpoint_consent", "generation", "key", "embedding_model"} {
		t.Run(mode, func(t *testing.T) {
			path, databaseURL := ragInstance(t)
			t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", syntheticEncryptionKey(t, 31))
			type dispatch struct{ model, authorization string }
			queries := make(chan dispatch, 4)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input struct {
					Input []string `json:"input"`
					Model string   `json:"model"`
				}
				if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Input) != 1 {
					http.Error(w, "invalid synthetic input", 400)
					return
				}
				vector := "[1,0]"
				if strings.Contains(input.Input[0], "B_ENDPOINT_BLOCK") {
					vector = "[0,1]"
				}
				if input.Input[0] == "RAG_SNAPSHOT_EVIDENCE" {
					queries <- dispatch{input.Model, r.Header.Get("Authorization")}
				}
				w.Write([]byte(`{"data":[{"index":0,"embedding":` + vector + `}]}`))
			}))
			defer provider.Close()
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2, "embeddingModel": "snapshot-embedding-model"})
			if status != 200 {
				t.Fatal("settings failed")
			}
			project := jobProject(t, origin, "Fresh RAG evidence", true, true)
			source := curationCapture(t, origin, project, "RAG_SNAPSHOT_EVIDENCE")
			other := ""
			var related map[string]any
			count := 1
			if mode == "citation" || mode == "endpoint_consent" || mode == "unscoped_consent" {
				other = jobProject(t, origin, "Fresh RAG endpoint", true, true)
				record := curationCapture(t, origin, other, "B_ENDPOINT_BLOCK")
				status, response := curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": source["id"], "toRecordId": record["id"], "type": "supports", "authority": "explicit", "note": "Selected scoped citation."})
				if status != 201 {
					t.Fatal("relationship failed")
				}
				related = response["relationship"].(map[string]any)
				count = 2
			}
			for _, job := range waitRAGJobs(t, origin, count) {
				waitAIJob(t, origin, job.(map[string]any)["id"].(string), "completed")
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
			if _, err = barrier.ExecContext(ctx, "LOCK TABLE rag_passages IN ACCESS EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}
			type response struct {
				status int
				body   map[string]any
			}
			done := make(chan response, 1)
			body := map[string]any{"projectId": project, "query": "RAG_SNAPSHOT_EVIDENCE"}
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
					t.Fatal("RAG query did not reach snapshot barrier")
				}
				time.Sleep(10 * time.Millisecond)
			}
			expected := 503
			switch mode {
			case "archived":
				status, _ = curationRequest(t, origin, "PATCH", "/records/"+source["id"].(string)+"/metadata", map[string]any{"expectedVersion": 1, "archived": true})
			case "citation":
				status, _ = curationRequest(t, origin, "POST", "/relationships/"+related["id"].(string)+"/reject", map[string]any{"expectedVersion": related["version"]})
			case "source":
				// Controlled historical corruption is input, never an application
				// mutation of immutable captures. A fresh hash must reject it.
				_, err = db.ExecContext(ctx, `UPDATE entities SET data=jsonb_set(data,'{content}','"Changed synthetic source during retrieval."') WHERE kind='record' AND id=$1`, source["id"])
				if err != nil {
					t.Fatal(err)
				}
				status = 200
			case "generation":
				status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "model": "new-completion-only-generation"})
				expected = 200
			case "key":
				status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "apiKey": "synthetic-key-after-snapshot"})
			case "embedding_model":
				status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "embeddingModel": "new-snapshot-embedding-model"})
			default:
				withdrawn := project
				if mode == "endpoint_consent" {
					withdrawn = other
				}
				code, response := projectRequest(t, origin, "GET", "/"+withdrawn, nil, restoredToken)
				if code != 200 {
					t.Fatal("project lookup failed")
				}
				current := response["project"].(map[string]any)
				settings := current["settings"].(map[string]any)
				settings["aiProcessing"] = false
				status, _ = projectRequest(t, origin, "PATCH", "/"+withdrawn+"/settings", map[string]any{"expectedVersion": current["version"], "settings": settings}, restoredToken)
				expected = 403
			}
			if status != 200 {
				t.Fatalf("snapshot blocked fixture change: HTTP%d", status)
			}
			if err = barrier.Rollback(); err != nil {
				t.Fatal(err)
			}
			result := <-done
			if result.status != expected {
				t.Fatalf("changed snapshot evidence escaped final fence: mode%s HTTP%d %v", mode, result.status, result.body)
			}
			if expected == 200 {
				if result.body["semantic"].(map[string]any)["available"] != true || len(result.body["results"].([]any)) == 0 {
					t.Fatal("completion-only generation invalidated admitted compatible evidence")
				}
			} else {
				code := "AI_CONFIGURATION_CHANGED"
				if expected == 403 {
					code = "AI_NOT_ALLOWED"
				}
				if result.body["results"] != nil || result.body["error"].(map[string]any)["code"] != code {
					t.Fatal("changed evidence did not fail with established safe error")
				}
			}
			if mode == "key" || mode == "embedding_model" || mode == "generation" {
				<-queries // The already-admitted request predates this settings edit.
				status, _ = curationRequest(t, origin, "POST", "/rag/retrieve", body)
				if status != 200 {
					t.Fatal("fresh retrieval could not use updated configuration or safe fallback")
				}
				select {
				case current := <-queries:
					model, authorization := "snapshot-embedding-model", ""
					if mode == "embedding_model" {
						model = "new-snapshot-embedding-model"
					}
					if mode == "key" {
						authorization = "Bearer synthetic-key-after-snapshot"
					}
					if current.model != model || current.authorization != authorization {
						t.Fatal("later provider request retained old dispatch configuration")
					}
				default:
					t.Fatal("fresh provider query was not dispatched")
				}
			}
		})
	}
}
