package backend_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRAGNativePgvectorLastDimensionRanksAcrossRestart(t *testing.T) {
	path, databaseURL := ragInstance(t)
	const dimensions = 2560
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		if r.URL.Path != "/v1/embeddings" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Input) == 0 {
			http.Error(w, "invalid embedding request", http.StatusBadRequest)
			return
		}
		data := make([]map[string]any, len(body.Input))
		for index, input := range body.Input {
			vector := make([]float64, dimensions)
			switch {
			case strings.Contains(input, "RETIREMENT_BEST_SOURCE_2559"):
				vector[2559] = 1
			case strings.Contains(input, "RETIREMENT_ORTHOGONAL_SOURCE_0"):
				vector[0] = 1
			default:
				// The query has no lexical overlap with either source and uses
				// the final native pgvector dimension.
				vector[2559] = 1
			}
			data[index] = map[string]any{"index": index, "embedding": vector}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer provider.Close()

	var projectID, bestRecordID, orthogonalRecordID string
	for _, phase := range []string{"index", "restart"} {
		t.Run(phase, func(t *testing.T) {
			origin := startServer(t, path, databaseURL)
			if phase == "index" {
				status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{
					"expectedVersion":     0,
					"enabled":             true,
					"scheduleMinutes":     0,
					"baseUrl":             provider.URL + "/v1",
					"embeddingDimensions": dimensions,
				})
				if status != http.StatusOK {
					t.Fatalf("provider fixture failed: %d", status)
				}
				projectID = jobProject(t, origin, "Native 2560-dimension retrieval", true, false)
				bestStatus, best := captureRequest(t, origin, projectID, "best-vector", map[string]any{
					"type": "finding", "title": "RETIREMENT_BEST_SOURCE_2559", "authority": "observed",
					"payload": map[string]any{"finding": "A source represented on the last vector dimension."},
				})
				orthogonalStatus, orthogonal := captureRequest(t, origin, projectID, "orthogonal-vector", map[string]any{
					"type": "finding", "title": "RETIREMENT_ORTHOGONAL_SOURCE_0", "authority": "observed",
					"payload": map[string]any{"finding": "A source represented on the first vector dimension."},
				})
				if bestStatus != http.StatusCreated || orthogonalStatus != http.StatusCreated {
					t.Fatalf("source capture failed: best=%d orthogonal=%d", bestStatus, orthogonalStatus)
				}
				bestRecordID = best["record"].(map[string]any)["id"].(string)
				orthogonalRecordID = orthogonal["record"].(map[string]any)["id"].(string)
				if bestRecordID == orthogonalRecordID {
					t.Fatal("source fixtures resolved to the same record")
				}
				jobs := waitRAGJobs(t, origin, 2)
				for _, value := range jobs {
					waitAIJob(t, origin, value.(map[string]any)["id"].(string), "completed")
				}
			}

			status, response := curationRequest(t, origin, "POST", "/rag/retrieve", map[string]any{
				"projectId": projectID,
				"query":     "nebular harmonic",
			})
			if status != http.StatusOK {
				t.Fatalf("semantic retrieval failed after %s: %d %v", phase, status, response)
			}
			results := response["results"].([]any)
			if len(results) == 0 {
				t.Fatalf("expected native vector evidence after %s: %v", phase, response)
			}
			if results[0].(map[string]any)["recordId"] != bestRecordID {
				t.Fatalf("last-dimension semantic match did not rank first after %s: %v", phase, results)
			}
		})
	}
}
