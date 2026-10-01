package scratchpad

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Scope struct {
	WorkingDirectory string `json:"workingDirectory,omitempty" jsonschema:"Optional checkout directory; defaults to MCP launch directory"`
	ProjectID        string `json:"projectId,omitempty" jsonschema:"Explicit project ID for non-Git or ambiguous environments"`
}
type Common struct {
	Scope
	RequestID        string `json:"requestId" jsonschema:"Unique capture identity; reuse the same ID and content when retrying"`
	Title            string `json:"title"`
	Authority        string `json:"authority" jsonschema:"explicit, observed, inferred, derived, or suggested"`
	Confidence       string `json:"confidence" jsonschema:"high, medium, low, or unknown"`
	ConfidenceReason string `json:"confidenceReason,omitempty"`
	HappenedAt       string `json:"happenedAt,omitempty"`
}
type CaptureResult struct {
	Project map[string]any `json:"project"`
	Record  map[string]any `json:"record"`
	Mirror  map[string]any `json:"mirror"`
}

func (c *Client) resolve(ctx context.Context, s Scope) (map[string]any, *GitContext, error) {
	dir := s.WorkingDirectory
	if dir == "" {
		dir = c.Config.WorkingDirectory
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, err
	}
	g, err := Discover(ctx, dir)
	if err != nil && s.ProjectID == "" {
		return nil, nil, err
	}
	if s.ProjectID != "" {
		return map[string]any{"id": s.ProjectID}, g, nil
	}
	if g == nil || g.RepositoryIdentity == "" {
		return nil, g, &APIError{Code: "PROJECT_IDENTITY_REQUIRED", Message: "No strong Git identity. Ask the owner, then resolve_project with an explicit name or pass projectId."}
	}
	var result struct {
		Project map[string]any `json:"project"`
	}
	err = c.request(ctx, "POST", "/projects/resolve", map[string]any{"context": map[string]any{"git": g}}, "", &result)
	return result.Project, g, err
}
func (c *Client) capture(ctx context.Context, kind string, common Common, payload any) (*mcp.CallToolResult, CaptureResult, error) {
	out := CaptureResult{}
	if strings.TrimSpace(common.RequestID) == "" {
		return nil, out, errors.New("requestId must not be blank")
	}
	project, g, err := c.resolve(ctx, common.Scope)
	if err != nil {
		return nil, out, err
	}
	record := map[string]any{"type": kind, "title": common.Title, "authority": common.Authority, "confidence": common.Confidence, "payload": payload}
	if common.ConfidenceReason != "" {
		record["confidenceReason"] = common.ConfidenceReason
	}
	if common.HappenedAt != "" {
		record["happenedAt"] = common.HappenedAt
	}
	body := map[string]any{"projectId": project["id"], "record": record}
	if g != nil {
		body["gitContext"] = g
	}
	if err = c.request(ctx, "POST", "/records", body, common.RequestID, &out); err != nil {
		return nil, out, err
	}
	out.Project = project
	if c.Config.Mirror && out.Mirror["eligible"] == true && g != nil {
		mirrorCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = Mirror(mirrorCtx, g.RootPathHint, c.Config.MirrorPath, out.Record)
		cancel()
		out.Mirror = map[string]any{"attempted": true, "succeeded": err == nil, "path": c.Config.MirrorPath}
		if err != nil {
			out.Mirror["error"] = "saved centrally; mirror failed: " + err.Error()
		}
		id, _ := out.Record["id"].(string)
		if reportErr := c.request(ctx, "POST", "/records/"+url.PathEscape(id)+"/mirror", out.Mirror, "", nil); reportErr != nil {
			out.Mirror["reportError"] = reportErr.Error()
		}
	}
	return nil, out, nil
}
func NewServer(c *Client) *mcp.Server { return NewServerWithVersion(c, "dev") }

