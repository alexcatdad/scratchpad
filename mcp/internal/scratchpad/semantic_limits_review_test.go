package scratchpad

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioSemanticSearchRejectsInvalidLimitsBeforeDispatch(t *testing.T) {
	var requests atomic.Int32
	ctx, session := presenceStdioSession(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "POST" || r.URL.Path != "/api/v1/search/semantic" {
			http.Error(w, "unexpected route", 404)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["projectId"] != "proj_presence" || body["query"] != "Synthetic meaning" {
			t.Error("semantic scope or query changed")
		}
		// Return the received public HTTP bound to verify omitted versus explicit values.
		response := map[string]any{"results": []any{}}
		if limit, exists := body["limit"]; exists {
			response["receivedLimit"] = limit
		}
		encoded, _ := json.Marshal(response)
		fmt.Fprint(w, string(encoded))
	})
	for _, limit := range []any{0, -1, 101, nil, 1.5, "2"} {
		t.Run(fmt.Sprintf("invalid_%v", limit), func(t *testing.T) {
			before := requests.Load()
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "semantic_search", Arguments: map[string]any{"projectId": "proj_presence", "query": "Synthetic meaning", "limit": limit}})
			if err == nil && (result == nil || !result.IsError) {
				t.Fatalf("invalid semantic bound %v selected results", limit)
			}
			if requests.Load() != before {
				t.Fatal("invalid semantic bound reached backend")
			}
		})
	}
	for _, limit := range []int{0, 1, 100} {
		input := map[string]any{"projectId": "proj_presence", "query": "Synthetic meaning"}
		if limit != 0 {
			input["limit"] = limit
		}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "semantic_search", Arguments: input})
		if err != nil || result.IsError {
			t.Fatalf("omitted or valid semantic bound failed: %v %+v", err, result)
		}
		body := result.StructuredContent.(map[string]any)
		if limit == 0 && body["receivedLimit"] != nil {
			t.Fatal("omitted limit became explicit")
		}
		if limit != 0 && body["receivedLimit"] != float64(limit) {
			t.Fatalf("valid bound lost: %+v", body)
		}
	}
}
