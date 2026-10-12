package backend_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMalformedHistoricalRouteIDsRemainExportOnly(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			raw := `{"id":"a/b","version":1,"createdAt":"2026-10-01T00:00:00Z","projectId":"proj_historical","type":"finding","title":"Historical malformed identity","authority":"observed","confidence":"high","payload":{"finding":"Keep recovery evidence."},"content":"finding: Keep recovery evidence."}`
			if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('record','a/b',$1,1,'2026-10-01T00:00:00Z')`, raw); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('record','safe:record',$1,1,'2026-10-01T00:00:00Z')`, strings.ReplaceAll(raw, `"a/b"`, `"safe:record"`)); err != nil {
				t.Fatal(err)
			}
			origin := startServer(t, path, databaseURL)
			for _, route := range []struct{ method, path string }{
				{"GET", "/records/a%2Fb"}, {"PATCH", "/records/a%2Fb/metadata"},
				{"POST", "/records/a%2Fb/revisions"}, {"POST", "/records/a%2Fb/evidence"}, {"POST", "/records/a%2Fb/mirror"},
				{"GET", "/projects/a%2Fb"}, {"PATCH", "/projects/a%2Fb"}, {"GET", "/projects/a%2Fb/settings"}, {"PATCH", "/projects/a%2Fb/settings"},
				{"GET", "/projects/a%2Fb/context"}, {"GET", "/projects/a%2Fb/context/compact"},
				{"POST", "/relationships/a%2Fb/accept"}, {"POST", "/relationships/a%2Fb/reject"},
				{"POST", "/ai/jobs/a%2Fb/retry"}, {"POST", "/suggestions/a%2Fb/accept"}, {"POST", "/suggestions/a%2Fb/reject"},
				{"DELETE", "/auth/credentials/a%2Fb"},
			} {
				status, _ := curationRequest(t, origin, route.method, route.path, map[string]any{})
				if status != 400 {
					t.Fatalf("malformed identity reached %s %s: %d", route.method, route.path, status)
				}
			}
			status, exported := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("historical recovery export unavailable")
			}
			records := exported["data"].(map[string]any)["record"].([]any)
			if len(records) != 2 {
				t.Fatal("route rejection changed recoverable history")
			}
			found := false
			for _, record := range records {
				found = found || record.(map[string]any)["id"] == "a/b"
			}
			if !found {
				t.Fatal("malformed historical identity disappeared from export")
			}
			status, detail := curationRequest(t, origin, "GET", "/records/safe%3Arecord", nil)
			if status != 200 || detail["record"].(map[string]any)["id"] != "safe:record" {
				t.Fatal("safe encoded opaque identity became unreachable")
			}
		})
	}
}

func TestNativeImportAllowsSlowAuthenticatedUpload(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	reader, writer := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer writer.Close()
		if _, err := io.WriteString(writer, `{"format":"scratchpad",`); err != nil {
			return
		}
		time.Sleep(17 * time.Second)
		_, _ = io.WriteString(writer, `"version":1,"data":{}}`)
	}()
	defer func() { reader.Close(); <-done }()
	request, _ := http.NewRequest("POST", origin+"/api/v1/import", reader)
	request.Header.Set("Authorization", "Bearer "+restoredToken)
	client := &http.Client{Timeout: 40 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if json.NewDecoder(response.Body).Decode(&result) != nil || response.StatusCode != 200 {
		t.Fatalf("valid slow import rejected: %d", response.StatusCode)
	}
	// Normal API calls remain available after the bounded upload finishes.
	status, _ := curationRequest(t, origin, "POST", "/export", map[string]any{})
	if status != 200 {
		t.Fatal("server unavailable after slow import")
	}
}
