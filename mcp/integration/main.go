// Command integration exercises an independently running Scratchpad HTTP server
// through the real MCP executable's stdio transport. It never bypasses auth.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type capture struct {
	Tool       string         `json:"tool"`
	Arguments  map[string]any `json:"arguments"`
	RecordID   string         `json:"recordId"`
	ProjectID  string         `json:"projectId"`
	GitContext map[string]any `json:"gitContext,omitempty"`
}
type state struct {
	Captures         []capture `json:"captures"`
	ProjectScenarios []capture `json:"projectScenarios,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	serverURL := flag.String("url", "http://localhost:3000", "Running Scratchpad HTTP server")
	publicKey := flag.String("public-key", "", "Enrolled SSH public key path")
	signingKey := flag.String("signing-key", "", "Signing private key path (or use SSH agent)")
	workspace := flag.String("workspace", "", "Existing disposable Git repository with one remote")
	binary := flag.String("binary", "", "Built scratchpad-mcp executable")
	statePath := flag.String("state", "", "Capture-state file retained across server restart")
	phase := flag.String("phase", "capture", "capture, projects, or verify after server restart")
	restrictedProject := flag.String("restricted-project", "", "Disposable external project ID for projects phase")
	mirror := flag.Bool("mirror", false, "Require successful central and local mirroring for decisions (enable server project setting first)")
	flag.Parse()
	if *publicKey == "" || *workspace == "" || *binary == "" || *statePath == "" {
		return errors.New("public-key, workspace, binary, and state flags are required")
	}
	if *phase != "capture" && *phase != "projects" && *phase != "verify" {
		return errors.New("phase must be capture, projects, or verify")
	}
	absWorkspace, err := filepath.Abs(*workspace)
	if err != nil {
		return err
	}
	absBinary, err := filepath.Abs(*binary)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, absBinary)
	command.Dir = absWorkspace
	command.Stderr = os.Stderr
	command.Env = append(os.Environ(), "SCRATCHPAD_URL="+*serverURL, "SCRATCHPAD_PUBLIC_KEY="+*publicKey, "SCRATCHPAD_SIGNING_KEY="+*signingKey, fmt.Sprintf("SCRATCHPAD_MIRROR=%t", *mirror || *phase == "projects"))
	session, err := mcp.NewClient(&mcp.Implementation{Name: "scratchpad-integration", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		return fmt.Errorf("initialize real stdio MCP: %w", err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return err
	}
	if len(listed.Tools) != 19 {
		return fmt.Errorf("expected 19 tools, got %d", len(listed.Tools))
	}
	var saved state
	if *phase == "capture" {
		nonce := make([]byte, 12)
		if _, err = rand.Read(nonce); err != nil {
			return err
		}
		prefix := "integration-" + hex.EncodeToString(nonce)
		kinds := []struct {
			kind    string
			payload map[string]any
		}{
			{"decision", map[string]any{"decision": "Keep private project memory", "rationale": "Reproducible integration"}},
			{"adr", map[string]any{"decision": "Use SQLite", "context": "Disposable integration instance"}},
			{"business_decision", map[string]any{"decision": "Self hosted", "requestedBy": "integration"}},
			{"finding", map[string]any{"finding": "Cross-process capture works", "limitations": []string{"Hardware validation remains unresolved"}}},
			{"qa", map[string]any{"question": "Does real stdio reach persistence?", "answer": "Verified by rereading the record"}},
			{"failure", map[string]any{"observed": "Example captured failure", "lesson": "Preserve failure context"}},
			{"constraint", map[string]any{"constraint": "English only"}},
			{"project_state", map[string]any{"state": "paused", "reason": "Awaiting a disposable test device", "previousState": "active", "followUp": "Validate the remaining hardware behavior before resuming"}},
		}
		for _, kind := range kinds {
			args := kind.payload
			args["title"] = "Integration " + kind.kind
			args["requestId"] = prefix + "-" + kind.kind
			args["authority"] = "observed"
			args["confidence"] = "high"
			out, err := call(ctx, session, "record_"+kind.kind, args)
			if err != nil {
				return err
			}
			record, ok := out["record"].(map[string]any)
			if !ok {
				return fmt.Errorf("missing record in %v", out)
			}
			project, ok := out["project"].(map[string]any)
			if !ok {
				return fmt.Errorf("missing resolved project in %v", out)
			}
			id, _ := record["id"].(string)
			pid, _ := project["id"].(string)
			if id == "" || pid == "" {
				return errors.New("missing record/project ID")
			}
			if *mirror && (kind.kind == "decision" || kind.kind == "adr" || kind.kind == "business_decision") {
				status, _ := out["mirror"].(map[string]any)
				if status["succeeded"] != true {
					return fmt.Errorf("mirror did not succeed: %v", status)
				}
			}
			saved.Captures = append(saved.Captures, capture{Tool: "record_" + kind.kind, Arguments: args, RecordID: id, ProjectID: pid})
		}
		data, err := json.MarshalIndent(saved, "", "  ")
		if err != nil {
			return err
		}
		if err = os.WriteFile(*statePath, append(data, '\n'), 0600); err != nil {
			return err
		}
	} else {
		data, err := os.ReadFile(*statePath)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, &saved); err != nil {
			return err
		}
		if len(saved.Captures) != 8 {
			return errors.New("state must contain all eight captures")
		}
	}
	if *phase == "projects" {
		if *restrictedProject == "" {
			return errors.New("restricted-project is required for projects phase")
		}
		saved.ProjectScenarios, err = projectScenarios(ctx, session, absWorkspace, *restrictedProject)
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(saved, "", "  ")
		if err != nil {
			return err
		}
		if err = os.WriteFile(*statePath, append(data, '\n'), 0600); err != nil {
			return err
		}
		fmt.Println("PASS projects: actual API and real stdio; restricted checkout unchanged with local mirroring enabled; linked worktrees share identity and preserve provenance; non-Git error, explicit resolution and stable replay")
		return nil
	}
	for index, item := range append(saved.Captures, saved.ProjectScenarios...) {
		out, err := call(ctx, session, "get_record", map[string]any{"id": item.RecordID})
		if err != nil {
			return err
		}
		record, ok := out["record"].(map[string]any)
		if !ok {
			return fmt.Errorf("missing fetched record: %v", out)
		}
		if record["id"] != item.RecordID || record["projectId"] != item.ProjectID || record["type"] != strings.TrimPrefix(item.Tool, "record_") {
			return fmt.Errorf("record mismatch: %v", record)
		}
		if index >= len(saved.Captures) {
			gitContext, _ := record["gitContext"].(map[string]any)
			if !reflect.DeepEqual(gitContext, item.GitContext) {
				return fmt.Errorf("project scenario lost original Git provenance: %v", record)
			}
		}
		payload, ok := record["payload"].(map[string]any)
		if !ok || len(payload) == 0 {
			return errors.New("typed payload was not persisted")
		}
		if content, ok := record["content"].(string); !ok || content == "" {
			return errors.New("deterministic content missing")
		}
		if *phase == "verify" {
			retried, err := call(ctx, session, item.Tool, item.Arguments)
			if err != nil {
				return fmt.Errorf("replay %s: %w", item.Tool, err)
			}
			r, _ := retried["record"].(map[string]any)
			if r["id"] != item.RecordID {
				return errors.New("retry created a duplicate")
			}
		}
	}
	projectID := saved.Captures[0].ProjectID
	resumeContext, err := call(ctx, session, "get_project_context", map[string]any{"workingDirectory": absWorkspace})
	if err != nil {
		return err
	}
	if err = verifyResumeContext(resumeContext, saved.Captures); err != nil {
		return err
	}
	search, err := call(ctx, session, "search_memory", map[string]any{"projectId": projectID, "query": "Integration", "limit": 100})
	if err != nil {
		return err
	}
	encoded, _ := json.Marshal(search)
	for _, item := range saved.Captures {
		if !strings.Contains(string(encoded), item.RecordID) {
			return fmt.Errorf("search omitted captured record %s: %s", item.RecordID, encoded)
		}
	}
	if _, err = call(ctx, session, "get_decision_history", map[string]any{"projectId": projectID}); err != nil {
		return err
	}
	if _, err = call(ctx, session, "find_related", map[string]any{"id": saved.Captures[0].RecordID}); err != nil {
		return err
	}
	if *phase == "verify" {
		changed := map[string]any{}
		for key, value := range saved.Captures[0].Arguments {
			changed[key] = value
		}
		changed["decision"] = "Changed content must conflict"
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: saved.Captures[0].Tool, Arguments: changed})
		if err != nil {
			return err
		}
		data, _ := json.Marshal(result)
		if !result.IsError || !strings.Contains(string(data), "CONFLICT") {
			return fmt.Errorf("changed replay did not report CONFLICT: %s", data)
		}
	}
	fmt.Printf("PASS %s: real stdio → SSH challenge → HTTP → persistent records; 8 typed captures, search, context, history, relationships", *phase)
	if *phase == "verify" {
		fmt.Print("; restart persistence, exact replay, changed-payload conflict")
		if len(saved.ProjectScenarios) > 0 {
			fmt.Printf("; %d project-boundary captures survive restore", len(saved.ProjectScenarios))
		}
	}
	fmt.Println()
	return nil
}

// Verify the information a new agent needs to resume the disposable project,
// rather than treating a successful context request as semantic acceptance.
func verifyResumeContext(context map[string]any, captures []capture) error {
	fields := map[string]string{
		"record_decision": "recentDecisions", "record_adr": "recentDecisions", "record_business_decision": "recentDecisions",
		"record_finding": "openFindings", "record_failure": "failures", "record_constraint": "constraints", "record_project_state": "stateHistory",
	}
	for _, item := range captures {
		field, relevant := fields[item.Tool]
		if !relevant {
			continue
		}
		list, _ := context[field].([]any)
		var linked map[string]any
		for _, value := range list {
			record, _ := value.(map[string]any)
			if record["id"] == item.RecordID {
				linked = record
				break
			}
		}
		if linked == nil || linked["projectId"] != item.ProjectID {
			return fmt.Errorf("resume context omitted linked %s capture %s", field, item.RecordID)
		}
		if item.Tool == "record_project_state" {
			current, _ := context["currentState"].(map[string]any)
			payload, _ := current["payload"].(map[string]any)
			if current["id"] != item.RecordID {
				return errors.New("resume context did not identify the current state source")
			}
			for _, name := range []string{"state", "reason", "previousState", "followUp"} {
				if payload[name] != item.Arguments[name] {
					return fmt.Errorf("resume context lost project-state %s", name)
				}
			}
		}
	}
	return nil
}
func call(ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) (map[string]any, error) {
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if result.IsError {
		data, _ := json.Marshal(result)
		return nil, fmt.Errorf("%s: %s", name, data)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if string(data) != "null" {
		if err = json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			if err = json.Unmarshal([]byte(text.Text), &out); err == nil {
				return out, nil
			}
		}
	}
	return nil, fmt.Errorf("%s returned no JSON object", name)
}
