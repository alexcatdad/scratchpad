package backend_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func generatorArchive(id string, generator map[string]any) map[string]any {
	archive := portableArchive()
	archive["data"].(map[string]any)["ai_artifact"] = []any{map[string]any{"id": id, "version": 1, "createdAt": "2026-10-01T00:00:00Z", "authority": "derived", "kind": "summary", "content": map[string]any{"text": "Retain source-backed historical interpretation"}, "generator": generator, "sourceRecordIds": []any{"rec:portable"}, "projectIds": []any{"proj_portable"}, "status": "pending"}}
	return archive
}

func TestGeneratedDocumentGeneratorExportsAndReplaysAtModelBoundary(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			provider := completionService(t, func(input map[string]any) any {
				id := input["records"].([]any)[0].(map[string]any)["id"]
				return map[string]any{"artifacts": []any{map[string]any{"kind": "export", "title": "Private generated draft", "content": map[string]any{"text": "Source-supported draft", "markdown": "## Synthetic document\n\nRetain the cited source."}, "sourceRecordIds": []any{id}}}}
			})
			defer provider.Close()
			prefix := strings.Repeat("/synthetic", 250)
			routed := httptest.NewServer(http.StripPrefix(prefix, provider.Config.Handler))
			defer routed.Close()
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			baseURL, model := routed.URL+prefix+"/v1", strings.Repeat("🙂", 100)
			status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": true, "scheduleMinutes": 0, "baseUrl": baseURL, "model": model, "analysisModes": []any{}})
			if status != 200 {
				t.Fatal("valid producer settings rejected")
			}
			project := jobProject(t, origin, "Portable generated provenance", true, false)
			curationCapture(t, origin, project, "Keep exact generated model and provider provenance")
			status, result := curationRequest(t, origin, "POST", "/summaries/export", map[string]any{"projectId": project, "format": "handoff"})
			if status != 202 {
				t.Fatal("document enqueue failed")
			}
			waitAIJob(t, origin, result["job"].(map[string]any)["id"].(string), "completed")
			status, suggestions := curationRequest(t, origin, "GET", "/suggestions?projectId="+project, nil)
			if status != 200 || len(suggestions["suggestions"].([]any)) != 1 {
				t.Fatal("generated document unavailable")
			}
			artifact := suggestions["suggestions"].([]any)[0].(map[string]any)
			generator := artifact["generator"].(map[string]any)
			if generator["model"] != model || generator["provider"] != baseURL || len(generator["version"].(string)) != 64 {
				t.Fatal("producer generator changed or lost provenance")
			}
			status, archive := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("generated document archive unavailable")
			}
			status, replay := curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 {
				t.Fatalf("generated export incompatible with own importer: HTTP %d %+v", status, replay)
			}
			status, retained := curationRequest(t, origin, "GET", "/suggestions?projectId="+project, nil)
			if status != 200 || len(retained["suggestions"].([]any)) != 1 || !reflect.DeepEqual(retained["suggestions"].([]any)[0], artifact) {
				t.Fatal("generator replay duplicated or rewrote generated document")
			}
		})
	}
}

func TestNativeArtifactGeneratorRejectsInvalidPresentationAtomically(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			_, before := curationRequest(t, origin, "POST", "/export", map[string]any{})
			for _, check := range []struct {
				field string
				value any
			}{
				{"model", map[string]any{"unsafe": "React child"}}, {"model", []any{"nested"}}, {"model", true}, {"model", 42}, {"model", nil}, {"model", strings.Repeat("🙂", 101)},
				{"provider", map[string]any{}}, {"version", []any{"invalid"}},
			} {
				status, result := curationRequest(t, origin, "POST", "/import", generatorArchive("artifact_invalid_generator", map[string]any{check.field: check.value}))
				if status != 400 || result["error"].(map[string]any)["code"] != "AI_ARTIFACT_INVALID" {
					t.Fatalf("unsafe generator.%s accepted: HTTP %d %+v", check.field, status, result)
				}
				_, after := curationRequest(t, origin, "POST", "/export", map[string]any{})
				if !reflect.DeepEqual(before["data"], after["data"]) {
					t.Fatal("invalid generator partially imported source or artifact history")
				}
			}
			for i, generator := range []map[string]any{{}, {"model": "", "extension": map[string]any{"historical": []any{float64(7), true}}}, {"model": strings.Repeat("🙂", 100), "provider": strings.Repeat("p", 3000), "version": strings.Repeat("v", 700)}} {
				id := []string{"artifact_omitted_generator", "artifact_extended_generator", "artifact_long_provenance"}[i]
				archive := generatorArchive(id, generator)
				status, result := curationRequest(t, origin, "POST", "/import", archive)
				if status != 200 {
					t.Fatalf("valid optional historical generator rejected: HTTP %d %+v", status, result)
				}
				status, exported := curationRequest(t, origin, "POST", "/export", map[string]any{})
				if status != 200 {
					t.Fatal("export failed")
				}
				found := false
				for _, item := range exported["data"].(map[string]any)["ai_artifact"].([]any) {
					artifact := item.(map[string]any)
					if artifact["id"] == id {
						found = true
						if !reflect.DeepEqual(artifact["generator"], generator) {
							t.Fatal("generator extension/provenance rewritten")
						}
					}
				}
				if !found {
					t.Fatal("historical generator lost on export")
				}
				status, _ = curationRequest(t, origin, "POST", "/import", exported)
				if status != 200 {
					t.Fatal("exported generator cannot replay")
				}
			}
		})
	}
}
