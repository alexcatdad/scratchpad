package backend_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The server binary embeds whatever dashboard build is in the checkout: the
// real one after `npm run build -w @scratchpad/web`, none otherwise. Both are
// valid here; internal/httpapi tests cover the file handling itself.
func TestEmbeddedDashboardServesOnlyTheRootDocument(t *testing.T) {
	t.Setenv("SCRATCHPAD_DASHBOARD_URL", "")
	origin := startServer(t, legacyFixture(t), "")
	index, err := http.Get(origin + "/?view=Memory")
	if err != nil {
		t.Fatal(err)
	}
	index.Body.Close()
	switch index.StatusCode {
	case 200:
		if index.Header.Get("Content-Type") != "text/html; charset=utf-8" || index.Header.Get("X-Content-Type-Options") != "nosniff" || index.Header.Get("Cache-Control") != "no-cache" {
			t.Fatalf("dashboard document headers = %v", index.Header)
		}
	case 404:
	default:
		t.Fatalf("dashboard document = %d", index.StatusCode)
	}
	for _, path := range []string{"/placeholder.txt", "/not-a-page", "/assets/", "/api/v1/not-a-real-route"} {
		response, err := http.Get(origin + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 404 || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
			t.Fatalf("GET %s = %d", path, response.StatusCode)
		}
	}
	response, err := http.Post(origin+"/", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatalf("POST / = %d", response.StatusCode)
	}
}

func TestDashboardDevelopmentProxyDoesNotReceiveAPICredentialsOrHandleAPIRoutes(t *testing.T) {
	development := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "scratchpad_session=synthetic-renderer-cookie")
		_ = json.NewEncoder(w).Encode(map[string]string{"authorization": r.Header.Get("Authorization"), "cookie": r.Header.Get("Cookie"), "forwarded": r.Header.Get("X-Forwarded-For")})
	}))
	defer development.Close()
	t.Setenv("SCRATCHPAD_DASHBOARD_URL", development.URL)
	origin := startServer(t, legacyFixture(t), "")
	request, _ := http.NewRequest("GET", origin+"/", nil)
	request.Header.Set("Authorization", "Bearer synthetic-dashboard-token")
	request.Header.Set("Cookie", "scratchpad_session=synthetic-dashboard-session")
	request.Header.Set("X-Forwarded-For", "192.0.2.1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var received map[string]string
	if json.NewDecoder(response.Body).Decode(&received) != nil || response.StatusCode != 200 {
		t.Fatal("development server forwarding failed")
	}
	for _, value := range received {
		if value != "" {
			t.Fatal("development server received API credentials or forwarding identity")
		}
	}
	if len(response.Cookies()) != 0 {
		t.Fatal("development server could overwrite authoritative API cookies")
	}
	unknown, err := http.Get(origin + "/api/v1/not-a-real-route")
	if err != nil {
		t.Fatal(err)
	}
	unknown.Body.Close()
	if unknown.StatusCode != 404 {
		t.Fatal("unknown API route was delegated to the development server")
	}
}
