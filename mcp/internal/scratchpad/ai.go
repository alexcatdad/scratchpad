package scratchpad

import (
	"context"
	"errors"
	"net/url"

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
	mcp.AddTool(server, &mcp.Tool{Name: "get_ai_jobs", Description: "Inspect persisted optional AI job status, retries and failures. Provider failure does not affect deterministic memory."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		var result any
		err := client.request(ctx, "GET", "/ai/jobs", nil, "", &result)
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
