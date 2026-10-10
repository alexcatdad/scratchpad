package scratchpad

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
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
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("use an HTTPS instance origin or supported loopback HTTP origin")
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, errors.New("use an HTTPS instance origin or supported loopback HTTP origin")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return nil, errors.New("use an HTTPS instance origin or supported loopback HTTP origin")
	}
	if u.Scheme == "http" {
		host := strings.ToLower(u.Hostname())
		if address, err := netip.ParseAddr(host); err == nil {
			host = address.String()
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return nil, errors.New("use an HTTPS instance origin or supported loopback HTTP origin")
		}
	}
	options := []ClientOption{WithRequestEditorFn(func(_ context.Context, request *http.Request) error {
		if auth.Token != "" {
			request.Header.Set("Authorization", "Bearer "+auth.Token)
		}
		if auth.Origin != "" {
			request.Header.Set("Origin", auth.Origin)
		}
		return nil
	})}
	transport := auth.HTTPClient
	if transport == nil {
		transport = &http.Client{}
	}
	if client, ok := transport.(*http.Client); ok {
		copy := *client
		copy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
		transport = &copy
	}
	options = append(options, WithHTTPClient(transport))
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
