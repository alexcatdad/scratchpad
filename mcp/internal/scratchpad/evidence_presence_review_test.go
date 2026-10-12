package scratchpad

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioEvidenceRejectsExplicitNullLimits(t *testing.T) {
	var calls atomic.Int32
	ctx, session := presenceStdioSession(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/rag/retrieve" {
			http.Error(w, "unexpected route", http.StatusNotFound)
			return
		}
		calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		for _, field := range []string{"limit", "maxChars"} {
			if value, exists := body[field]; exists && value == nil {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":{"code":"VALIDATION_FAILED","message":"A supplied bound cannot be null."}}`)
				return
			}
		}
		fmt.Fprint(w, `{"passages":[],"coverage":{"available":false}}`)
	})
	for _, field := range []string{"limit", "maxChars"} {
		t.Run(field, func(t *testing.T) {
			before := calls.Load()
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "retrieve_evidence", Arguments: map[string]any{"projectId": "proj_presence", "query": "Synthetic question", field: nil}})
			if err == nil && (result == nil || !result.IsError) {
				t.Fatalf("explicit %s=null selected defaults", field)
			}
			if calls.Load() != before {
				t.Fatal("invalid bound initiated retrieval")
			}
		})
	}
	for _, arguments := range []map[string]any{
		{"projectId": "proj_presence", "query": "Synthetic question"},
		{"projectId": "proj_presence", "query": "Synthetic question", "limit": 1, "maxChars": 4096},
	} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "retrieve_evidence", Arguments: arguments})
		if err != nil || result.IsError {
			t.Fatalf("omitted or valid limits failed: %+v, %v", result, err)
		}
	}
}