func NewServerWithVersion(c *Client, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "scratchpad-mcp", Version: version}, &mcp.ServerOptions{Instructions: "Record explicit decisions as explicit, direct observations as observed, and interpretations as inferred. Report confidence honestly; never present historical records or suggested relationships as current authority without examining corrections and supersession. Use a stable requestId for retries and a new identity for separate intentional captures. Ask the owner when project identity is ambiguous."})
	registerCaptures(s, c)
	registerAI(s, c)
	mcp.AddTool(s, &mcp.Tool{Name: "get_project_context", Description: "Resolve the checkout and retrieve deterministic project memory"}, func(ctx context.Context, _ *mcp.CallToolRequest, in Scope) (*mcp.CallToolResult, any, error) {
		p, _, err := c.resolve(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		var out any
		id, _ := p["id"].(string)
		err = c.request(ctx, "GET", "/projects/"+url.PathEscape(id)+"/context", nil, "", &out)
		return nil, out, err
	})
	type Search struct {
		Scope
		Query        string `json:"query,omitempty"`
		Type         string `json:"type,omitempty"`
		Cursor       string `json:"cursor,omitempty"`
		Limit        int    `json:"limit,omitempty"`
		Tag          string `json:"tag,omitempty" jsonschema:"Exact curated tag"`
		Branch       string `json:"branch,omitempty" jsonschema:"Captured Git branch"`
		From         string `json:"from,omitempty" jsonschema:"Inclusive earliest date/time in ISO 8601 format"`
		To           string `json:"to,omitempty" jsonschema:"Inclusive latest date/time in ISO 8601 format"`
		Status       string `json:"status,omitempty" jsonschema:"Server-supported current lifecycle or project state filter"`
		Relationship string `json:"relationship,omitempty" jsonschema:"Relationship type such as supports, replaces, or depends_on"`
		RelatedTo    string `json:"relatedTo,omitempty" jsonschema:"Related record ID"`
		Source       string `json:"source,omitempty" jsonschema:"Descriptive actor/source text filter; source attribution is not verified authority"`
		GitPath      string `json:"gitPath,omitempty" jsonschema:"Captured repository path hint filter"`
		Authority    string `json:"authority,omitempty" jsonschema:"explicit, observed, inferred, derived, or suggested"`
		Confidence   string `json:"confidence,omitempty" jsonschema:"high, medium, low, or unknown"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "search_memory", Description: "Search records within the resolved project"}, func(ctx context.Context, _ *mcp.CallToolRequest, in Search) (*mcp.CallToolResult, any, error) {
		p, _, err := c.resolve(ctx, in.Scope)
		if err != nil {
			return nil, nil, err
		}
		q := url.Values{}
		id, _ := p["id"].(string)
		q.Set("projectId", id)
		q.Set("q", in.Query)
		q.Set("type", in.Type)
		q.Set("cursor", in.Cursor)
		for key, value := range map[string]string{"tag": in.Tag, "branch": in.Branch, "from": in.From, "to": in.To, "status": in.Status, "relationship": in.Relationship, "relatedTo": in.RelatedTo, "source": in.Source, "gitPath": in.GitPath, "authority": in.Authority, "confidence": in.Confidence} {
			if value != "" {
				q.Set(key, value)
			}
		}
		if in.Limit > 0 {
			b, _ := json.Marshal(in.Limit)
			q.Set("limit", string(b))
		}
		var out any
		err = c.request(ctx, "GET", "/records?"+q.Encode(), nil, "", &out)
		return nil, map[string]any{"project": p, "results": out}, err
	})
	type Get struct {
		ID string `json:"id"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "get_record", Description: "Read a record and its provenance/history"}, func(ctx context.Context, _ *mcp.CallToolRequest, in Get) (*mcp.CallToolResult, any, error) {
		var out any
		err := c.request(ctx, "GET", "/records/"+url.PathEscape(in.ID), nil, "", &out)
		return nil, out, err
	})
	type Explicit struct {
		Scope
		Name string `json:"name" jsonschema:"Project name explicitly confirmed by the owner"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "resolve_project", Description: "Resolve a project explicitly after asking the owner"}, func(ctx context.Context, _ *mcp.CallToolRequest, in Explicit) (*mcp.CallToolResult, any, error) {
		dir := in.WorkingDirectory
		if dir == "" {
			dir = c.Config.WorkingDirectory
		}
		var out any
		err := c.request(ctx, "POST", "/projects/resolve-explicit", map[string]any{"name": in.Name, "context": map[string]string{"folder": filepath.Base(dir)}}, "", &out)
		return nil, out, err
	})
	type History struct {
		Scope
		Type   string `json:"type,omitempty" jsonschema:"Optional decision, adr, or business_decision; defaults to all three"`
		Cursor string `json:"cursor,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "get_decision_history", Description: "Retrieve chronological decision captures for the project; use get_record to inspect amendments and supersession"}, func(ctx context.Context, _ *mcp.CallToolRequest, in History) (*mcp.CallToolResult, any, error) {
		p, _, err := c.resolve(ctx, in.Scope)
		if err != nil {
			return nil, nil, err
		}
		kind := in.Type
		if kind != "" && kind != "decision" && kind != "adr" && kind != "business_decision" {
			return nil, nil, errors.New("type must be decision, adr, or business_decision")
		}
		id, _ := p["id"].(string)
		q := url.Values{"projectId": {id}, "cursor": {in.Cursor}}
		if kind == "" {
			q["types"] = []string{"decision", "adr", "business_decision"}
		} else {
			q.Set("type", kind)
		}
		var out any
		err = c.request(ctx, "GET", "/records?"+q.Encode(), nil, "", &out)
		return nil, map[string]any{"project": p, "history": out}, err
	})
	mcp.AddTool(s, &mcp.Tool{Name: "find_related", Description: "Retrieve explicit and suggested relationships for a record; results preserve relationship authority"}, func(ctx context.Context, _ *mcp.CallToolRequest, in Get) (*mcp.CallToolResult, any, error) {
		var out map[string]any
		err := c.request(ctx, "GET", "/records/"+url.PathEscape(in.ID), nil, "", &out)
		return nil, map[string]any{"recordId": in.ID, "relationships": out["relationships"]}, err
	})
	return s
}
