package scratchpad

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This boundary fixture observes real MCP protocol calls, local Git/filesystem
// behavior, and HTTP payloads. API policy/persistence is tested by the TypeScript
// tests and the production-server browser + stdio integration workflow.
type acceptanceFixture struct {
	mu          sync.Mutex
	requests    []string
	captures    []map[string]any
	records     map[string]map[string]any
	retries     map[string]string
	reports     int
	failCapture bool
}

func acceptanceSession(t *testing.T, dir string, mirror bool) (*mcp.ClientSession, *acceptanceFixture) {
	t.Helper()
	f := &acceptanceFixture{records: map[string]map[string]any{}, retries: map[string]string{}}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		if r.Body != nil && r.Method != "GET" {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
		}
		switch {
		case r.URL.Path == "/api/v1/projects/resolve":
			git := body["context"].(map[string]any)["git"].(map[string]any)
			if git["repositoryIdentity"] != "github.com/owner/project" {
				t.Errorf("unexpected identity: %v", git)
			}
			fmt.Fprint(w, `{"project":{"id":"git-project","name":"Project"}}`)
		case r.URL.Path == "/api/v1/projects/resolve-explicit":
			if body["name"] != "Owner confirmed project" {
				t.Error(body)
			}
			fmt.Fprint(w, `{"project":{"id":"explicit-project","name":"Owner confirmed project"}}`)
		case r.URL.Path == "/api/v1/records" && r.Method == "POST":
			if f.failCapture {
				w.WriteHeader(503)
				fmt.Fprint(w, `{"error":{"code":"UNAVAILABLE","message":"central write unavailable"}}`)
				return
			}
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				t.Error("missing retry identity")
			}
			f.captures = append(f.captures, body)
			id := f.retries[key]
			if id == "" {
				id = fmt.Sprintf("record-%d", len(f.records)+1)
				record := body["record"].(map[string]any)
				record["id"] = id
				record["projectId"] = body["projectId"]
				if g, ok := body["gitContext"]; ok {
					record["gitContext"] = g
				}
				f.records[id] = record
				f.retries[key] = id
			}
			record := f.records[id]
			kind := record["type"]
			eligible := kind == "decision" || kind == "adr" || kind == "business_decision"
			if err := json.NewEncoder(w).Encode(map[string]any{"record": record, "mirror": map[string]bool{"eligible": eligible}}); err != nil {
				t.Error(err)
			}
		case strings.HasSuffix(r.URL.Path, "/mirror"):
			f.reports++
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/records/"), "/mirror")
			if f.records[id] == nil {
				t.Error("mirror reported before central record existed")
			}
			fmt.Fprint(w, `{}`)
		case strings.HasPrefix(r.URL.Path, "/api/v1/records/") && r.Method == "GET":
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/records/")
			if err := json.NewEncoder(w).Encode(map[string]any{"record": f.records[id]}); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected API call: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(api.Close)
	c, err := NewClient(Config{URL: api.URL, WorkingDirectory: dir, Mirror: mirror})
	if err != nil {
		t.Fatal(err)
	}
	c.token = "fixture-session"
	c.expires = time.Now().Add(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	server, err := NewServer(c).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	client, err := mcp.NewClient(&mcp.Implementation{Name: "acceptance", Version: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, f
}
func acceptanceCall(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	result, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("%s failed: %+v", name, result)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal(data, &out); err != nil {
		t.Fatalf("%s %v", data, err)
	}
	return out
}
func acceptanceFailure(t *testing.T, s *mcp.ClientSession, name string, args map[string]any, code string) {
	t.Helper()
	result, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	if !result.IsError || !strings.Contains(string(data), code) {
		t.Fatalf("expected %s: %s", code, data)
	}
}
func decisionArgs(key string) map[string]any {
	return map[string]any{"requestId": key, "title": "Keep context", "decision": "Preserve evidence", "authority": "explicit", "confidence": "high"}
}
func initializedRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-b", "main")
	run(t, dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "initial")
	run(t, dir, "remote", "add", "origin", "git@github.com:owner/project.git")
	return dir
}
func snapshotFiles(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = sha256.Sum256(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestAcceptanceNonGitExplicitResolutionAndRetry(t *testing.T) {
	dir := t.TempDir()
	before := snapshotFiles(t, dir)
	session, f := acceptanceSession(t, dir, false)
	args := decisionArgs("non-git")
	acceptanceFailure(t, session, "record_decision", args, "PROJECT_IDENTITY_REQUIRED")
	if len(f.requests) != 0 {
		t.Fatalf("weak identity reached API: %v", f.requests)
	}
	resolved := acceptanceCall(t, session, "resolve_project", map[string]any{"name": "Owner confirmed project"})
	args["projectId"] = resolved["project"].(map[string]any)["id"]
	created := acceptanceCall(t, session, "record_decision", args)
	retried := acceptanceCall(t, session, "record_decision", args)
	record := created["record"].(map[string]any)
	if record["id"] != retried["record"].(map[string]any)["id"] || record["projectId"] != "explicit-project" {
		t.Fatal("explicit retry did not preserve identity")
	}
	if _, ok := record["gitContext"]; ok {
		t.Fatal("invented Git context for non-Git project")
	}
	acceptanceCall(t, session, "get_record", map[string]any{"id": record["id"]})
	if !reflect.DeepEqual(before, snapshotFiles(t, dir)) {
		t.Fatal("non-Git capture modified project folder")
	}
}
func TestAcceptanceAmbiguityExplicitResolutionPreservesProvenance(t *testing.T) {
	dir := initializedRepository(t)
	run(t, dir, "remote", "rename", "origin", "upstream")
	run(t, dir, "remote", "add", "fork", "https://github.com/other/project.git")
	commit, _ := git(context.Background(), dir, "rev-parse", "HEAD")
	session, f := acceptanceSession(t, dir, false)
	args := decisionArgs("ambiguous")
	acceptanceFailure(t, session, "record_decision", args, "PROJECT_IDENTITY_AMBIGUOUS")
	if len(f.requests) != 0 {
		t.Fatal("ambiguous project guessed before owner choice")
	}
	resolved := acceptanceCall(t, session, "resolve_project", map[string]any{"name": "Owner confirmed project"})
	args["projectId"] = resolved["project"].(map[string]any)["id"]
	out := acceptanceCall(t, session, "record_decision", args)
	g := out["record"].(map[string]any)["gitContext"].(map[string]any)
	if g["branch"] != "main" || g["commit"] != commit {
		t.Fatalf("explicit selection lost checkout provenance: %v", g)
	}
	if g["repositoryIdentity"] != nil || g["remote"] != nil {
		t.Fatal("invented a remote identity after ambiguity")
	}
}
func TestAcceptanceWorktreesShareProjectAndKeepIndependentContext(t *testing.T) {
	dir := initializedRepository(t)
	wt := filepath.Join(t.TempDir(), "feature-checkout")
	run(t, dir, "worktree", "add", "-b", "feature", wt)
	run(t, wt, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "feature")
	mainCommit, _ := git(context.Background(), dir, "rev-parse", "HEAD")
	featureCommit, _ := git(context.Background(), wt, "rev-parse", "HEAD")
	session, _ := acceptanceSession(t, dir, false)
	main := acceptanceCall(t, session, "record_decision", decisionArgs("main"))
	args := decisionArgs("feature")
	args["workingDirectory"] = wt
	feature := acceptanceCall(t, session, "record_decision", args)
	again := acceptanceCall(t, session, "record_decision", decisionArgs("main-again"))
	for _, out := range []map[string]any{main, feature, again} {
		if out["project"].(map[string]any)["id"] != "git-project" {
			t.Fatal("worktree created a different project")
		}
	}
	for i, item := range []struct {
		out            map[string]any
		branch, commit string
		worktree       bool
	}{{main, "main", mainCommit, false}, {feature, "feature", featureCommit, true}, {again, "main", mainCommit, false}} {
		g := item.out["record"].(map[string]any)["gitContext"].(map[string]any)
		if g["branch"] != item.branch || g["commit"] != item.commit || g["worktree"].(map[string]any)["detected"] != item.worktree {
			t.Fatalf("checkout %d wrong provenance: %v", i, g)
		}
	}
}
func TestAcceptanceContractorRepositoryUntouched(t *testing.T) {
	dir := initializedRepository(t)
	if err := os.WriteFile(filepath.Join(dir, "client.txt"), []byte("private client content"), 0600); err != nil {
		t.Fatal(err)
	}
	before := snapshotFiles(t, dir)
	session, f := acceptanceSession(t, dir, false)
	acceptanceCall(t, session, "record_decision", decisionArgs("contractor"))
	after := snapshotFiles(t, dir)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("capture modified contractor working tree or Git metadata")
	}
	if len(f.records) != 1 || f.reports != 0 {
		t.Fatal("contractor capture did not remain central only")
	}
}
func TestAcceptanceMirrorDefaultTypesRetryAndCentralFailure(t *testing.T) {
	dir := initializedRepository(t)
	session, f := acceptanceSession(t, dir, true)
	fields := []struct{ kind, field, value string }{{"decision", "decision", "Keep evidence"}, {"adr", "decision", "Keep evidence"}, {"business_decision", "decision", "Keep evidence"}, {"finding", "finding", "Observed"}, {"qa", "question", "Why"}, {"failure", "observed", "Failed"}, {"constraint", "constraint", "English"}, {"project_state", "state", "active"}}
	for _, item := range fields {
		args := map[string]any{"requestId": item.kind, "title": "Capture " + item.kind, "authority": "observed", "confidence": "high", item.field: item.value}
		if item.kind == "qa" {
			args["answer"] = "Because"
		}
		acceptanceCall(t, session, "record_"+item.kind, args)
		acceptanceCall(t, session, "record_"+item.kind, args)
	}
	path := filepath.Join(dir, "scratchpad/decisions.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 || len(f.records) != 8 || f.reports != 6 {
		t.Fatalf("wrong default mirror/retry behavior: lines=%d records=%d reports=%d", len(lines), len(f.records), f.reports)
	}
	for _, line := range lines {
		var record map[string]any
		if err = json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		kind := record["type"]
		if kind != "decision" && kind != "adr" && kind != "business_decision" {
			t.Fatalf("ineligible default type mirrored: %v", kind)
		}
	}
	f.mu.Lock()
	f.failCapture = true
	f.mu.Unlock()
	acceptanceFailure(t, session, "record_decision", decisionArgs("central-failure"), "UNAVAILABLE")
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(data) || f.reports != 6 {
		t.Fatal("mirror mutated after failed central write")
	}
}

func TestReleaseVersionInProtocol(t *testing.T) {
	c, err := NewClient(Config{URL: "http://localhost:1", WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	server, err := NewServerWithVersion(c, "9.8.7-test").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "release-check", Version: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if got := client.InitializeResult().ServerInfo.Version; got != "9.8.7-test" {
		t.Fatalf("protocol advertised %q", got)
	}
}

func TestSearchProtocolForwardsDeterministicFilters(t *testing.T) {
	expected := map[string]string{"projectId": "selected-project", "q": "historical choice", "type": "decision", "tag": "storage", "branch": "feature/storage", "from": "2026-01-01T00:00:00Z", "to": "2026-09-29T23:59:59Z", "status": "active", "relationship": "supports", "relatedTo": "record-existing", "source": "Owner & agent", "gitPath": "/workspace/project folder", "authority": "explicit", "confidence": "high", "cursor": "opaque-cursor", "limit": "17"}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/records" || r.Method != "GET" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		for key, want := range expected {
			if got := r.URL.Query().Get(key); got != want {
				t.Errorf("filter %s=%q; want %q", key, got, want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"records":[],"nextCursor":null}`)
	}))
	defer api.Close()
	c, err := NewClient(Config{URL: api.URL, WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	c.token = "test"
	c.expires = time.Now().Add(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	server, err := NewServer(c).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "filter-test", Version: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	args := map[string]any{}
	for key, value := range expected {
		args[key] = value
	}
	delete(args, "q")
	args["query"] = "historical choice"
	args["limit"] = 17
	acceptanceCall(t, client, "search_memory", args)
}
