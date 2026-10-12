package backend_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"
)

func ragInstance(t *testing.T) (string, string) {
	t.Helper()
	if os.Getenv("TEST_PGVECTOR_URL") == "" {
		t.Skip("TEST_PGVECTOR_URL is required for passage RAG")
	}
	t.Setenv("TEST_POSTGRES_URL", os.Getenv("TEST_PGVECTOR_URL"))
	t.Setenv("SCRATCHPAD_RAG_ENABLED", "true")
	path, databaseURL := portableInstance(t, "postgres")
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE EXTENSION vector`); err != nil {
		t.Fatal(err)
	}
	return path, databaseURL
}

func TestRAGHistoryLifecycleIsNotTruncatedWithRelationshipPreviews(t *testing.T) {
	path, databaseURL := ragInstance(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Input) != 1 {
			http.Error(w, "invalid fixture input", 400)
			return
		}
		vector := []int{1, 0}
		if strings.Contains(body.Input[0], "Related source") {
			vector = []int{0, 1}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": vector}}})
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("provider fixture failed")
	}
	projectID := jobProject(t, origin, "Bounded historical citations", true, false)
	record := curationCapture(t, origin, projectID, "Historical choice")
	status, response := curationRequest(t, origin, "POST", "/rag/index", map[string]any{"projectId": projectID, "recordId": record["id"]})
	if status != 202 {
		t.Fatal("enqueue failed")
	}
	waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "completed")
	for i := 0; i < 11; i++ {
		next := curationCapture(t, origin, projectID, fmt.Sprintf("Related source %d", i))
		typ := "related_to"
		if i == 10 {
			typ = "replaces"
		}
		status, _ = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": next["id"], "toRecordId": record["id"], "type": typ, "authority": "explicit"})
		if status != 201 {
			t.Fatalf("relationship fixture failed: %d", status)
		}
	}
	status, response = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "historical choice", "intent": "history"})
	if status != 200 || len(response["results"].([]any)) != 1 {
		t.Fatalf("historical evidence missing: %d %v", status, response)
	}
	item := response["results"].([]any)[0].(map[string]any)
	if item["applicability"] != "superseded" || len(item["relationships"].([]any)) > 10 {
		t.Fatalf("bounded relationship previews changed historical authority: %v", item)
	}
	status, response = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "historical choice"})
	if status != 200 || len(response["results"].([]any)) != 0 {
		t.Fatal("superseded evidence was presented as current guidance")
	}
}

func TestRAGResponseBudgetRetainsCitedUnicodeEvidenceWithOmissions(t *testing.T) {
	path, databaseURL := ragInstance(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Input) != 1 {
			http.Error(w, "invalid fixture input", 400)
			return
		}
		vector := []int{1, 0}
		if strings.Contains(body.Input[0], "Supporting source") {
			vector = []int{0, 1}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": vector}}})
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("provider fixture failed")
	}
	projectID := jobProject(t, origin, "Bounded Unicode evidence", true, false)
	status, response := captureRequest(t, origin, projectID, "", map[string]any{"type": "finding", "title": "Unicode evidence", "authority": "observed", "payload": map[string]any{"finding": strings.Repeat("文", 2000)}})
	if status != 201 {
		t.Fatal("source fixture failed")
	}
	record := response["record"].(map[string]any)
	status, response = curationRequest(t, origin, "POST", "/rag/index", map[string]any{"projectId": projectID, "recordId": record["id"]})
	if status != 202 {
		t.Fatal("indexing failed")
	}
	waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "completed")
	for i := 0; i < 10; i++ {
		other := curationCapture(t, origin, projectID, fmt.Sprintf("Supporting source %d", i))
		status, _ = curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": other["id"], "toRecordId": record["id"], "type": "related_to", "authority": "explicit", "note": strings.Repeat("synthetic context ", 20)})
		if status != 201 {
			t.Fatal("relationship fixture failed")
		}
	}
	status, response = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "Unicode evidence", "maxChars": 4096})
	if status != 200 || len(response["results"].([]any)) != 1 {
		t.Fatalf("response budget discarded all cited evidence: %d %v", status, response)
	}
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded)+1 > 4096 {
		t.Fatalf("serialized response exceeded its byte budget: %d", len(encoded)+1)
	}
	item := response["results"].([]any)[0].(map[string]any)
	if item["textTruncated"] != true || item["relationshipsOmitted"].(float64) < 1 || !strings.Contains(item["text"].(string), "文") {
		t.Fatal("bounded evidence lost text or omission reporting")
	}
	location := item["sourceLocation"].(map[string]any)
	units := utf16.Encode([]rune(record["content"].(string)))
	if string(utf16.Decode(units[int(location["start"].(float64)):int(location["end"].(float64))])) != item["text"] {
		t.Fatal("budget truncation invalidated the source citation")
	}
}

func TestRAGCompletionModelChangeDoesNotDiscardInFlightPassageEmbedding(t *testing.T) {
	path, databaseURL := ragInstance(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var first atomic.Bool
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if first.CompareAndSwap(false, true) {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer func() { unblock(); provider.Close() }()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("provider fixture failed")
	}
	projectID := jobProject(t, origin, "Independent embedding compatibility", true, false)
	record := curationCapture(t, origin, projectID, "Completion choices preserve embeddings")
	status, response := curationRequest(t, origin, "POST", "/rag/index", map[string]any{"projectId": projectID, "recordId": record["id"]})
	if status != 202 {
		t.Fatal("index enqueue failed")
	}
	jobID := response["job"].(map[string]any)["id"].(string)
	select {
	case <-started:
	case <-time.After(4 * time.Second):
		t.Fatal("indexing did not reach the provider")
	}
	status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "model": "replacement-completion-model"})
	if status != 200 {
		t.Fatal("provider wait blocked a completion model update")
	}
	unblock()
	waitAIJob(t, origin, jobID, "completed")
	status, response = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "completion independence"})
	if status != 200 || len(response["results"].([]any)) != 1 || calls.Load() != 2 {
		t.Fatal("completion change discarded or repeated the compatible embedding")
	}
}

func TestRAGRechecksConsentProviderEnablementModelAndTokenBeforeActivation(t *testing.T) {
	for _, change := range []struct {
		name, field    string
		value, restore any
	}{
		{"consent", "aiProcessing", false, true},
		{"provider-disabled", "enabled", false, true},
		{"embedding-model", "embeddingModel", "replacement-embedding-model", "test-embedding-model"},
		{"provider-token", "apiKey", "synthetic-provider-b", "synthetic-provider-a"},
	} {
		t.Run(change.name, func(t *testing.T) {
			path, databaseURL := ragInstance(t)
			t.Setenv("SCRATCHPAD_ENCRYPTION_KEY_FILE", syntheticEncryptionKey(t, 17))
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			var first atomic.Bool
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if first.CompareAndSwap(false, true) {
					close(started)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
			}))
			defer func() { unblock(); provider.Close() }()
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2, "embeddingModel": "test-embedding-model", "apiKey": "synthetic-provider-a"})
			if status != 200 {
				t.Fatal("provider fixture failed")
			}
			projectID := jobProject(t, origin, "Activation boundary", true, false)
			record := curationCapture(t, origin, projectID, "Revalidate before activating evidence")
			status, response := curationRequest(t, origin, "POST", "/rag/index", map[string]any{"projectId": projectID, "recordId": record["id"]})
			if status != 202 {
				t.Fatal("enqueue failed")
			}
			jobID := response["job"].(map[string]any)["id"].(string)
			select {
			case <-started:
			case <-time.After(4 * time.Second):
				t.Fatal("worker did not reach provider")
			}
			var projectSettings map[string]any
			if change.name == "consent" {
				_, project := projectRequest(t, origin, "GET", "/"+projectID, nil, restoredToken)
				projectSettings = project["project"].(map[string]any)["settings"].(map[string]any)
				projectSettings[change.field] = change.value
				status, _ = projectRequest(t, origin, "PATCH", "/"+projectID+"/settings", map[string]any{"expectedVersion": 2, "settings": projectSettings}, restoredToken)
			} else {
				status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, change.field: change.value})
			}
			if status != 200 {
				t.Fatal("provider wait blocked configuration or consent changes")
			}
			unblock()
			waitAIJob(t, origin, jobID, "failed")
			if change.name == "consent" {
				projectSettings[change.field] = change.restore
				status, _ = projectRequest(t, origin, "PATCH", "/"+projectID+"/settings", map[string]any{"expectedVersion": 3, "settings": projectSettings}, restoredToken)
			} else {
				status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 2, change.field: change.restore})
			}
			if status != 200 {
				t.Fatal("configuration recovery failed")
			}
			status, response = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "activation boundary"})
			if status != 200 || len(response["results"].([]any)) != 0 {
				t.Fatal("invalidated provider output became retrievable evidence")
			}
		})
	}
}

func TestRAGRejectsMalformedProjectionMarkerWithoutRepair(t *testing.T) {
	path, databaseURL := ragInstance(t)
	var record map[string]any
	t.Run("capture", func(t *testing.T) {
		origin := startServer(t, path, databaseURL)
		projectID := jobProject(t, origin, "Projection migration safety", false, false)
		record = curationCapture(t, origin, projectID, "Retain source through failed startup")
	})
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`UPDATE rag_projection_migrations SET applied_at='malformed'`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "serve")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL="+databaseURL, "SCRATCHPAD_DATABASE_PATH="+path, "SCRATCHPAD_LISTEN_ADDR=127.0.0.1:0", "SCRATCHPAD_PUBLIC_URL=http://localhost:3000")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
		t.Fatalf("malformed projection marker was not refused safely: %v %s", err, out)
	}
	t.Setenv("SCRATCHPAD_RAG_ENABLED", "false")
	origin := startServer(t, path, databaseURL)
	status, response := curationRequest(t, origin, "GET", "/records/"+record["id"].(string), nil)
	if status != 200 || !reflect.DeepEqual(response["record"], record) {
		t.Fatal("failed optional projection migration changed source history")
	}
}

func TestRAGPassagesSurviveRestartAndExplicitRebuildWithoutRewritingHistory(t *testing.T) {
	path, databaseURL := ragInstance(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer provider.Close()
	var record, completed map[string]any
	var projectID, passageID string
	for _, phase := range []string{"index", "restart-and-rebuild"} {
		t.Run(phase, func(t *testing.T) {
			origin := startServer(t, path, databaseURL)
			if phase == "index" {
				status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
				if status != 200 {
					t.Fatal("provider fixture failed")
				}
				projectID = jobProject(t, origin, "Recoverable passages", true, false)
				record = curationCapture(t, origin, projectID, "Raw history remains authoritative")
				status, response := curationRequest(t, origin, "POST", "/rag/index", map[string]any{"projectId": projectID, "recordId": record["id"]})
				if status != 202 {
					t.Fatal("indexing failed")
				}
				completed = waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "completed")
			}
			status, response := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "raw history"})
			if status != 200 || len(response["results"].([]any)) != 1 {
				t.Fatalf("passage missing after %s: %d %v", phase, status, response)
			}
			id := response["results"].([]any)[0].(map[string]any)["id"].(string)
			if phase == "index" {
				passageID = id
				return
			}
			if id != passageID {
				t.Fatal("restart changed source citation identity")
			}
			db, err := sql.Open("pgx", databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.Exec(`DELETE FROM rag_passages`); err != nil {
				t.Fatal(err)
			}
			status, response = curationRequest(t, origin, "POST", "/rag/index", map[string]any{"projectId": projectID, "recordId": record["id"], "rebuild": true, "expectedVersion": completed["version"]})
			if status != 202 || response["job"].(map[string]any)["status"] != "queued" {
				t.Fatalf("explicit rebuild did not requeue the projection: %d %v", status, response)
			}
			job := response["job"].(map[string]any)
			waitAIJob(t, origin, job["id"].(string), "completed")
			status, response = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "raw history"})
			if status != 200 || len(response["results"].([]any)) != 1 || response["results"].([]any)[0].(map[string]any)["id"] != passageID {
				t.Fatal("rebuild did not restore the same cited source")
			}
			status, response = curationRequest(t, origin, "GET", "/records/"+record["id"].(string), nil)
			if status != 200 || !reflect.DeepEqual(response["record"], record) {
				t.Fatal("projection rebuild rewrote immutable history")
			}
		})
	}
}

func TestRAGRetrievesSeparateCitedPassagesAcrossCompletionModelChange(t *testing.T) {
	path, databaseURL := ragInstance(t)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		if r.URL.Path != "/v1/embeddings" || json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "unexpected provider call", 400)
			return
		}
		calls.Add(int32(len(body.Input)))
		data := []any{}
		for index, input := range body.Input {
			vector := []int{0, 1}
			if strings.Contains(input, "TARGET_EVIDENCE") {
				vector = []int{1, 0}
			}
			data = append(data, map[string]any{"index": index, "embedding": vector})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer provider.Close()
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("provider fixture failed")
	}
	projectID := jobProject(t, origin, "Passage evidence", true, false)
	status, response := captureRequest(t, origin, projectID, "", map[string]any{"type": "finding", "title": "Long source", "authority": "observed", "payload": map[string]any{"finding": strings.Repeat("x", 6200) + " TARGET_EVIDENCE keeps its own embedding 🦊"}})
	if status != 201 {
		t.Fatal("source fixture failed")
	}
	record := response["record"].(map[string]any)
	status, response = curationRequest(t, origin, "POST", "/rag/index", map[string]any{"projectId": projectID, "recordId": record["id"]})
	if status != 202 {
		t.Fatalf("passage indexing failed: %d %v", status, response)
	}
	job := waitAIJob(t, origin, response["job"].(map[string]any)["id"].(string), "completed")
	if job["passageCount"] != float64(2) {
		t.Fatalf("long source must retain two passages: %v", job)
	}
	var passageID any
	for _, phase := range []string{"initial", "completion-model-change"} {
		if phase != "initial" {
			status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "model": "different-completion-model"})
			if status != 200 {
				t.Fatal("completion model change failed")
			}
		}
		status, response = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": projectID, "query": "TARGET_EVIDENCE", "limit": 1})
		if status != 200 {
			t.Fatalf("passage retrieval failed: %d %v", status, response)
		}
		results := response["results"].([]any)
		if len(results) != 1 {
			t.Fatalf("target passage missing: %v", response)
		}
		passage := results[0].(map[string]any)
		if passage["recordId"] != record["id"] || passage["projectId"] != projectID || passage["score"] != float64(1) || !strings.Contains(passage["text"].(string), "TARGET_EVIDENCE") || passage["authority"] != "observed" {
			t.Fatalf("passage evidence was diluted or lost its provenance: %v", passage)
		}
		location := passage["sourceLocation"].(map[string]any)
		start, end := int(location["start"].(float64)), int(location["end"].(float64))
		units := utf16.Encode([]rune(record["content"].(string)))
		if location["field"] != "content" || location["unit"] != "utf16" || start != 6000 || end > len(units) || string(utf16.Decode(units[start:end])) != passage["text"] || passage["sourceUrl"] != "/api/v1/records/"+record["id"].(string) {
			t.Fatalf("citation does not identify the immutable source excerpt: %v", passage)
		}
		if phase == "initial" {
			passageID = passage["id"]
		} else if passage["id"] != passageID {
			t.Fatal("completion model changed passage identity")
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("completion model change re-embedded indexed sources: %d inputs", calls.Load())
	}
}
