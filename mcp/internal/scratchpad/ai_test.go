package scratchpad

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOptionalAIToolsKeepScopeAndProviderErrors(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Error("AI tools bypassed authenticated API")
		}
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		if r.Method == "POST" {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
		}
		switch r.URL.Path {
		case "/api/v1/search/semantic":
			if body["query"] == "disabled" {
				w.WriteHeader(409)
				fmt.Fprint(w, `{"error":{"code":"AI_DISABLED","message":"Enable optional AI first."}}`)
				return
			}
			if body["crossProject"] == true {
				ids, ok := body["projectIds"].([]any)
				if !ok || len(ids) != 2 || ids[0] != "one" || ids[1] != "two" {
					t.Error("cross-project selection lost")
				}
			} else if body["projectId"] != "one" {
				t.Error("project scope lost")
			}
			fmt.Fprint(w, `{"results":[],"indexRequired":true}`)
		case "/api/v1/suggestions":
			if r.URL.Query().Get("crossProject") != "true" || len(r.URL.Query()["projectIds"]) != 2 {
				t.Error("suggestion subset was widened")
			}
			fmt.Fprint(w, `{"suggestions":[]}`)
		case "/api/v1/ai/jobs":
			if r.Method == "POST" && (body["projectId"] != "one" || body["type"] != "embed") {
				t.Error("processing request changed scope or type")
			}
			fmt.Fprint(w, `{"jobs":[],"job":{"id":"job"}}`)
		case "/api/v1/summaries/export":
			if body["projectId"] != "one" || body["format"] != "adr" {
				t.Error("document request changed scope or format")
			}
			fmt.Fprint(w, `{"job":{"id":"job"}}`)
		default:
			t.Errorf("unexpected AI request %s", r.URL.Path)
		}
	}))
	defer api.Close()
	client, err := NewClient(Config{URL: api.URL, WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	client.token = "test"
	client.expires = time.Now().Add(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := NewServer(client).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "AI acceptance", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"semantic_search", map[string]any{"projectId": "one", "query": "resume project"}},
		{"semantic_search", map[string]any{"crossProject": true, "projectIds": []string{"one", "two"}, "query": "shared failure"}},
		{"get_suggestions", map[string]any{"crossProject": true, "projectIds": []string{"one", "two"}}},
		{"process_memory", map[string]any{"projectId": "one", "type": "embed"}},
		{"get_ai_jobs", map[string]any{}},
		{"generate_document", map[string]any{"projectId": "one", "format": "adr"}},
	} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: call.name, Arguments: call.args})
		if err != nil || result.IsError {
			t.Fatalf("%s: %v %v", call.name, result, err)
		}
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "semantic_search", Arguments: map[string]any{"projectId": "one", "query": "disabled"}})
	if err != nil || !result.IsError {
		t.Fatalf("provider policy error lost: %v %v", result, err)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "semantic_search", Arguments: map[string]any{"projectIds": []string{"one", "two"}, "query": "implicit cross project"}})
	if err != nil || !result.IsError {
		t.Fatalf("accepted implicit scope widening: %v %v", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 7 {
		t.Fatalf("unexpected API writes or requests: %d", requests)
	}
}
