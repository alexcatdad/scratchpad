package backend_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestPassageDimensionAvailabilityCannotRollbackOrdinaryMemory(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		for _, dimensions := range []int{16001, 16384} {
			t.Run(fmt.Sprintf("%s/%d", engine, dimensions), func(t *testing.T) {
				t.Setenv("SCRATCHPAD_RAG_ENABLED", "true")
				var path, databaseURL string
				if engine == "postgres" {
					path, databaseURL = ragInstance(t)
				} else {
					path, databaseURL = portableInstance(t, engine)
				}
				origin := startServer(t, path, databaseURL)
				status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0, "embeddingDimensions": dimensions})
				if status != 200 {
					t.Fatal("valid legacy dimensions were rejected")
				}
				project := jobProject(t, origin, "Unsupported passage dimension capture", true, false)
				input := map[string]any{"type": "finding", "title": "DIMENSION_MEMORY_WITNESS", "authority": "observed", "payload": map[string]any{"finding": "Retain ordinary immutable evidence when optional passage dimensions are unavailable."}}
				status, captured := captureRequest(t, origin, project, "dimension-retry", input)
				if status != 201 {
					t.Fatalf("optional passage configuration rolled back ordinary capture: HTTP%d", status)
				}
				status, replayed := captureRequest(t, origin, project, "dimension-retry", input)
				if status != 201 || !reflect.DeepEqual(captured, replayed) {
					t.Fatal("optional passage dimensions broke durable capture retry")
				}
				record := captured["record"].(map[string]any)
				id := record["id"].(string)
				status, detail := curationRequest(t, origin, "GET", "/records/"+id, nil)
				if status != 200 || !reflect.DeepEqual(detail["record"], record) || detail["metadata"].(map[string]any)["archived"] != false || len(detail["audit"].([]any)) != 1 {
					t.Fatal("ordinary capture lost raw evidence, metadata or one audit")
				}
				status, listing := searchRecords(t, origin, "q=DIMENSION_MEMORY_WITNESS&projectId="+project)
				if status != 200 || len(listing["records"].([]any)) != 1 {
					t.Fatal("capture lost its lexical projection")
				}
				// Reconciliation has an opportunity to run; neither capture nor backfill may
				// create a passage job that the configured derivative cannot execute.
				time.Sleep(1100 * time.Millisecond)
				status, jobs := curationRequest(t, origin, "GET", "/ai/jobs", nil)
				if status != 200 || len(jobs["jobs"].([]any)) != 0 {
					t.Fatal("unsupported passage configuration queued unusable derivative work")
				}
				status, availability := curationRequest(t, origin, "GET", "/rag/status", nil)
				if status != 200 || availability["available"] != false {
					t.Fatal("unsupported passage dimensions claimed derivative availability")
				}
				if engine == "sqlite" {
					if availability["reason"] != "postgres_required" {
						t.Fatal("SQLite memory independence changed")
					}
					return
				}
				if availability["reason"] != "embedding_dimensions_unsupported" {
					t.Fatal("passage status did not identify incompatible dimensions")
				}
				status, coverage := curationRequest(t, origin, "GET", "/rag/coverage?projectId="+project, nil)
				if status != 409 || coverage["error"].(map[string]any)["code"] != "RAG_UNAVAILABLE" {
					t.Fatal("unsupported derivative coverage was reported as complete or invalid ordinary configuration")
				}
				status, queued := curationRequest(t, origin, "POST", "/rag/index", map[string]any{"projectId": project, "recordId": id})
				if status != 409 || queued["error"].(map[string]any)["code"] != "RAG_UNAVAILABLE" {
					t.Fatal("explicit index admitted unusable passage dimensions")
				}
				status, retrieval := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{"projectId": project, "query": "DIMENSION_MEMORY_WITNESS"})
				if status != 200 || len(retrieval["results"].([]any)) != 1 || retrieval["semantic"].(map[string]any)["indexCoverage"] != "unavailable" {
					t.Fatal("optional derivative configuration hid cited lexical evidence or uncertainty")
				}
				// Compatible configuration can reconcile the preserved source afterwards.
				status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "embeddingDimensions": 16000})
				if status != 200 {
					t.Fatal("compatible dimension update failed")
				}
				waitRAGJobs(t, origin, 1)
				status, availability = curationRequest(t, origin, "GET", "/rag/status", nil)
				if status != 200 || availability["available"] != true {
					t.Fatal("maximum supported passage dimensions remained unavailable")
				}
				status, detail = curationRequest(t, origin, "GET", "/records/"+id, nil)
				if status != 200 || !reflect.DeepEqual(detail["record"], record) {
					t.Fatal("derivative recovery rewrote original knowledge")
				}
			})
		}
	}
}

func TestLegacySemanticDimensionsRemainUsableWhenPassageRAGIsUnavailable(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			t.Setenv("SCRATCHPAD_RAG_ENABLED", "true")
			var path, databaseURL string
			if engine == "postgres" {
				path, databaseURL = ragInstance(t)
			} else {
				path, databaseURL = portableInstance(t, engine)
			}
			vector := make([]float64, 16384)
			vector[0] = 1
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": vector}}})
			}))
			defer provider.Close()
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "embeddingDimensions": 16384})
			if status != 200 {
				t.Fatal("legacy model dimension configuration failed")
			}
			project := jobProject(t, origin, "Legacy wide vectors", true, false)
			record := curationCapture(t, origin, project, "Legacy high-dimensional source")
			status, queued := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "embed", "projectId": project})
			if status != 202 {
				t.Fatal("legacy embedding enqueue was blocked by passage dimensions")
			}
			completed := waitAIJob(t, origin, queued["job"].(map[string]any)["id"].(string), "completed")
			if completed["sourceCount"] != float64(1) {
				t.Fatal("legacy embedding lost the preserved source")
			}
			status, response := curationRequest(t, origin, "POST", "/search/semantic", map[string]any{"projectId": project, "query": "preserved wide-vector evidence"})
			if status != 200 || len(response["results"].([]any)) != 1 || response["results"].([]any)[0].(map[string]any)["record"].(map[string]any)["id"] != record["id"] {
				t.Fatal("optional passage dimension limit disabled existing semantic search")
			}
			status, jobs := curationRequest(t, origin, "GET", "/ai/jobs", nil)
			if status != 200 || len(jobs["jobs"].([]any)) != 1 {
				t.Fatal("unsupported passage generation contaminated the legacy worker queue")
			}
		})
	}
}
