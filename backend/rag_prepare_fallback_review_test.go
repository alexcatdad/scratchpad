package backend_test

import (
	"database/sql"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestOptionalRAGPreparationFailurePreservesLexicalEvidenceAndCoreErrors(t *testing.T) {
	if os.Getenv("TEST_PGVECTOR_URL") == "" {
		t.Skip("TEST_PGVECTOR_URL is required")
	}
	t.Setenv("TEST_POSTGRES_URL", os.Getenv("TEST_PGVECTOR_URL"))
	t.Setenv("SCRATCHPAD_RAG_ENABLED", "true")
	path, databaseURL := portableInstance(t, "postgres")
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0, "embeddingDimensions": 2})
	if status != 200 {
		t.Fatal("disabled provider fixture failed")
	}
	project := jobProject(t, origin, "Optional projection outage", true, false)
	record := curationCapture(t, origin, project, "OPTIONAL_PROJECTION_WITNESS")
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Install the extension after startup, with a malformed optional migration
	// ledger. Core sources, authentication and lexical projection remain healthy.
	if _, err = db.Exec(`CREATE EXTENSION vector; CREATE TABLE rag_projection_migrations(private_fixture_marker TEXT)`); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"projectId": project, "query": "OPTIONAL_PROJECTION_WITNESS"}
	status, result := curationRequest(t, origin, "POST", "/rag/retrieve", request)
	if status != 200 {
		t.Fatalf("optional preparation discarded lexical evidence: HTTP %d %+v", status, result)
	}
	semantic := result["semantic"].(map[string]any)
	if semantic["available"] != false || semantic["reason"] != "RAG_UNAVAILABLE" || semantic["indexCoverage"] != "unavailable" || len(result["results"].([]any)) != 1 || result["results"].([]any)[0].(map[string]any)["recordId"] != record["id"] {
		t.Fatalf("optional outage misrepresented evidence: %+v", result)
	}
	status, detail := curationRequest(t, origin, "GET", "/records/"+record["id"].(string), nil)
	if status != 200 || !reflect.DeepEqual(detail["record"], record) {
		t.Fatal("optional preparation altered the immutable source")
	}
	denied := jobProject(t, origin, "Private excluded project", false, false)
	status, _ = curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": denied, "query": "OPTIONAL_PROJECTION_WITNESS"})
	if status != 403 {
		t.Fatalf("optional outage bypassed consent: HTTP %d", status)
	}
	if _, err = db.Exec(`DROP TABLE record_search`); err != nil {
		t.Fatal(err)
	}
	status, result = curationRequest(t, origin, "POST", "/rag/retrieve", request)
	if status != 503 || result["results"] != nil || strings.Contains(result["error"].(map[string]any)["message"].(string), "private_fixture_marker") {
		t.Fatalf("core failure became successful evidence or exposed projection internals: HTTP %d %+v", status, result)
	}
}
