package scratchpad

import (
	"context"
	"encoding/json"
	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
	"net/http"
	"os"
)

// NewLocalClient has no server URL, key paths or authentication state. The
// collector owns the connection; checkout discovery remains in the MCP process.
func NewLocalClient(socket string) (*Client, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	service := localcapture.NewClient(socket)
	return &Client{Config: Config{WorkingDirectory: dir}, HTTP: &http.Client{}, CaptureService: service, OnlineService: service}, nil
}

// RequestCentral is used only by the collector's authenticated delivery adapter.
func (c *Client) RequestCentral(ctx context.Context, req localcapture.OnlineRequest) (json.RawMessage, error) {
	var out json.RawMessage
	var err error
	var body any
	if req.Method != http.MethodGet {
		body = req.Body
	}
	if req.Path == "/rag/retrieve" {
		err = c.requestEvidence(ctx, req.Body, &out)
	} else {
		err = c.request(ctx, req.Method, req.Path, body, "", &out)
	}
	return out, err
}
