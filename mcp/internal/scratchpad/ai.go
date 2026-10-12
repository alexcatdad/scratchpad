package scratchpad

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AI is optional, and project consent and derived-data policy remain server-owned.
type AIScope struct {
	Scope
	CrossProject bool     `json:"crossProject,omitempty" jsonschema:"Explicitly request all permitted projects; never includes projects denying AI or cross-project analysis"`
	ProjectIDs   []string `json:"projectIds,omitempty" jsonschema:"Optional project IDs for explicitly requested cross-project operations"`
}

func (c *Client) aiScope(ctx context.Context, scope AIScope) (map[string]any, error) {
	if scope.CrossProject {
		body := map[string]any{"crossProject": true}
		if len(scope.ProjectIDs) > 0 {
			body["projectIds"] = scope.ProjectIDs
		}
		return body, nil
	}
	if len(scope.ProjectIDs) > 0 {
		return nil, errors.New("projectIds requires explicit crossProject=true; otherwise use projectId or checkout discovery")
	}
	project, _, err := c.resolve(ctx, scope.Scope)
	if err != nil {
		return nil, err
	}
	return map[string]any{"projectId": project["id"]}, nil
}

func registerAI(server *mcp.Server, client *Client) {
	type Evidence struct {
		AIScope
		Query    string `json:"query" jsonschema:"Question or identifier to retrieve cited project evidence for"`
		Intent   string `json:"intent,omitempty" jsonschema:"current (default) or history; historical choices are not current policy"`
		Limit    *int   `json:"limit,omitempty" jsonschema:"Maximum evidence passages, 1 to 100"`
		MaxChars *int   `json:"maxChars,omitempty" jsonschema:"Serialized response byte budget, 4096 to 64000"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "retrieve_evidence", Description: "Retrieve bounded cited evidence on the Go PostgreSQL RAG backend. Defaults to the discovered project; cross-project retrieval requires explicit participation scope. Preserves authority, source links, lifecycle and indexing uncertainty; lexical fallback can work during model/index outages. Scores do not establish accepted policy. Follow source IDs with get_record, and never treat incomplete indexing as absent evidence."}, func(ctx context.Context, request *mcp.CallToolRequest, input Evidence) (*mcp.CallToolResult, any, error) {
		if nullArgument(request, "limit") || nullArgument(request, "maxChars") {
			return nil, nil, errors.New("invalid evidence response limits")
		}
		if input.Intent != "" && input.Intent != "current" && input.Intent != "history" {
			return nil, nil, errors.New("intent must be current or history")
		}
		if (input.Limit != nil && (*input.Limit < 1 || *input.Limit > 100)) || (input.MaxChars != nil && (*input.MaxChars < 4096 || *input.MaxChars > 64000)) {
			return nil, nil, errors.New("invalid evidence response limits")
		}
		body, err := client.aiScope(ctx, input.AIScope)
		if err != nil {
			return nil, nil, err
		}
		body["query"] = input.Query
		if input.Intent != "" {
			body["intent"] = input.Intent
		}
		if input.Limit != nil {
			body["limit"] = *input.Limit
		}
		if input.MaxChars != nil {
			body["maxChars"] = *input.MaxChars
		}
		var result any
		err = client.request(ctx, "POST", "/rag/retrieve", body, "", &result)
		return nil, result, err
	})
	type SemanticSearch struct {
		AIScope
		Query string `json:"query" jsonschema:"Meaning to search for; requires compatible built embeddings and project consent"`
		Limit int    `json:"limit,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "semantic_search", Description: "Optional search by meaning in the resolved project or explicitly permitted cross-project scope. Returns derived similarity scores and source records; exact search_memory remains independent of AI."}, func(ctx context.Context, _ *mcp.CallToolRequest, input SemanticSearch) (*mcp.CallToolResult, any, error) {
		body, err := client.aiScope(ctx, input.AIScope)
		if err != nil {
			return nil, nil, err
		}
		body["query"] = input.Query
		if input.Limit > 0 {
			body["limit"] = input.Limit
		}
		var result any
		err = client.request(ctx, "POST", "/search/semantic", body, "", &result)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_suggestions", Description: "Read optional AI summaries, patterns and reviewable suggestions for the resolved project or permitted cross-project scope. Derived output is not approved human policy."}, func(ctx context.Context, _ *mcp.CallToolRequest, input AIScope) (*mcp.CallToolResult, any, error) {
		body, err := client.aiScope(ctx, input)
		if err != nil {
			return nil, nil, err
		}
		query := url.Values{}
		if input.CrossProject {
			query.Set("crossProject", "true")
			for _, id := range input.ProjectIDs {
				query.Add("projectIds", id)
			}
		} else {
			query.Set("projectId", body["projectId"].(string))
		}
		var result any
		err = client.request(ctx, "GET", "/suggestions?"+query.Encode(), nil, "", &result)
		return nil, result, err
	})
	type Analyze struct {
		AIScope
		Type string `json:"type" jsonschema:"analyze for derived summaries/suggestions or embed for a rebuildable semantic index"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "process_memory", Description: "Queue optional analysis or embeddings when the owner requests processing. Does not enable AI, alter raw captures, or approve suggestions; poll get_ai_jobs for completion."}, func(ctx context.Context, _ *mcp.CallToolRequest, input Analyze) (*mcp.CallToolResult, any, error) {
		if input.Type != "analyze" && input.Type != "embed" {
			return nil, nil, errors.New("type must be analyze or embed")
		}
		body, err := client.aiScope(ctx, input.AIScope)
		if err != nil {
			return nil, nil, err
		}
		body["type"] = input.Type
		var result any
		err = client.request(ctx, "POST", "/ai/jobs", body, "", &result)
		return nil, result, err
	})
	type Jobs struct {
		Limit  *int    `json:"limit,omitempty" jsonschema:"Page size, 1 to 200; omitted uses the server default"`
		Cursor *string `json:"cursor,omitempty" jsonschema:"Opaque nonempty nextCursor from a previous page"`
		JobID  *string `json:"jobId,omitempty" jsonschema:"Poll one persisted job by ID; cannot be combined with cursor"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "get_ai_jobs", Description: "Inspect persisted optional AI job status, retries and failures. Follow nextCursor for further pages; hasActive covers all jobs. Use jobId to poll a specific job. Provider failure does not affect deterministic memory."}, func(ctx context.Context, request *mcp.CallToolRequest, input Jobs) (*mcp.CallToolResult, any, error) {
		if nullArgument(request, "limit") || nullArgument(request, "cursor") || nullArgument(request, "jobId") || (input.Limit != nil && (*input.Limit < 1 || *input.Limit > 200)) || (input.Cursor != nil && *input.Cursor == "") || (input.JobID != nil && (*input.JobID == "" || input.Cursor != nil)) {
			return nil, nil, errors.New("invalid job pagination or lookup")
		}
		query := url.Values{}
		if input.Limit != nil {
			query.Set("limit", strconv.Itoa(*input.Limit))
		}
		if input.Cursor != nil {
			query.Set("cursor", *input.Cursor)
		}
		if input.JobID != nil {
			query.Set("jobId", *input.JobID)
		}
		path := "/ai/jobs"
		if len(query) > 0 {
			path += "?" + query.Encode()
		}
		var result any
		err := client.request(ctx, "GET", path, nil, "", &result)
		if err == nil && len(query) > 0 {
			body, _ := result.(map[string]any)
			if _, supported := body["hasActive"].(bool); !supported {
				return nil, nil, errors.New("backend does not support AI job pagination or lookup; update the backend or call get_ai_jobs without arguments")
			}
		}
		return nil, result, err
	})
	type Document struct {
		Scope
		Format string `json:"format" jsonschema:"handoff, architecture, decisions, client_history, or adr"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "generate_document", Description: "Queue an owner-requested private Markdown handoff/report/ADR document from the resolved project. Sharing requires a separate explicit choice; inspect generated artifacts with get_suggestions."}, func(ctx context.Context, _ *mcp.CallToolRequest, input Document) (*mcp.CallToolResult, any, error) {
		body, err := client.aiScope(ctx, AIScope{Scope: input.Scope})
		if err != nil {
			return nil, nil, err
		}
		body["format"] = input.Format
		var result any
		err = client.request(ctx, "POST", "/summaries/export", body, "", &result)
		return nil, result, err
	})
}
