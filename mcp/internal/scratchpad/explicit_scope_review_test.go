package scratchpad

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioExplicitScopeOnlyToolsBypassGit(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	run(t, checkout, "init")
	run(t, checkout, "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "commit", "--allow-empty", "-m", "initial")
	run(t, checkout, "remote", "add", "origin", "https://github.com/owner/scope-fixture.git")
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "git-invoked")
	script := fmt.Sprintf("#!/bin/sh\nprintf invoked >> %q\nexec %q \"$@\"\n", marker, realGit)
	if err = os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var implicit atomic.Bool
	var resolved atomic.Int32
	var captured atomic.Int32
	ctx, session := presenceStdioSession(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Method == "POST" && json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "invalid body", 400)
			return
		}
		if r.URL.Path == "/api/v1/projects/resolve" {
			resolved.Add(1)
			git := body["context"].(map[string]any)["git"].(map[string]any)
			if !implicit.Load() || git["repositoryIdentity"] != "github.com/owner/scope-fixture" {
				http.Error(w, "unexpected discovery", 400)
				return
			}
			fmt.Fprint(w, `{"project":{"id":"proj_discovered"}}`)
			return
		}
		project := "proj_explicit"
		if implicit.Load() {
			project = "proj_discovered"
		}
		if r.Method == "POST" && body["projectId"] != project {
			http.Error(w, "scope changed", 400)
			return
		}
		switch r.URL.Path {
		case "/api/v1/rag/retrieve":
			fmt.Fprint(w, `{"results":[{"recordId":"rec_evidence"}],"resultsOmitted":0}`)
		case "/api/v1/search/semantic":
			fmt.Fprint(w, `{"results":[]}`)
		case "/api/v1/suggestions":
			fmt.Fprint(w, `{"suggestions":[]}`)
		case "/api/v1/ai/jobs", "/api/v1/summaries/export":
			fmt.Fprint(w, `{"job":{"id":"job_synthetic"}}`)
		case "/api/v1/projects/proj_explicit/context":
			fmt.Fprint(w, `{"recentDecisions":[]}`)
		case "/api/v1/records":
			if r.Method == "GET" {
				fmt.Fprint(w, `{"records":[]}`)
				return
			}
			captured.Add(1)
			git, present := body["gitContext"]
			if implicit.Load() {
				if !present || git.(map[string]any)["repositoryIdentity"] != "github.com/owner/scope-fixture" {
					http.Error(w, "capture provenance missing", 400)
					return
				}
			} else if present {
				http.Error(w, "undiscovered provenance claimed", 400)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"record": body["record"], "mirror": map[string]any{"eligible": false}})
		default:
			http.Error(w, "unexpected route", 404)
		}
	})
	for _, tool := range []struct {
		name   string
		fields map[string]any
	}{
		{"retrieve_evidence", map[string]any{"query": "explicit evidence"}},
		{"semantic_search", map[string]any{"query": "explicit meaning"}},
		{"get_suggestions", nil},
		{"process_memory", map[string]any{"type": "analyze"}},
		{"generate_document", map[string]any{"format": "handoff"}},
		{"get_project_context", nil},
		{"search_memory", map[string]any{"query": "explicit"}},
		{"get_decision_history", nil},
	} {
		input := map[string]any{"projectId": "proj_explicit", "workingDirectory": filepath.Join(checkout, "unavailable")}
		for key, value := range tool.fields {
			input[key] = value
		}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool.name, Arguments: input})
		if err != nil || result.IsError {
			t.Errorf("explicit scope failed %s: %v %+v", tool.name, err, result)
		}
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Error("explicit project scope invoked Git")
	}
	if resolved.Load() != 0 || captured.Load() != 0 {
		t.Fatal("explicit scope unexpectedly resolved checkout or captured a record")
	}
	implicit.Store(true)
	for _, tool := range []struct {
		name  string
		input map[string]any
	}{
		{"retrieve_evidence", map[string]any{"workingDirectory": checkout, "query": "implicit evidence"}},
		{"record_decision", map[string]any{"workingDirectory": checkout, "requestId": "implicit_capture", "title": "Implicit decision", "authority": "explicit", "confidence": "high", "decision": "Retain discovered provenance."}},
	} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool.name, Arguments: tool.input})
		if err != nil || result.IsError {
			t.Fatalf("implicit discovery/provenance failed %s: %v %+v", tool.name, err, result)
		}
	}
	if _, err = os.Stat(marker); err != nil || resolved.Load() != 2 || captured.Load() != 1 {
		t.Fatal("implicit checkout resolution did not invoke Git and preserve capture")
	}
}
