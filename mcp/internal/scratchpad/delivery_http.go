package scratchpad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
)

// ServerBuild is the build a server reports from GET /about.
type ServerBuild struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// About reads the authenticated server build. Servers older than the endpoint
// return an API error, which callers treat as an unknown version.
func (c *Client) About(ctx context.Context) (ServerBuild, error) {
	var result ServerBuild
	if err := c.request(ctx, "GET", "/about", nil, "", &result); err != nil {
		return ServerBuild{}, err
	}
	return result, nil
}

// ResolveGitProject applies the MCP's existing server-backed repository
// resolution to a Git snapshot taken for one capture call.
func (c *Client) ResolveGitProject(ctx context.Context, git json.RawMessage) (map[string]any, error) {
	if len(git) == 0 || string(git) == "null" {
		return nil, &APIError{Code: "PROJECT_IDENTITY_REQUIRED", Message: "No strong Git identity is available for this capture."}
	}
	var result struct {
		Project map[string]any `json:"project"`
	}
	if err := c.request(ctx, "POST", "/projects/resolve", map[string]any{"context": map[string]any{"git": git}}, "", &result); err != nil {
		return nil, err
	}
	if result.Project == nil || result.Project["id"] == "" {
		return nil, errors.New("Scratchpad project resolution returned no project identity")
	}
	return result.Project, nil
}

func (c *Client) ListCentralProjects(ctx context.Context) ([]map[string]any, error) {
	var result struct {
		Projects []map[string]any `json:"projects"`
	}
	if err := c.request(ctx, "GET", "/projects", nil, "", &result); err != nil {
		return nil, err
	}
	return result.Projects, nil
}

// CaptureCentral posts one immutable capture with the original caller identity.
// A present clientSavedAt must be echoed exactly or the server is incompatible
// with the durable local retry contract.
func (c *Client) CaptureCentral(ctx context.Context, projectID string, record map[string]any, git json.RawMessage, requestID, clientSavedAt string) (CaptureResult, error) {
	out := CaptureResult{}
	if clientSavedAt != "" {
		record["clientSavedAt"] = clientSavedAt
	}
	body := map[string]any{"projectId": projectID, "record": record}
	if len(git) > 0 && string(git) != "null" {
		body["gitContext"] = git
	}
	if err := c.request(ctx, "POST", "/records", body, requestID, &out); err != nil {
		return out, err
	}
	if clientSavedAt != "" && out.Record["clientSavedAt"] != clientSavedAt {
		return out, fmt.Errorf("Scratchpad server did not preserve clientSavedAt: expected %q, got %v", clientSavedAt, out.Record["clientSavedAt"])
	}
	out.Project = map[string]any{"id": projectID}
	if c.Config.Mirror && out.Mirror["eligible"] == true && len(git) > 0 {
		var snapshot map[string]any
		if err := json.Unmarshal(git, &snapshot); err != nil {
			return out, errors.New("invalid captured Git snapshot")
		}
		root, _ := snapshot["rootPathHint"].(string)
		if root != "" {
			err := Mirror(ctx, root, c.Config.MirrorPath, out.Record)
			out.Mirror = map[string]any{"attempted": true, "succeeded": err == nil, "path": c.Config.MirrorPath}
			if err != nil {
				out.Mirror["error"] = "saved centrally; mirror failed: " + err.Error()
			}
			id, _ := out.Record["id"].(string)
			if reportErr := c.request(ctx, "POST", "/records/"+url.PathEscape(id)+"/mirror", out.Mirror, "", nil); reportErr != nil {
				out.Mirror["reportError"] = reportErr.Error()
			}
		}
	}
	return out, nil
}
