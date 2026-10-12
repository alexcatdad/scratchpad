package backend_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDashboardRenderingDoesNotReceiveAPICredentialsOrHandleAPIRoutes(t *testing.T) {
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "scratchpad_session=synthetic-renderer-cookie")
		json.NewEncoder(w).Encode(map[string]string{"authorization": r.Header.Get("Authorization"), "cookie": r.Header.Get("Cookie"), "forwarded": r.Header.Get("X-Forwarded-For")})
	}))
	defer renderer.Close()
	t.Setenv("SCRATCHPAD_DASHBOARD_URL", renderer.URL)
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
		t.Fatal("renderer forwarding failed")
	}
	for _, value := range received {
		if value != "" {
			t.Fatal("renderer received API credentials or forwarding identity")
		}
	}
	if len(response.Cookies()) != 0 {
		t.Fatal("renderer could overwrite authoritative API cookies")
	}
	unknown, err := http.Get(origin + "/api/v1/not-a-real-route")
	if err != nil {
		t.Fatal(err)
	}
	unknown.Body.Close()
	if unknown.StatusCode != 404 {
		t.Fatal("unknown API route was delegated to the renderer")
	}
}
