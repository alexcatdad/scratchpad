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
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type capture struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	RecordID  string         `json:"recordId"`
	ProjectID string         `json:"projectId"`
}
type state struct {
	Captures []capture `json:"captures"`
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
	phase := flag.String("phase", "capture", "capture or verify after server restart")
	mirror := flag.Bool("mirror", false, "Require successful central and local mirroring for decisions (enable server project setting first)")
	flag.Parse()
	if *publicKey == "" || *workspace == "" || *binary == "" || *statePath == "" {
		return errors.New("public-key, workspace, binary, and state flags are required")
	}
	if *phase != "capture" && *phase != "verify" {
		return errors.New("phase must be capture or verify")
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
	command.Env = append(os.Environ(), "SCRATCHPAD_URL="+*serverURL, "SCRATCHPAD_PUBLIC_KEY="+*publicKey, "SCRATCHPAD_SIGNING_KEY="+*signingKey, fmt.Sprintf("SCRATCHPAD_MIRROR=%t", *mirror))
	session, err := mcp.NewClient(&mcp.Implementation{Name: "scratchpad-integration", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		return fmt.Errorf("initialize real stdio MCP: %w", err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return err
	}
	if len(listed.Tools) != 14 {
		return fmt.Errorf("expected 14 tools, got %d", len(listed.Tools))
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
		}{{"decision", map[string]any{"decision": "Keep private project memory", "rationale": "Reproducible integration"}}, {"adr", map[string]any{"decision": "Use SQLite", "context": "Disposable integration instance"}}, {"business_decision", map[string]any{"decision": "Self hosted", "requestedBy": "integration"}}, {"finding", map[string]any{"finding": "Cross-process capture works"}}, {"qa", map[string]any{"question": "Does real stdio reach persistence?", "answer": "Verified by rereading the record"}}, {"failure", map[string]any{"observed": "Example captured failure", "lesson": "Preserve failure context"}}, {"constraint", map[string]any{"constraint": "English only"}}, {"project_state", map[string]any{"state": "integration-validation"}}}
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
	for _, item := range saved.Captures {
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
	if _, err = call(ctx, session, "get_project_context", map[string]any{"workingDirectory": absWorkspace}); err != nil {
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
	}
	fmt.Println()
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
