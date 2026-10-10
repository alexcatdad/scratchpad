package scratchpad

import (
	"context"
	"errors"
	"fmt"
	"net"
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

func requestOrigin(u *url.URL) string {
	if u == nil || u.User != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if address, err := netip.ParseAddr(host); err == nil {
		host = address.String()
	}
	port := u.Port()
	if number, err := strconv.Atoi(port); err == nil {
		port = strconv.Itoa(number)
	}
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else if u.Scheme == "http" {
			port = "80"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(host, port)
}

type authenticatedTransport struct {
	client HttpRequestDoer
	origin string
}

func (t authenticatedTransport) Do(request *http.Request) (*http.Response, error) {
	if request == nil || requestOrigin(request.URL) != t.origin {
		return nil, errors.New("use the configured instance origin")
	}
	if request.Host != "" {
		hostURL := *request.URL
		hostURL.Host = request.Host
		if requestOrigin(&hostURL) != t.origin {
			return nil, errors.New("use the configured instance origin")
		}
	}
	return t.client.Do(request)
}

// NewAuthenticatedClient expects an instance origin URL (paths include /api/v1).
func NewAuthenticatedClient(baseURL string, auth Auth) (*ClientWithResponses, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || strings.ContainsAny(baseURL, "?#") || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
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
	options = append(options, WithHTTPClient(authenticatedTransport{client: transport, origin: requestOrigin(u)}))
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
