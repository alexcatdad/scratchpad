package scratchpad

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestScopedToolsDoNotExecuteCheckoutHelpers(t *testing.T) {
	root, source := t.TempDir(), t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, source} {
		run(t, dir, "init")
		run(t, dir, "-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "--allow-empty", "-m", "synthetic")
	}
	run(t, root, "-c", "protocol.file.allow=always", "submodule", "add", source, "child")
	run(t, root, "-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "-am", "synthetic submodule")
	run(t, root, "remote", "add", "origin", "https://github.com/synthetic/prepared.git")
	worktree := filepath.Join(t.TempDir(), "worktree")
	run(t, root, "worktree", "add", "-b", "synthetic-feature", worktree)
	if err := os.WriteFile(filepath.Join(root, "synthetic-untracked"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	markers := []string{}
	for _, dir := range []string{root, filepath.Join(root, "child")} {
		marker := filepath.Join(t.TempDir(), "executed")
		hook := filepath.Join(t.TempDir(), "fsmonitor")
		if err := os.WriteFile(hook, []byte(fmt.Sprintf("#!/bin/sh\nprintf synthetic > %q\n", marker)), 0700); err != nil {
			t.Fatal(err)
		}
		run(t, dir, "config", "core.fsmonitor", hook)
		markers = append(markers, marker)
	}
	run(t, root, "config", "submodule.recurse", "true")
	run(t, root, "config", "status.submoduleSummary", "true")
	// Positive controls prove the harmless hooks are live before testing MCP.
	for i, dir := range []string{root, filepath.Join(root, "child")} {
		run(t, dir, "status", "--porcelain")
		if _, err := os.Stat(markers[i]); err != nil {
			t.Fatal("marker hook did not execute in positive control", err)
		}
		if err := os.Remove(markers[i]); err != nil {
			t.Fatal(err)
		}
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/projects/resolve" {
			fmt.Fprint(w, `{"project":{"id":"synthetic-project"}}`)
			return
		}
		if r.URL.Path == "/api/v1/records" && r.Method == "POST" {
			var body struct {
				GitContext GitContext `json:"gitContext"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.GitContext.RootPathHint != canonicalRoot || body.GitContext.Commit == "" || !body.GitContext.Dirty || body.GitContext.RepositoryIdentity != "github.com/synthetic/prepared" {
				t.Errorf("lost provenance: %+v", body.GitContext)
			}
			fmt.Fprint(w, `{"record":{"id":"synthetic-record"},"mirror":{"eligible":false}}`)
			return
		}
		fmt.Fprint(w, `{"records":[],"artifacts":[],"job":{"id":"synthetic-job"}}`)
	}))
	defer api.Close()
	c, _ := NewClient(Config{URL: api.URL, WorkingDirectory: root})
	c.token = "synthetic-token"
	c.expires = time.Now().Add(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	server, err := NewServer(c).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "synthetic", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, tool := range []struct {
		name string
		args map[string]any
	}{
		{"get_project_context", map[string]any{}},
		{"search_memory", map[string]any{"query": "synthetic", "workingDirectory": root}},
		{"record_project_state", map[string]any{"projectId": "synthetic-project", "requestId": "synthetic-capture", "title": "Synthetic", "authority": "observed", "confidence": "high", "state": "Synthetic"}},
		{"get_decision_history", map[string]any{}},
		{"semantic_search", map[string]any{"query": "synthetic"}},
		{"get_suggestions", map[string]any{}},
		{"process_memory", map[string]any{"type": "analyze"}},
		{"generate_document", map[string]any{"format": "handoff"}},
	} {
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: tool.name, Arguments: tool.args})
		if err != nil || result.IsError {
			t.Fatalf("%s: %+v %v", tool.name, result, err)
		}
		for _, marker := range markers {
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("%s executed checkout helper: %v", tool.name, err)
			}
		}
	}
	g, err := Discover(ctx, worktree)
	if err != nil || g == nil || !g.Worktree.Detected || g.Branch != "synthetic-feature" || g.Commit == "" {
		t.Fatalf("worktree provenance: %+v %v", g, err)
	}
	for _, marker := range markers {
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("worktree executed helper", err)
		}
	}
}

func TestDiscoveryGitVersionBoundary(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"2.35.1", "2.36.0", "2.56.0 (vendor build)", "unrecognized"} {
		t.Run(version, func(t *testing.T) {
			dir := t.TempDir()
			run(t, dir, "init")
			bin := t.TempDir()
			marker := filepath.Join(bin, "inspected")
			script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = --version ]; then printf 'git version %%s\\n' %q; exit 0; fi\nprintf synthetic > %q\nexec %q \"$@\"\n", version, marker, realGit)
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			g, err := Discover(context.Background(), dir)
			unsupported := strings.HasPrefix(version, "2.35") || version == "unrecognized"
			if unsupported {
				if err == nil || !strings.Contains(err.Error(), "Git 2.36") {
					t.Fatalf("unsafe version accepted: %+v %v", g, err)
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatal("unsupported Git inspected checkout", err)
				}
			} else if err != nil || g == nil {
				t.Fatalf("supported version rejected: %+v %v", g, err)
			}
		})
	}
}
