package backend_test

import (
	"net/http"
	"testing"
)

func TestAboutReportsBuildOnlyToAuthenticatedClients(t *testing.T) {
	path, _, _ := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	res, err := http.Get(origin + "/api/v1/about")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("anonymous about returned %d", res.StatusCode)
	}
	status, body := curationRequest(t, origin, "GET", "/about", nil)
	if status != 200 {
		t.Fatalf("about unavailable: %d", status)
	}
	if body["version"] != "dev" || body["commit"] != "" || body["database"] != "sqlite" || body["githubConfigured"] != false {
		t.Fatalf("unexpected about body: %v", body)
	}
	if version, ok := body["schemaVersion"].(float64); !ok || version < 1 {
		t.Fatalf("schema version missing: %v", body["schemaVersion"])
	}
}
