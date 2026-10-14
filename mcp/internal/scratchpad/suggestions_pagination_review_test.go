package scratchpad

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioSuggestionsRetainPageContinuationAndLegacyCompatibility(t *testing.T) {
	var requests atomic.Int32
	ctx, session := presenceStdioSession(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		query := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/api/v1/suggestions" || query.Get("projectId") != "proj_selected" {
			http.Error(w, "scope lost", 400)
			return
		}
		if query.Get("limit") == "" && query.Get("cursor") == "" {
			fmt.Fprint(w, `{"suggestions":[]}`)
			return
		}
		if query.Get("limit") == "2" && query.Get("cursor") == "synthetic-continuation" {
			fmt.Fprint(w, `{"suggestions":[{"id":"artifact_selected"}],"hasMore":true,"nextCursor":"synthetic-next"}`)
			return
		}
		fmt.Fprint(w, `{"suggestions":[]}`)
	})
	input := map[string]any{"projectId": "proj_selected", "limit": 2, "cursor": "synthetic-continuation"}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_suggestions", Arguments: input})
	if err != nil || result.IsError {
		t.Fatalf("suggestion page failed: %v %+v", err, result)
	}
	body := result.StructuredContent.(map[string]any)
	if body["hasMore"] != true || body["nextCursor"] != "synthetic-next" || body["suggestions"].([]any)[0].(map[string]any)["id"] != "artifact_selected" {
		t.Fatal("suggestion continuation lost")
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "get_suggestions", Arguments: map[string]any{"projectId": "proj_selected"}})
	if err != nil || result.IsError || len(result.StructuredContent.(map[string]any)["suggestions"].([]any)) != 0 {
		t.Fatal("legacy scope-only response changed")
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "get_suggestions", Arguments: map[string]any{"projectId": "proj_selected", "limit": 1}})
	if err != nil || !result.IsError {
		t.Fatal("legacy API silently appeared to honor pagination")
	}
	for _, invalid := range []map[string]any{{"limit": 0}, {"limit": 201}, {"limit": nil}, {"limit": 1.5}, {"cursor": ""}, {"cursor": nil}} {
		invalid["projectId"] = "proj_selected"
		result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "get_suggestions", Arguments: invalid})
		if err == nil && !result.IsError {
			t.Fatal("invalid pagination accepted")
		}
	}
	if requests.Load() != 3 {
		t.Fatal("invalid suggestion page reached API")
	}
}
