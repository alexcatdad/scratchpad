package scratchpad

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func presenceStdioSession(t *testing.T, handler http.HandlerFunc) (context.Context, *mcp.ClientSession) {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-presence-token" {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(api.Close)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, executable, "-test.run=^TestStdioProtocol$")
	command.Env = append(os.Environ(), "SCRATCHPAD_TEST_HELPER=1", "SCRATCHPAD_TEST_API_URL="+api.URL)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "presence-stdio", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return ctx, session
}

func TestStdioQARejectsExplicitEmptyAnswer(t *testing.T) {
	ctx, session := presenceStdioSession(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/records" {
			http.Error(w, "unexpected route", http.StatusNotFound)
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		record := body["record"].(map[string]any)
		payload := record["payload"].(map[string]any)
		if answer, supplied := payload["answer"]; supplied {
			text, ok := answer.(string)
			if !ok || strings.TrimSpace(text) == "" {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":{"code":"VALIDATION_FAILED","message":"A supplied answer must be nonempty."}}`)
				return
			}
		}
		record["id"] = "rec_presence"
		json.NewEncoder(w).Encode(map[string]any{"record": record, "mirror": map[string]any{"eligible": false}})
	})
	for _, scenario := range []struct {
		name, answer string
		supplied     bool
		wantError    bool
	}{{"empty", "", true, true}, {"omitted", "", false, false}, {"answered", "Synthetic answer evidence.", true, false}, {"whitespace", " \t", true, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			arguments := map[string]any{"projectId": "proj_presence", "requestId": "qa-presence-" + scenario.name, "title": "Question", "authority": "explicit", "confidence": "high", "question": "Where is the answer evidence?"}
			if scenario.supplied {
				arguments["answer"] = scenario.answer
			}
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "record_qa", Arguments: arguments})
			failed := err != nil || (result != nil && result.IsError)
			if failed != scenario.wantError {
				t.Fatalf("supplied answer presence changed capture behavior: %+v, %v", result, err)
			}
			if !failed {
				body := result.StructuredContent.(map[string]any)
				payload := body["record"].(map[string]any)["payload"].(map[string]any)
				answer, exists := payload["answer"]
				if exists != scenario.supplied || (exists && answer != scenario.answer) {
					t.Fatal("successful capture changed answer presence or text")
				}
			}
		})
	}
}

func TestStdioContextRejectsExplicitZeroLimits(t *testing.T) {
	ctx, session := presenceStdioSession(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/projects/proj_presence/context" {
			http.Error(w, "unexpected route", http.StatusNotFound)
			return
		}
		// The external API fixture implements the published numerical contract.
		for name, bounds := range map[string][2]int{"version": {1, 2}, "limit": {1, 100}, "maxChars": {4096, 64000}, "previewChars": {32, 2000}} {
			if raw, exists := r.URL.Query()[name]; exists {
				value, err := strconv.Atoi(raw[0])
				if err != nil || value < bounds[0] || value > bounds[1] {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"error":{"code":"VALIDATION_FAILED","message":"Invalid context limit."}}`)
					return
				}
			}
		}
		if r.URL.Query().Get("version") == "2" {
			fmt.Fprint(w, `{"version":2,"records":[]}`)
		} else {
			fmt.Fprint(w, `{"currentState":null,"recentDecisions":[]}`)
		}
	})
	for _, field := range []string{"version", "limit", "maxChars", "previewChars"} {
		t.Run(field, func(t *testing.T) {
			arguments := map[string]any{"projectId": "proj_presence", "version": 2, field: 0}
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_project_context", Arguments: arguments})
			if err == nil && (result == nil || !result.IsError) {
				t.Fatalf("explicit %s=0 silently selected defaults", field)
			}
		})
	}
	for _, arguments := range []map[string]any{{"projectId": "proj_presence"}, {"projectId": "proj_presence", "version": 2}, {"projectId": "proj_presence", "version": 2, "limit": 1, "maxChars": 4096, "previewChars": 32}} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_project_context", Arguments: arguments})
		if err != nil || result.IsError {
			t.Fatalf("omitted or valid context limits no longer work: %+v, %v", result, err)
		}
	}
}

func TestStdioOptionalNullValuesDoNotSelectDefaults(t *testing.T) {
	ctx, session := presenceStdioSession(t, func(w http.ResponseWriter, r *http.Request) {
		for _, values := range r.URL.Query() {
			if values[0] == "null" {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":{"code":"VALIDATION_FAILED","message":"A supplied number cannot be null."}}`)
				return
			}
		}
		if r.Method == "GET" {
			fmt.Fprint(w, `{"version":2,"records":[]}`)
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		record := body["record"].(map[string]any)
		payload := record["payload"].(map[string]any)
		if answer, provided := payload["answer"]; provided && answer == nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"code":"VALIDATION_FAILED","message":"A supplied answer cannot be null."}}`)
			return
		}
		record["id"] = "rec_presence"
		json.NewEncoder(w).Encode(map[string]any{"record": record, "mirror": map[string]any{"eligible": false}})
	})
	for _, field := range []string{"version", "limit", "maxChars", "previewChars"} {
		t.Run(field, func(t *testing.T) {
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_project_context", Arguments: map[string]any{"projectId": "proj_presence", "version": 2, field: nil}})
			if err == nil && (result == nil || !result.IsError) {
				t.Fatalf("explicit %s=null silently selected defaults", field)
			}
		})
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "record_qa", Arguments: map[string]any{"projectId": "proj_presence", "requestId": "qa-null", "title": "Question", "authority": "explicit", "confidence": "high", "question": "Where is the answer?", "answer": nil}})
	if err == nil && (result == nil || !result.IsError) {
		t.Fatal("explicit null answer silently became an unresolved capture")
	}
}
