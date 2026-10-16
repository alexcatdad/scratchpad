package localcapture

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

const OpOnlineRequest Operation = "online_request"
const OnlineTimeout = 120 * time.Second

type OnlineRequest struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
}
type OnlineResponse struct {
	Body      json.RawMessage `json:"body,omitempty"`
	ErrorCode string          `json:"errorCode,omitempty"`
}
type OnlineService interface {
	OnlineRequest(context.Context, OnlineRequest) (OnlineResponse, error)
}
type OnlineAdapter interface {
	OnlineRequest(context.Context, Binding, OnlineRequest) (OnlineResponse, error)
}

// Only tool-exposed operations are forwarded. In particular POST /records and
// authentication/administration endpoints cannot bypass durable capture intake.
func ValidOnlineRequest(r OnlineRequest) bool {
	u, err := url.ParseRequestURI(r.Path)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || strings.Contains(u.Path, "//") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	for _, part := range parts {
		if part == "." || part == ".." || part == "" {
			return false
		}
	}
	if r.Method == "GET" {
		if u.Path == "/records" || u.Path == "/projects" || u.Path == "/suggestions" || u.Path == "/ai/jobs" {
			return true
		}
		return (len(parts) == 2 && parts[0] == "records") || (len(parts) == 3 && parts[0] == "projects" && parts[2] == "context")
	}
	if r.Method != "POST" || u.RawQuery != "" {
		return false
	}
	switch u.Path {
	case "/projects/resolve", "/projects/resolve-explicit", "/search/semantic", "/rag/retrieve", "/ai/jobs", "/summaries/export":
		return true
	}
	return false
}
func (c *IPCClient) OnlineRequest(ctx context.Context, req OnlineRequest) (OnlineResponse, error) {
	var out OnlineResponse
	client := *c
	client.Timeout = OnlineTimeout + time.Second
	err := client.call(ctx, OpOnlineRequest, req, &out)
	return out, err
}
func (r *Runtime) OnlineRequest(ctx context.Context, req OnlineRequest) (OnlineResponse, error) {
	if !ValidOnlineRequest(req) {
		return OnlineResponse{}, ErrConflictSentinel
	}
	r.mu.Lock()
	stopped, quiescing := r.stopped, r.quiescing
	r.mu.Unlock()
	if stopped {
		return OnlineResponse{}, ErrServiceStoppedSentinel
	}
	if quiescing {
		return OnlineResponse{}, ErrServiceQuiescingSentinel
	}
	r.configMu.RLock()
	c, err := r.store.configuration()
	generation := r.configGeneration
	r.configMu.RUnlock()
	if err != nil {
		return OnlineResponse{}, err
	}
	if err = validateConfiguration(c); err != nil {
		return OnlineResponse{}, ErrKeyUnavailableSentinel
	}
	adapter, ok := r.adapter.(OnlineAdapter)
	if !ok {
		return OnlineResponse{}, ErrUnknownOperationSentinel
	}
	ctx, cancel := context.WithTimeout(ctx, OnlineTimeout)
	defer cancel()
	out, err := adapter.OnlineRequest(ctx, Binding{ServerURL: c.ServerURL, CredentialFingerprint: c.CredentialFingerprint, PublicKeyPath: c.PublicKeyPath, SigningKeyPath: c.SigningKeyPath}, req)
	if err != nil {
		code, _ := classifyFailure(err, DeliveryConnectivity, true)
		r.noteConnectionFor(generation, code)
		return OnlineResponse{}, err
	}
	if !r.noteConnectionFor(generation, "") {
		return OnlineResponse{}, ErrConflictSentinel
	}
	return out, nil
}
