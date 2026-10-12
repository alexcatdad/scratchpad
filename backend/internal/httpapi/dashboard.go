package httpapi

import (
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

func Dashboard(raw string) (http.Handler, error) {
	if raw == "" {
		return http.NotFoundHandler(), nil
	}
	target, err := url.Parse(raw)
	if err != nil || target.Host == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || (target.Path != "" && target.Path != "/") || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, errors.New("CONFIG_INVALID: dashboard URL must be a loopback HTTP(S) origin")
	}
	host := target.Hostname()
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("CONFIG_INVALID: dashboard URL must be a loopback HTTP(S) origin")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 10 * time.Second
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			for _, name := range []string{"Authorization", "Cookie", "Origin", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
				request.Out.Header.Del(name)
			}
		},
		ModifyResponse: func(response *http.Response) error {
			response.Header.Del("Set-Cookie")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			Error(w, 503, "DASHBOARD_UNAVAILABLE", "Dashboard rendering is unavailable.")
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != "GET" && r.Method != "HEAD") || strings.HasPrefix(r.URL.Path, "/api/") {
			Error(w, 404, "NOT_FOUND", "Endpoint not found.")
			return
		}
		proxy.ServeHTTP(w, r)
	}), nil
}
