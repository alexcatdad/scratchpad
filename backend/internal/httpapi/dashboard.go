package httpapi

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// The dashboard is a static single-page app. `npm run build -w @scratchpad/web`
// writes it to dashboard/dist, which is not committed; placeholder.txt keeps
// the directory embeddable in a checkout with no web build.
//
//go:embed all:dashboard
var embeddedDashboard embed.FS

// Explicit types keep responses independent of the host's MIME database.
var dashboardTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json",
	".map":   "application/json",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".ico":   "image/x-icon",
	".webp":  "image/webp",
	".woff2": "font/woff2",
	".txt":   "text/plain; charset=utf-8",
}

type dashboardFile struct {
	contentType, cacheControl, etag string
	body, compressed                []byte
}

// Dashboard serves the dashboard for GET and HEAD outside /api/. With no
// development URL it serves the build embedded in the binary. A loopback
// development URL proxies a running `vite dev` server instead.
func Dashboard(developmentURL string) (http.Handler, error) {
	if developmentURL != "" {
		return dashboardProxy(developmentURL)
	}
	files, err := fs.Sub(embeddedDashboard, "dashboard/dist")
	if err != nil {
		return nil, err
	}
	return StaticDashboard(files)
}

// StaticDashboard serves a built dashboard: index.html for "/", every other
// file under its own path. Files under assets/ carry content hashes in their
// names and are cached for a year; everything else is revalidated. A build
// with no index.html has no dashboard, so every path is 404 and the binary is
// an API-only server.
func StaticDashboard(build fs.FS) (http.Handler, error) {
	files := map[string]*dashboardFile{}
	if _, err := fs.Stat(build, "index.html"); err == nil {
		err = fs.WalkDir(build, ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			body, err := fs.ReadFile(build, name)
			if err != nil {
				return err
			}
			files["/"+name] = newDashboardFile(name, body)
			return nil
		})
		if err != nil {
			return nil, errors.New("CONFIG_INVALID: embedded dashboard could not be read")
		}
		files["/"] = files["/index.html"]
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file := files[r.URL.Path]
		if (r.Method != "GET" && r.Method != "HEAD") || strings.HasPrefix(r.URL.Path, "/api/") || file == nil {
			Error(w, 404, "NOT_FOUND", "Endpoint not found.")
			return
		}
		header := w.Header()
		header.Set("Content-Type", file.contentType)
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Cache-Control", file.cacheControl)
		body, etag := file.body, file.etag
		if file.compressed != nil {
			header.Set("Vary", "Accept-Encoding")
			if acceptsGzip(r.Header.Get("Accept-Encoding")) {
				header.Set("Content-Encoding", "gzip")
				body, etag = file.compressed, file.etag+"-gzip"
			}
		}
		header.Set("ETag", `"`+etag+`"`)
		// ServeContent answers conditional and range requests.
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
	}), nil
}

func newDashboardFile(name string, body []byte) *dashboardFile {
	sum := sha256.Sum256(body)
	file := &dashboardFile{
		contentType:  dashboardTypes[path.Ext(name)],
		cacheControl: "no-cache",
		etag:         hex.EncodeToString(sum[:16]),
		body:         body,
	}
	if file.contentType == "" {
		file.contentType = "application/octet-stream"
	}
	if strings.HasPrefix(name, "assets/") {
		file.cacheControl = "public, max-age=31536000, immutable"
	}
	if strings.HasPrefix(file.contentType, "text/") || strings.HasPrefix(file.contentType, "application/json") || file.contentType == "image/svg+xml" {
		var buffer bytes.Buffer
		writer, _ := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
		_, _ = writer.Write(body)
		if writer.Close() == nil && buffer.Len() < len(body) {
			file.compressed = buffer.Bytes()
		}
	}
	return file
}

func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		coding, parameters, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		quality, found := strings.CutPrefix(strings.ReplaceAll(strings.ToLower(parameters), " ", ""), "q=")
		if !found {
			return true
		}
		value, err := strconv.ParseFloat(quality, 64)
		return err == nil && value > 0
	}
	return false
}

// dashboardProxy is the development-only path: it forwards dashboard requests
// to a `vite dev` server on loopback so hot reloading works behind the Go
// server. It never forwards credentials or accepts cookies from the target.
func dashboardProxy(raw string) (http.Handler, error) {
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
			Error(w, 503, "DASHBOARD_UNAVAILABLE", "Dashboard development server is unavailable.")
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
