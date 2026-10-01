// Command ai-integration verifies optional AI tools through the real stdio MCP.
// All application authentication and project consent checks remain enabled.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	endpoint := flag.String("url", "", "Application URL")
	public := flag.String("public-key", "", "Enrolled SSH public key")
	private := flag.String("signing-key", "", "Disposable signing key")
	workspace := flag.String("workspace", "", "Disposable Git checkout")
	binary := flag.String("binary", "", "Real MCP executable")
	other := flag.String("other-project", "", "Second permitted project")
	denied := flag.String("denied-project", "", "Project denying AI processing")
	flag.Parse()
	if *endpoint == "" || *public == "" || *private == "" || *workspace == "" || *binary == "" || *other == "" || *denied == "" {
		return errors.New("All integration flags are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	absBinary, err := filepath.Abs(*binary)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, absBinary)
	command.Dir = *workspace
	command.Stderr = os.Stderr
	command.Env = append(os.Environ(), "SCRATCHPAD_URL="+*endpoint, "SCRATCHPAD_PUBLIC_KEY="+*public, "SCRATCHPAD_SIGNING_KEY="+*private, "SCRATCHPAD_MIRROR=false")
	session, err := mcp.NewClient(&mcp.Implementation{Name: "scratchpad-ai-integration", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		return err
	}
	defer session.Close()
	projectContext, err := call(ctx, session, "get_project_context", map[string]any{})
	if err != nil {
		return err
	}
	project, _ := projectContext["project"].(map[string]any)
	local, _ := project["id"].(string)
	if local == "" {
		return errors.New("Checkout discovery returned no project")
	}
	localScope := map[string]any{"projectId": local}
	crossScope := map[string]any{"crossProject": true, "projectIds": []string{local, *other}}
	for _, scope := range []map[string]any{localScope, crossScope} {
		// Omit the local project ID to prove the default execution directory is authoritative.
		input := map[string]any{"type": "embed"}
		if scope["crossProject"] == true {
			input["crossProject"] = true
			input["projectIds"] = scope["projectIds"]
		}
		out, err := call(ctx, session, "process_memory", input)
		if err != nil {
			return err
		}
		job, _ := out["job"].(map[string]any)
		if err = verifyScope(job, scope); err != nil {
			return err
		}
		if err = waitJob(ctx, session, job); err != nil {
			return err
		}
		query := map[string]any{"query": "private durable memory"}
		if scope["crossProject"] == true {
			query["crossProject"] = true
			query["projectIds"] = scope["projectIds"]
		}
		result, err := call(ctx, session, "semantic_search", query)
		if err != nil {
			return err
		}
		matches, _ := result["results"].([]any)
		if len(matches) == 0 {
			return errors.New("Semantic tool returned no source-linked records")
		}
		seen := map[string]bool{}
		for _, item := range matches {
			match, _ := item.(map[string]any)
			record, _ := match["record"].(map[string]any)
			pid, _ := record["projectId"].(string)
			if record["id"] == "" || pid != local && (scope["crossProject"] != true || pid != *other) {
				return fmt.Errorf("Semantic result escaped requested consent scope: %v", record)
			}
			seen[pid] = true
		}
		if !seen[local] || scope["crossProject"] == true && !seen[*other] {
			return errors.New("Semantic result omitted indexed requested project")
		}
		input["type"] = "analyze"
		out, err = call(ctx, session, "process_memory", input)
		if err != nil {
			return err
		}
		job, _ = out["job"].(map[string]any)
		if err = waitJob(ctx, session, job); err != nil {
			return err
		}
	}
	generated, err := call(ctx, session, "generate_document", map[string]any{"format": "handoff"})
	if err != nil {
		return err
	}
	job, _ := generated["job"].(map[string]any)
	if err = verifyScope(job, localScope); err != nil {
		return err
	}
	if err = waitJob(ctx, session, job); err != nil {
		return err
	}
	suggestions, err := call(ctx, session, "get_suggestions", map[string]any{})
	if err != nil {
		return err
	}
	items, _ := suggestions["suggestions"].([]any)
	if len(items) < 2 {
		return errors.New("Local suggestions omitted analysis or generated document")
	}
	document := false
	for _, item := range items {
		artifact, _ := item.(map[string]any)
		if err = verifyArtifact(artifact, local, *other, false); err != nil {
			return err
		}
		if err = verifySources(ctx, session, artifact); err != nil {
			return err
		}
		if artifact["kind"] == "export" {
			content, _ := artifact["content"].(map[string]any)
			if content["markdown"] == "" || artifact["format"] != "handoff" {
				return errors.New("Generated handoff lost private Markdown output")
			}
			document = true
		}
	}
	if !document {
		return errors.New("Document tool did not produce a source-linked handoff")
	}
	suggestions, err = call(ctx, session, "get_suggestions", crossScope)
	if err != nil {
		return err
	}
	items, _ = suggestions["suggestions"].([]any)
	crossFound := false
	for _, item := range items {
		artifact, _ := item.(map[string]any)
		if err = verifyArtifact(artifact, local, *other, true); err != nil {
			return err
		}
		if err = verifySources(ctx, session, artifact); err != nil {
			return err
		}
		if artifact["crossProject"] == true {
			crossFound = true
		}
	}
	if !crossFound {
		return errors.New("Explicit cross-project suggestions missing")
	}
	for _, tool := range []string{"process_memory", "semantic_search"} {
		args := map[string]any{"crossProject": true, "projectIds": []string{local, *denied}, "type": "analyze", "query": "private memory"}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err == nil && !result.IsError {
			return fmt.Errorf("%s silently included or ignored an explicitly denied project", tool)
		}
	}
	deniedSuggestions, err := call(ctx, session, "get_suggestions", map[string]any{"crossProject": true, "projectIds": []string{local, *denied}})
	if err == nil {
		values, _ := deniedSuggestions["suggestions"].([]any)
		for _, value := range values {
			artifact, _ := value.(map[string]any)
			if err := verifyArtifact(artifact, local, *other, false); err != nil {
				return errors.New("Suggestion retrieval exposed denied project sources")
			}
		}
	}
	fmt.Println("Verified all five optional AI tools through authenticated real stdio MCP; local discovery, explicit cross-project consent, source citations, semantic retrieval and private handoff generation.")
	return nil
}
func verifyScope(job, expected map[string]any) error {
	scope, _ := job["scope"].(map[string]any)
	ids, _ := scope["projectIds"].([]any)
	if expected["crossProject"] == true {
		if len(ids) != 2 || scope["crossProject"] != true {
			return errors.New("Cross-project job scope changed")
		}
	} else if len(ids) != 1 || ids[0] != expected["projectId"] {
		return errors.New("Default local job escaped checkout project")
	}
	return nil
}
func verifyArtifact(item map[string]any, local, other string, cross bool) error {
	sources, _ := item["sourceRecordIds"].([]any)
	projects, _ := item["projectIds"].([]any)
	if len(sources) == 0 || len(projects) == 0 || item["authority"] != "derived" || item["private"] != true {
		return errors.New("AI artifact lost source citations or derived/private provenance")
	}
	for _, project := range projects {
		if project != local && (!cross || project != other) {
			return errors.New("Suggestion escaped selected projects")
		}
	}
	return nil
}
func verifySources(ctx context.Context, session *mcp.ClientSession, artifact map[string]any) error {
	sources, _ := artifact["sourceRecordIds"].([]any)
	projects, _ := artifact["projectIds"].([]any)
	content, _ := artifact["content"].(map[string]any)
	markdown, _ := content["markdown"].(string)
	for _, source := range sources {
		record, err := call(ctx, session, "get_record", map[string]any{"id": source})
		if err != nil {
			return err
		}
		raw, _ := record["record"].(map[string]any)
		if raw["id"] != source {
			return errors.New("Derived citation could not reread its immutable source")
		}
		belongs := false
		for _, project := range projects {
			if raw["projectId"] == project {
				belongs = true
			}
		}
		if !belongs {
			return errors.New("Cited source belongs to a different project")
		}
		if artifact["kind"] == "export" && !strings.Contains(markdown, source.(string)) {
			return errors.New("Export Markdown omitted its source citation")
		}
	}
	return nil
}
func waitJob(ctx context.Context, session *mcp.ClientSession, job map[string]any) error {
	key, _ := job["id"].(string)
	if key == "" {
		return errors.New("AI tool returned no durable job identity")
	}
	for {
		out, err := call(ctx, session, "get_ai_jobs", map[string]any{})
		if err != nil {
			return err
		}
		jobs, _ := out["jobs"].([]any)
		for _, value := range jobs {
			current, _ := value.(map[string]any)
			if current["id"] != key {
				continue
			}
			if current["status"] == "completed" {
				return nil
			}
			if current["status"] == "failed" {
				return fmt.Errorf("AI job failed: %v", current["lastError"])
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
func call(ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) (map[string]any, error) {
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
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
		err = json.Unmarshal(data, &out)
		return out, err
	}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			if json.Unmarshal([]byte(text.Text), &out) == nil {
				return out, nil
			}
		}
	}
	return nil, errors.New("MCP tool returned no structured JSON")
}
