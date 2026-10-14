package scratchpad

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioCompactLimitsRequireExplicitVersionTwo(t *testing.T) {
	var requests atomic.Int32
	ctx, session := presenceStdioSession(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("version") == "2" {
			fmt.Fprint(w, `{"version":2,"records":[]}`)
		} else {
			fmt.Fprint(w, `{"recentDecisions":[]}`)
		}
	})
	for _, version := range []any{nil, 1} {
		for _, field := range []struct {
			name  string
			value int
		}{{"limit", 1}, {"maxChars", 4096}, {"previewChars", 32}} {
			input := map[string]any{"projectId": "proj_presence", field.name: field.value}
			if version != nil {
				input["version"] = version
			}
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_project_context", Arguments: input})
			if err == nil && !result.IsError {
				t.Errorf("legacy context silently ignored %s", field.name)
			}
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid compact limit/version combination reached API")
	}
	for _, input := range []map[string]any{{"projectId": "proj_presence"}, {"projectId": "proj_presence", "version": 1}, {"projectId": "proj_presence", "version": 2, "limit": 1, "maxChars": 4096, "previewChars": 32}} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_project_context", Arguments: input})
		if err != nil || result.IsError {
			t.Fatal("valid legacy or compact context failed")
		}
	}
}

func TestStdioEvidenceUsesServerDeadlineBeyondThirtySeconds(t *testing.T) {
	ctx, session := presenceStdioSession(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/rag/retrieve" {
			http.Error(w, "unexpected", 400)
			return
		}
		// A real delayed HTTP response reproduces the client's fixed 30s cap;
		// the fixture represents a provider wait within a longer server deadline.
		timer := time.NewTimer(31 * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, `{"results":[{"recordId":"rec_slow_evidence"}],"resultsOmitted":0,"semantic":{"available":true,"indexCoverage":"complete"}}`)
	}, 45*time.Second)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "retrieve_evidence", Arguments: map[string]any{"projectId": "proj_presence", "query": "Slow valid evidence"}})
	if err != nil || result.IsError {
		t.Fatalf("valid server-bounded evidence wait was prematurely canceled: %v %+v", err, result)
	}
	if result.StructuredContent.(map[string]any)["results"].([]any)[0].(map[string]any)["recordId"] != "rec_slow_evidence" {
		t.Fatal("slow evidence response lost citations")
	}
}

func TestStdioEvidenceCallerCancellationReachesHTTP(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	ctx, session := presenceStdioSession(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-release:
		}
	})
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: "retrieve_evidence", Arguments: map[string]any{"projectId": "proj_presence", "query": "Cancel evidence"}})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(4 * time.Second):
		t.Fatal("evidence request did not reach HTTP")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled caller received success")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("caller cancellation did not finish")
	}
	select {
	case <-canceled:
	case <-time.After(4 * time.Second):
		t.Fatal("caller cancellation did not stop HTTP")
	}
}
