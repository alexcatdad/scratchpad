// Command rag-integration exercises cited evidence through the actual stdio MCP.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	flag.String("url", "", "Backend URL")
	flag.String("public-key", "", "Synthetic public-key file")
	flag.String("signing-key", "", "Synthetic private-key file")
	workspace := flag.String("workspace", "", "Synthetic Git checkout")
	binary := flag.String("binary", "", "MCP binary")
	other := flag.String("other-project", "", "Second consenting project")
	denied := flag.String("denied-project", "", "Excluded project")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, *binary)
	cmd.Dir = *workspace
	cmd.Env = append(os.Environ(), "SCRATCHPAD_MIRROR=false")
	session, err := mcp.NewClient(&mcp.Implementation{Name: "rag-integration", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return err
	}
	defer session.Close()
	contextResult, err := call(ctx, session, "get_project_context", map[string]any{})
	if err != nil {
		return err
	}
	local := contextResult["project"].(map[string]any)["id"].(string)
	for _, cross := range []bool{false, true} {
		input := map[string]any{"query": "STDIO_RAG_EVIDENCE", "intent": "history", "limit": 10, "maxChars": 4096}
		if cross {
			input["crossProject"], input["projectIds"] = true, []string{local, *other}
		}
		result, err := call(ctx, session, "retrieve_evidence", input)
		if err != nil {
			return err
		}
		results, ok := result["results"].([]any)
		if !ok || len(results) == 0 {
			return errors.New("RAG tool returned no evidence")
		}
		seen := map[string]bool{}
		for _, value := range results {
			item := value.(map[string]any)
			pid := item["projectId"].(string)
			if pid != local && (!cross || pid != *other) {
				return errors.New("RAG tool escaped selected scope")
			}
			if item["sourceUrl"] != "/api/v1/records/"+item["recordId"].(string) || !strings.Contains(item["text"].(string), "STDIO_RAG_EVIDENCE") {
				return errors.New("RAG tool lost source citation")
			}
			seen[pid] = true
		}
		if !seen[local] || (cross && !seen[*other]) {
			return errors.New("RAG tool omitted selected evidence")
		}
		if result["semantic"].(map[string]any)["available"] != false {
			return errors.New("RAG tool hid lexical fallback uncertainty")
		}
		encoded, _ := json.Marshal(result)
		if len(encoded)+1 > 4096 {
			return errors.New("RAG tool lost response budget")
		}
	}
	for _, input := range []map[string]any{
		{"projectId": *denied, "query": "STDIO_RAG_EVIDENCE"},
		{"projectIds": []string{local, *other}, "query": "STDIO_RAG_EVIDENCE"},
		{"projectId": local, "query": "STDIO_RAG_EVIDENCE", "intent": "unsupported"},
		{"projectId": local, "query": "STDIO_RAG_EVIDENCE", "limit": 0},
		{"projectId": local, "query": "STDIO_RAG_EVIDENCE", "maxChars": 0},
	} {
		out, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "retrieve_evidence", Arguments: input})
		if err == nil && !out.IsError {
			return errors.New("invalid or forbidden RAG request succeeded")
		}
	}
	fmt.Println("Verified real stdio RAG discovery, cited lexical fallback, budgets and explicit participation scope.")
	return nil
}
func call(ctx context.Context, session *mcp.ClientSession, name string, input map[string]any) (map[string]any, error) {
	out, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: input})
	if err != nil {
		return nil, err
	}
	if out.IsError {
		return nil, fmt.Errorf("%s failed: %v", name, out.Content)
	}
	raw, err := json.Marshal(out.StructuredContent)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = json.Unmarshal(raw, &result)
	return result, err
}
