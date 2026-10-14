package backend_test

import (
	"database/sql"
	"net/http"
	"os"
	"testing"
)

func TestRAGAvailabilityIsAuthenticatedAndDisabledByDefault(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			t.Setenv("SCRATCHPAD_RAG_ENABLED", "")
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			res, err := http.Get(origin + "/api/v1/rag/status")
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != 401 {
				t.Fatalf("RAG availability must require authentication: %d", res.StatusCode)
			}
			status, result := curationRequest(t, origin, "GET", "/rag/status", nil)
			if status != 200 || result["enabled"] != false || result["available"] != false || result["database"] != engine || result["reason"] != "disabled" {
				t.Fatalf("RAG must be explicitly disabled by default: %d %v", status, result)
			}
		})
	}
}

func TestRAGRequiresInstalledPgvectorWithoutBlockingMemory(t *testing.T) {
	if os.Getenv("TEST_PGVECTOR_URL") == "" {
		t.Skip("TEST_PGVECTOR_URL is required for the native pgvector workflow")
	}
	t.Setenv("TEST_POSTGRES_URL", os.Getenv("TEST_PGVECTOR_URL"))
	t.Setenv("SCRATCHPAD_RAG_ENABLED", "true")
	path, databaseURL := portableInstance(t, "postgres")
	origin := startServer(t, path, databaseURL)
	status, result := curationRequest(t, origin, "GET", "/rag/status", nil)
	if status != 200 || result["enabled"] != true || result["available"] != false || result["reason"] != "pgvector_required" {
		t.Fatalf("missing extension must be explicitly reported: %d %v", status, result)
	}
	status, _ = curationRequest(t, origin, "GET", "/projects", nil)
	if status != 200 {
		t.Fatalf("missing extension blocked ordinary memory: %d", status)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE EXTENSION vector`); err != nil {
		t.Fatal(err)
	}
	status, result = curationRequest(t, origin, "GET", "/rag/status", nil)
	if status != 200 || result["available"] != true || result["reason"] != "available" || result["pgvectorVersion"] != "0.8.7" {
		t.Fatalf("installed pgvector must become available: %d %v", status, result)
	}
	if _, err = db.Exec(`DROP EXTENSION vector`); err != nil {
		t.Fatal(err)
	}
	status, result = curationRequest(t, origin, "GET", "/rag/status", nil)
	if status != 200 || result["available"] != false || result["reason"] != "pgvector_required" {
		t.Fatalf("extension removal must invalidate availability: %d %v", status, result)
	}
}

func TestRAGCannotEnableOnSQLiteAndOrdinaryMemoryRemainsAvailable(t *testing.T) {
	t.Setenv("SCRATCHPAD_RAG_ENABLED", "true")
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	status, result := curationRequest(t, origin, "GET", "/rag/status", nil)
	if status != 200 || result["enabled"] != true || result["available"] != false || result["reason"] != "postgres_required" {
		t.Fatalf("SQLite must report unsupported RAG: %d %v", status, result)
	}
	status, _ = curationRequest(t, origin, "GET", "/projects", nil)
	if status != 200 {
		t.Fatalf("unsupported RAG blocked ordinary memory: %d", status)
	}
}
