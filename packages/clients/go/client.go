package scratchpad

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Auth configures an existing SSH-issued bearer token or a browser cookie client.
// Origin is required for cookie-authenticated mutations. No credentials are enrolled here.
type Auth struct {
	Token      string
	Origin     string
	HTTPClient HttpRequestDoer
}

// NewAuthenticatedClient expects an instance origin URL (paths include /api/v1).
func NewAuthenticatedClient(baseURL string, auth Auth) (*ClientWithResponses, error) {
	options := []ClientOption{WithRequestEditorFn(func(_ context.Context, request *http.Request) error {
		if auth.Token != "" {
			request.Header.Set("Authorization", "Bearer "+auth.Token)
		}
		if auth.Origin != "" {
			request.Header.Set("Origin", auth.Origin)
		}
		return nil
	})}
	if auth.HTTPClient != nil {
		options = append(options, WithHTTPClient(auth.HTTPClient))
	}
	return NewClientWithResponses(strings.TrimRight(baseURL, "/"), options...)
}

// ResponseError preserves typed API failure details and HTTP status.
type ResponseError struct {
	Status int
	Detail Error
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("Scratchpad HTTP %d: %v", e.Status, e.Detail)
}
