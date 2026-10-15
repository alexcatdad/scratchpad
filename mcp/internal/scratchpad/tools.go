package scratchpad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
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
	Outcome          string         `json:"outcome"`
	Receipt          map[string]any `json:"receipt"`
	CentralRecordID  string         `json:"centralRecordId,omitempty"`
	CentralProjectID string         `json:"centralProjectId,omitempty"`
	Project          map[string]any `json:"project,omitempty"`
	Record           map[string]any `json:"record,omitempty"`
	Mirror           map[string]any `json:"mirror,omitempty"`
}

func (c *Client) resolve(ctx context.Context, s Scope) (map[string]any, *GitContext, error) {
	if s.ProjectID != "" {
		return map[string]any{"id": s.ProjectID}, nil, nil
	}
	dir := s.WorkingDirectory
	if dir == "" {
		dir = c.Config.WorkingDirectory
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, err
	}
	g, err := Discover(ctx, dir)
	if err != nil {
		return nil, nil, err
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
func (c *Client) resolveCapture(ctx context.Context, s Scope) (map[string]any, *GitContext, error) {
	if s.ProjectID == "" {
		return c.resolve(ctx, s)
	}
	dir := s.WorkingDirectory
	if dir == "" {
		dir = c.Config.WorkingDirectory
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, err
	}
	// Capture retains known checkout provenance after explicit owner selection,
	// including partial context when discovery reports ambiguous remotes.
	g, _ := Discover(ctx, dir)
	return map[string]any{"id": s.ProjectID}, g, nil
}

func (c *Client) capture(ctx context.Context, kind string, common Common, payload any) (*mcp.CallToolResult, CaptureResult, error) {
	out := CaptureResult{}
	if strings.TrimSpace(common.RequestID) == "" {
		return nil, out, errors.New("requestId must not be blank")
	}
	if c.CaptureService == nil {
		return nil, out, errors.New("local capture service is unavailable")
	}
	// Snapshot the invocation's repository before any server-side project
	// resolution. The durable service owns resolution and retries after intake.
	dir := captureWorkingDirectory(c, common.Scope)
	g, err := Discover(ctx, dir)
	var discoveryAPIError *APIError
	if err != nil && !(common.Scope.ProjectID != "" && g != nil && errors.As(err, &discoveryAPIError) && discoveryAPIError.Code == "PROJECT_IDENTITY_AMBIGUOUS") {
		return nil, out, err
	}
	configuration, err := c.CaptureService.GetConfiguration(ctx)
	if err != nil {
		return nil, out, errors.New("local capture service configuration is unavailable")
	}
	if err = c.validateCaptureConfiguration(configuration); err != nil {
		return nil, out, err
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, out, errors.New("capture payload is invalid")
	}
	record := localcapture.Record{Type: kind, Title: common.Title, Authority: common.Authority, Confidence: common.Confidence, Payload: encodedPayload}
	if common.ConfidenceReason != "" {
		record.ConfidenceReason = common.ConfidenceReason
	}
	if common.HappenedAt != "" {
		record.HappenedAt = common.HappenedAt
	}
	var gitContext json.RawMessage
	if g != nil {
		gitContext, err = json.Marshal(g)
		if err != nil {
			return nil, out, errors.New("Git provenance snapshot is invalid")
		}
	}
	request := localcapture.CaptureRequest{
		RequestID: common.RequestID,
		Record:    record,
		Scope: localcapture.Scope{
			WorkingDirectory: captureWorkingDirectory(c, common.Scope),
			ProjectID:        common.Scope.ProjectID,
		},
		GitContext: gitContext,
		Mirror: localcapture.MirrorConsent{
			Enabled: c.Config.Mirror,
			Path:    c.Config.MirrorPath,
		},
		Binding: localcapture.Binding{
			ServerURL: configuration.ServerURL, CredentialFingerprint: configuration.CredentialFingerprint,
			PublicKeyPath: configuration.PublicKeyPath, SigningKeyPath: configuration.SigningKeyPath,
		},
	}
	result, err := c.CaptureService.Capture(ctx, request)
	if err != nil {
		if errors.Is(err, localcapture.ErrConflictSentinel) {
			return nil, out, fmt.Errorf("CONFLICT: %w", err)
		}
		return nil, out, err
	}
	out.Outcome = string(result.Outcome)
	out.Receipt, err = jsonObject(result.Receipt)
	if err != nil {
		return nil, CaptureResult{}, err
	}
	if result.Outcome == localcapture.OutcomeDelivered {
		out.CentralRecordID = result.Receipt.CentralRecordID
		out.CentralProjectID = result.Receipt.CentralProjectID
		out.Record, err = rawJSONObject(result.CentralRecord)
		if err != nil {
			return nil, CaptureResult{}, errors.New("local capture service returned an invalid central record")
		}
		out.Project, err = rawJSONObject(result.CentralProject)
		if err != nil {
			return nil, CaptureResult{}, errors.New("local capture service returned an invalid central project")
		}
		out.Mirror, _ = rawJSONObject(result.Mirror)
	}
	return nil, out, nil
}

func captureWorkingDirectory(c *Client, scope Scope) string {
	dir := scope.WorkingDirectory
	if dir == "" {
		dir = c.Config.WorkingDirectory
	}
	absolute, err := filepath.Abs(dir)
	if err == nil {
		return absolute
	}
	return dir
}

func (c *Client) validateCaptureConfiguration(configuration localcapture.ServiceConfiguration) error {
	configuredURL, err := url.Parse(configuration.ServerURL)
	clientURL, clientErr := url.Parse(c.Config.URL)
	if err != nil || clientErr != nil || configuredURL.Scheme != clientURL.Scheme || !strings.EqualFold(configuredURL.Host, clientURL.Host) || strings.TrimRight(configuredURL.Path, "/") != strings.TrimRight(clientURL.Path, "/") || configuration.CredentialFingerprint == "" {
		return errors.New("MCP server settings do not match the local capture service configuration")
	}
	for _, pair := range [][2]string{{configuration.PublicKeyPath, c.Config.PublicKeyPath}, {configuration.SigningKeyPath, c.Config.SigningKeyPath}} {
		configuredPath, pathErr := filepath.Abs(pair[0])
		clientPath, clientPathErr := filepath.Abs(pair[1])
		if pathErr != nil || clientPathErr != nil || filepath.Clean(configuredPath) != filepath.Clean(clientPath) {
			return errors.New("MCP SSH key references do not match the local capture service configuration")
		}
	}
	return nil
}

func jsonObject(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return rawJSONObject(data)
}

func rawJSONObject(data []byte) (map[string]any, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	var value map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}
func NewServer(c *Client) *mcp.Server { return NewServerWithVersion(c, "dev") }

func nullArgument(request *mcp.CallToolRequest, name string) bool {
	var arguments map[string]json.RawMessage
	if json.Unmarshal(request.Params.Arguments, &arguments) != nil {
		return false
	}
	value, exists := arguments[name]
	return exists && strings.TrimSpace(string(value)) == "null"
}

func compactVersionTwo(value any) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	text, exponent := string(number), 0
	if at := strings.IndexAny(text, "eE"); at >= 0 {
		var err error
		exponent, err = strconv.Atoi(text[at+1:])
		if err != nil {
			return false
		}
		text = text[:at]
	}
	fraction := 0
	if at := strings.IndexByte(text, '.'); at >= 0 {
		fraction = len(text) - at - 1
		text = text[:at] + text[at+1:]
	}
	text = strings.TrimLeft(text, "0")
	coefficient := strings.TrimRight(text, "0")
	return coefficient == "2" && exponent == fraction-(len(text)-len(coefficient))
}

func NewServerWithVersion(c *Client, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "scratchpad-mcp", Version: version}, &mcp.ServerOptions{Instructions: "Record explicit decisions as explicit, direct observations as observed, and interpretations as inferred. Report confidence honestly; never present historical records or suggested relationships as current authority without examining corrections and supersession. Use a stable requestId for retries and a new identity for separate intentional captures. Ask the owner when project identity is ambiguous. Record only secret references. Keep API tokens, passwords, private keys and installation encryption keys out of captures. Synthetic reference: secret://provider/main. Keep the value in owner-controlled secret configuration."})
	registerCaptures(s, c)
	registerCaptureReceipts(s, c)
	registerAI(s, c)
	type ContextInput struct {
		Scope
		Version      *int `json:"version,omitempty" jsonschema:"Omit for legacy context; version 2 requests bounded compact context on the Go backend"`
		Limit        *int `json:"limit,omitempty" jsonschema:"Compact source record limit, 1 to 100"`
		MaxChars     *int `json:"maxChars,omitempty" jsonschema:"Compact serialized response budget, 4096 to 64000 bytes"`
		PreviewChars *int `json:"previewChars,omitempty" jsonschema:"Compact preview limit, 32 to 2000 UTF-16 units"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "get_project_context", Description: "Resolve the checkout and retrieve deterministic project memory; explicitly request version 2 for compact bounded previews"}, func(ctx context.Context, request *mcp.CallToolRequest, in ContextInput) (*mcp.CallToolResult, any, error) {
		for _, field := range []string{"version", "limit", "maxChars", "previewChars"} {
			if nullArgument(request, field) {
				return nil, nil, &APIError{Code: "VALIDATION_FAILED", Message: field + " must be an integer when supplied."}
			}
		}
		if (in.Limit != nil || in.MaxChars != nil || in.PreviewChars != nil) && (in.Version == nil || *in.Version != 2) {
			return nil, nil, &APIError{Code: "VALIDATION_FAILED", Message: "Compact context limits require explicit version 2."}
		}
		p, _, err := c.resolve(ctx, in.Scope)
		if err != nil {
			return nil, nil, err
		}
		var out any
		id, _ := p["id"].(string)
		path := "/projects/" + url.PathEscape(id) + "/context"
		params := url.Values{}
		for key, value := range map[string]*int{"version": in.Version, "limit": in.Limit, "maxChars": in.MaxChars, "previewChars": in.PreviewChars} {
			if value != nil {
				params.Set(key, strconv.Itoa(*value))
			}
		}
		if len(params) > 0 {
			path += "?" + params.Encode()
		}
		err = c.request(ctx, "GET", path, nil, "", &out)
		if err == nil && in.Version != nil && *in.Version == 2 {
			view, ok := out.(map[string]any)
			if !ok || !compactVersionTwo(view["version"]) {
				return nil, nil, errors.New("COMPACT_CONTEXT_UNAVAILABLE: this server does not support version 2; request legacy context or use the Go backend")
			}
		}
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

func registerCaptureReceipts(server *mcp.Server, c *Client) {
	type Input struct {
		ReceiptID string `json:"receiptId" jsonschema:"Local capture receipt ID returned by a record_* tool"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "get_capture_receipt", Description: "Look up the local delivery state for a durable capture receipt"}, func(ctx context.Context, _ *mcp.CallToolRequest, input Input) (*mcp.CallToolResult, any, error) {
		if c.CaptureService == nil {
			return nil, nil, errors.New("local capture service is unavailable")
		}
		if strings.TrimSpace(input.ReceiptID) == "" {
			return nil, nil, errors.New("receiptId must not be blank")
		}
		receipt, err := c.CaptureService.LookupReceipt(ctx, input.ReceiptID)
		if err != nil {
			return nil, nil, err
		}
		return nil, receipt, nil
	})
}
