package backend_test

import (
	"database/sql"
	"os"
	"reflect"
	"testing"
)

func TestRAGLateExtensionInstallationWithDisabledProvider(t *testing.T) {
	if os.Getenv("TEST_PGVECTOR_URL") == "" {
		t.Skip("TEST_PGVECTOR_URL is required")
	}
	t.Setenv("TEST_POSTGRES_URL", os.Getenv("TEST_PGVECTOR_URL"))
	t.Setenv("SCRATCHPAD_RAG_ENABLED", "true")
	for _, first := range []string{"coverage", "retrieval"} {
		t.Run(first, func(t *testing.T) {
			path, databaseURL := portableInstance(t, "postgres")
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0, "embeddingDimensions": 2})
			if status != 200 {
				t.Fatal("cannot disable provider")
			}
			project := jobProject(t, origin, "Late extension", true, false)
			record := curationCapture(t, origin, project, "LATE_EXTENSION_WITNESS")
			checkRetrieval := func(coverage string) {
				status, result := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": project, "query": "LATE_EXTENSION_WITNESS"})
				if status != 200 {
					t.Fatalf("lexical fallback failed: HTTP%d %v", status, result)
				}
				semantic := result["semantic"].(map[string]any)
				if semantic["available"] != false || semantic["indexCoverage"] != coverage || len(result["results"].([]any)) != 1 {
					t.Fatalf("incorrect fallback evidence/coverage: %v", result)
				}
				reason := "AI_DISABLED"
				if coverage == "unavailable" {
					reason = "RAG_UNAVAILABLE"
				}
				if semantic["reason"] != reason || result["results"].([]any)[0].(map[string]any)["recordId"] != record["id"] {
					t.Fatal("fallback lost source citation or provider availability reason")
				}
			}
			checkRetrieval("unavailable")
			db, err := sql.Open("pgx", databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.Exec(`CREATE EXTENSION vector`); err != nil {
				t.Fatal(err)
			}
			checkCoverage := func() {
				status, result := curationRequest(t, origin, "GET", "/rag/coverage?projectId="+project, nil)
				if status != 200 || result["status"] != "incomplete" || result["eligibleSources"] != float64(1) || result["indexedSources"] != float64(0) || result["passageCount"] != float64(0) {
					t.Fatalf("late extension must report incomplete coverage: HTTP%d %v", status, result)
				}
			}
			if first == "coverage" {
				checkCoverage()
				checkRetrieval("incomplete")
			} else {
				checkRetrieval("incomplete")
				checkCoverage()
			}
			status, detail := curationRequest(t, origin, "GET", "/records/"+record["id"].(string), nil)
			if status != 200 || !reflect.DeepEqual(detail["record"], record) {
				t.Fatal("optional projection initialization changed immutable evidence")
			}
		})
	}
}
