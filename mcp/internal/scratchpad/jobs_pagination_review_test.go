package scratchpad

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioAIJobsPreservesContinuationAndSpecificJobPolling(t *testing.T) {
	var requests atomic.Int32
	ctx, session := presenceStdioSession(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		query := r.URL.Query()
		if r.Method == "GET" && r.URL.Path == "/api/v1/ai/jobs" && r.URL.RawQuery == "" {
			fmt.Fprint(w, `{"jobs":[]}`)
			return
		}
		if r.Method != "GET" || r.URL.Path != "/api/v1/ai/jobs" || !((query.Get("limit") == "2" && query.Get("cursor") == "synthetic-next") || query.Get("jobId") == "job_old" || query.Get("limit") == "1" || query.Get("limit") == "200") {
			http.Error(w, "job query changed", 400)
			return
		}
		fmt.Fprint(w, `{"jobs":[{"id":"job_old","status":"running"}],"hasActive":true,"nextCursor":"synthetic-more"}`)
	})
	for _, input := range []map[string]any{{"limit": 2, "cursor": "synthetic-next"}, {"jobId": "job_old"}, {"limit": 1}, {"limit": 200}, {}} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_ai_jobs", Arguments: input})
		if err != nil || result.IsError {
			t.Fatalf("job polling query lost: %v %+v", err, result)
		}
		body := result.StructuredContent.(map[string]any)
		if len(input) == 0 {
			if len(body["jobs"].([]any)) != 0 || body["hasActive"] != nil || body["nextCursor"] != nil {
				t.Fatal("legacy no-argument jobs response changed")
			}
			continue
		}
		if body["hasActive"] != true || body["nextCursor"] != "synthetic-more" || body["jobs"].([]any)[0].(map[string]any)["id"] != "job_old" {
			t.Fatal("job continuation/status changed")
		}
	}
	for _, input := range []map[string]any{{"limit": 0}, {"limit": 201}, {"limit": nil}, {"limit": 1.5}, {"limit": "2"}, {"cursor": ""}, {"cursor": nil}, {"jobId": ""}, {"jobId": nil}, {"jobId": "job_old", "cursor": "synthetic-next"}} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_ai_jobs", Arguments: input})
		if err == nil && !result.IsError {
			t.Fatal("invalid job query accepted")
		}
	}
	if requests.Load() != 5 {
		t.Fatal("invalid pagination reached API")
	}
}

func TestStdioAIJobsExplicitFiltersRejectLegacyUnfilteredResponse(t *testing.T) {
	ctx, session := presenceStdioSession(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jobs":[{"id":"job_unrelated","status":"completed"}]}`)
	})
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_ai_jobs", Arguments: map[string]any{}})
	if err != nil || result.IsError || result.StructuredContent.(map[string]any)["jobs"].([]any)[0].(map[string]any)["id"] != "job_unrelated" {
		t.Fatal("no-argument legacy jobs compatibility failed")
	}
	for _, input := range []map[string]any{{"limit": 1}, {"jobId": "job_requested"}, {"cursor": "synthetic-next"}} {
		result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "get_ai_jobs", Arguments: input})
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Fatal("legacy backend silently appeared to honor explicit job filters")
		}
		message := ""
		for _, content := range result.Content {
			if text, ok := content.(*mcp.TextContent); ok {
				message += text.Text
			}
		}
		if !strings.Contains(message, "does not support") {
			t.Fatalf("unsupported capability error unclear: %s", message)
		}
	}
}
