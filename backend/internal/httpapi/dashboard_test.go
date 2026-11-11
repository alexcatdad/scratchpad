package httpapi

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

type dashboardResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func dashboardRequest(t *testing.T, handler http.Handler, method, target string, headers ...string) dashboardResponse {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	for index := 0; index+1 < len(headers); index += 2 {
		request.Header.Set(headers[index], headers[index+1])
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return dashboardResponse{recorder.Code, recorder.Header(), recorder.Body.Bytes()}
}

func TestStaticDashboardServesTheBuild(t *testing.T) {
	script := strings.Repeat("console.log('synthetic dashboard');", 64)
	handler, err := StaticDashboard(fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>Synthetic</title>")},
		"assets/index-abc.js":  {Data: []byte(script)},
		"assets/index-abc.css": {Data: []byte("body{}")},
		"assets/unknown.bin":   {Data: []byte{0, 1, 2}},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, method := range []string{"GET", "HEAD"} {
		// The query string carries dashboard state and never selects the file.
		index := dashboardRequest(t, handler, method, "/?view=Memory&authError=denied")
		body := index.Body
		if index.StatusCode != 200 || index.Header.Get("Content-Type") != "text/html; charset=utf-8" || index.Header.Get("Cache-Control") != "no-cache" || index.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s / = %d %v", method, index.StatusCode, index.Header)
		}
		if (method == "GET") != strings.Contains(string(body), "Synthetic") {
			t.Fatalf("%s / body = %q", method, body)
		}
	}

	asset := dashboardRequest(t, handler, "GET", "/assets/index-abc.js")
	body := asset.Body
	if asset.StatusCode != 200 || string(body) != script || asset.Header.Get("Content-Type") != "text/javascript; charset=utf-8" || asset.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" || asset.Header.Get("X-Content-Type-Options") != "nosniff" || asset.Header.Get("Content-Encoding") != "" || asset.Header.Get("Vary") != "Accept-Encoding" {
		t.Fatalf("asset = %d %v", asset.StatusCode, asset.Header)
	}
	if style := dashboardRequest(t, handler, "GET", "/assets/index-abc.css"); style.Header.Get("Content-Type") != "text/css; charset=utf-8" {
		t.Fatalf("stylesheet type = %q", style.Header.Get("Content-Type"))
	}
	if unknown := dashboardRequest(t, handler, "GET", "/assets/unknown.bin"); unknown.Header.Get("Content-Type") != "application/octet-stream" || unknown.Header.Get("Vary") != "" {
		t.Fatalf("unknown type = %v", unknown.Header)
	}

	compressed := dashboardRequest(t, handler, "GET", "/assets/index-abc.js", "Accept-Encoding", "br, gzip;q=0.8")
	reader, err := gzip.NewReader(bytes.NewReader(compressed.Body))
	if err != nil || compressed.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip response = %v %v", err, compressed.Header)
	}
	if body, _ := io.ReadAll(reader); string(body) != script {
		t.Fatal("gzip body differs from the asset")
	}
	if compressed.Header.Get("ETag") == asset.Header.Get("ETag") {
		t.Fatal("encodings share an ETag")
	}
	if refused := dashboardRequest(t, handler, "GET", "/assets/index-abc.js", "Accept-Encoding", "gzip;q=0"); refused.Header.Get("Content-Encoding") != "" {
		t.Fatal("gzip was sent to a client that refused it")
	}
	if cached := dashboardRequest(t, handler, "GET", "/assets/index-abc.js", "If-None-Match", asset.Header.Get("ETag")); cached.StatusCode != 304 {
		t.Fatalf("conditional request = %d", cached.StatusCode)
	}

	for _, target := range []string{"/missing", "/assets/", "/assets/../index.html", "/api/v1/not-a-real-route", "/api/"} {
		if response := dashboardRequest(t, handler, "GET", target); response.StatusCode != 404 || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
			t.Fatalf("GET %s = %d", target, response.StatusCode)
		}
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if response := dashboardRequest(t, handler, method, "/"); response.StatusCode != 404 {
			t.Fatalf("%s / = %d", method, response.StatusCode)
		}
	}
}

func TestStaticDashboardWithoutABuildIsNotFound(t *testing.T) {
	handler, err := StaticDashboard(fstest.MapFS{"placeholder.txt": {Data: []byte("no build")}})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/", "/placeholder.txt", "/index.html"} {
		if response := dashboardRequest(t, handler, "GET", target); response.StatusCode != 404 {
			t.Fatalf("GET %s = %d", target, response.StatusCode)
		}
	}
}

func TestDashboardSelectsEmbeddedBuildOrDevelopmentProxy(t *testing.T) {
	if _, err := Dashboard(""); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"http://192.0.2.1:4000", "http://127.0.0.1:4000/path", "ftp://127.0.0.1", "http://user@127.0.0.1:4000"} {
		if _, err := Dashboard(target); err == nil {
			t.Fatalf("development URL %s was accepted", target)
		}
	}
	// Nothing listens on the discard port.
	handler, err := Dashboard("http://127.0.0.1:9")
	if err != nil {
		t.Fatal(err)
	}
	if response := dashboardRequest(t, handler, "GET", "/"); response.StatusCode != 503 {
		t.Fatalf("unreachable development server = %d", response.StatusCode)
	}
	if response := dashboardRequest(t, handler, "POST", "/"); response.StatusCode != 404 {
		t.Fatalf("POST through the proxy = %d", response.StatusCode)
	}
}
