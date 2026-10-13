package backend_test

import (
	"reflect"
	"sync/atomic"
	"testing"
)

func TestAnalysisWithoutSupportedSuggestionsCompletesWithWarningsOnce(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		for _, mode := range []string{"insufficient", "empty"} {
			t.Run(engine+"/"+mode, func(t *testing.T) {
				var calls atomic.Int32
				provider := completionService(t, func(input map[string]any) any {
					calls.Add(1)
					artifacts := []any{}
					if mode == "insufficient" {
						id := input["records"].([]any)[0].(map[string]any)["id"]
						for _, kind := range []string{"pattern", "pattern", "duplicate_candidate"} {
							artifacts = append(artifacts, map[string]any{"kind": kind, "title": "Unsupported single-source interpretation", "content": map[string]any{"text": "Only one independent source supports this candidate."}, "sourceRecordIds": []any{id}})
						}
					}
					return map[string]any{"artifacts": artifacts}
				})
				defer provider.Close()
				path, databaseURL := portableInstance(t, engine)
				origin := startServer(t, path, databaseURL)
				status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1", "analysisModes": []any{"pattern", "duplicate_candidate"}})
				if status != 200 {
					t.Fatal("provider configuration failed")
				}
				project := jobProject(t, origin, "No supported interpretation", true, false)
				record := curationCapture(t, origin, project, "Retain one explicit source without inventing independent corroboration")
				status, result := curationRequest(t, origin, "POST", "/ai/jobs", map[string]any{"type": "analyze", "projectId": project})
				if status != 202 {
					t.Fatal("analysis enqueue failed")
				}
				job := waitAIJob(t, origin, result["job"].(map[string]any)["id"].(string), "completed")
				warnings := []any{}
				if mode == "insufficient" {
					warnings = []any{map[string]any{"code": "AI_INSUFFICIENT_EVIDENCE", "kind": "pattern", "count": float64(2)}, map[string]any{"code": "AI_INSUFFICIENT_EVIDENCE", "kind": "duplicate_candidate", "count": float64(1)}}
				}
				if job["attempts"] != float64(1) || job["sourceCount"] != float64(1) || len(job["artifactIds"].([]any)) != 0 || !reflect.DeepEqual(job["warnings"], warnings) || job["lastError"] != nil || calls.Load() != 1 {
					t.Fatalf("valid empty analysis retried or lost warnings: %+v calls=%d", job, calls.Load())
				}
				status, suggestions := curationRequest(t, origin, "GET", "/suggestions?projectId="+project, nil)
				if status != 200 || len(suggestions["suggestions"].([]any)) != 0 {
					t.Fatal("unsupported interpretation became a suggestion")
				}
				status, detail := curationRequest(t, origin, "GET", "/records/"+record["id"].(string), nil)
				if status != 200 || !reflect.DeepEqual(detail["record"], record) {
					t.Fatal("analysis rewrote immutable source evidence")
				}
				if mode == "insufficient" {
					_, archive := curationRequest(t, origin, "POST", "/export", map[string]any{})
					found := false
					for _, item := range archive["data"].(map[string]any)["audit"].([]any) {
						audit := item.(map[string]any)
						if audit["action"] == "ai.output.rejected" && audit["entityId"] == job["id"] && reflect.DeepEqual(audit["next"].(map[string]any)["warnings"], warnings) {
							found = true
						}
					}
					if !found {
						t.Fatal("insufficient-evidence warning audit missing")
					}
				}
			})
		}
	}
}

func TestRequestedDocumentRejectsEmptyProviderArtifactResponse(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var calls atomic.Int32
			provider := completionService(t, func(input map[string]any) any { calls.Add(1); return map[string]any{"artifacts": []any{}} })
			defer provider.Close()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": provider.URL + "/v1"})
			if status != 200 {
				t.Fatal("provider configuration failed")
			}
			project := jobProject(t, origin, "Requested nonempty document", true, false)
			curationCapture(t, origin, project, "A requested document must not complete without a document artifact")
			status, result := curationRequest(t, origin, "POST", "/summaries/export", map[string]any{"projectId": project, "format": "handoff"})
			if status != 202 {
				t.Fatal("document enqueue failed")
			}
			job := waitAIJob(t, origin, result["job"].(map[string]any)["id"].(string), "failed")
			if job["attempts"] != float64(3) || job["lastError"] != "AI provider is unavailable or returned invalid output." || calls.Load() != 3 {
				t.Fatalf("empty document provider response became successful analysis: %+v calls=%d", job, calls.Load())
			}
			status, result = curationRequest(t, origin, "GET", "/suggestions?projectId="+project, nil)
			if status != 200 || len(result["suggestions"].([]any)) != 0 {
				t.Fatal("empty document response created an artifact")
			}
		})
	}
}
