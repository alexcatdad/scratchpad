package backend_test

import (
	"reflect"
	"testing"
)

func TestImportedEntityExtensionsSurviveTypedProjectAndProfileChanges(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			status, archive := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("native archive unavailable")
			}
			data := archive["data"].(map[string]any)
			for kind := range data {
				data[kind] = []any{}
			}
			extension := map[string]any{"nested": map[string]any{"retained": true, "nullable": nil}, "values": []any{"historical", float64(7)}}
			data["project"] = []any{map[string]any{
				"id": "proj_domain_import", "version": 7, "createdAt": "2026-10-01T00:00:00.000Z",
				"name": "Imported project", "slug": "imported-project", "kind": "normal", "historicalExtension": extension,
				"settings": map[string]any{"enabledRecordTypes": []string{"finding"}, "repoMirroring": map[string]any{"enabled": true, "recordTypes": []string{"finding"}}, "crossProjectAnalysis": true, "aiProcessing": true},
			}}
			data["profile"] = []any{map[string]any{
				"id": "owner-profile", "version": 7, "createdAt": "2026-10-01T00:00:00.000Z",
				"displayName": "Imported owner", "historicalExtension": extension,
			}}
			status, _ = curationRequest(t, origin, "POST", "/import", archive)
			if status != 200 {
				t.Fatal("historical extension import failed")
			}
			status, response := projectRequest(t, origin, "PATCH", "/proj_domain_import", map[string]any{"expectedVersion": 7, "name": "Renamed project", "kind": "external"}, restoredToken)
			if status != 200 {
				t.Fatal("versioned imported project update failed")
			}
			project := response["project"].(map[string]any)
			if project["version"] != float64(8) || project["name"] != "Renamed project" || !reflect.DeepEqual(project["historicalExtension"], extension) {
				t.Fatal("project change discarded imported extension or version semantics")
			}
			settings := project["settings"].(map[string]any)
			if settings["repoMirroring"].(map[string]any)["enabled"] != false || settings["crossProjectAnalysis"] != false || settings["aiProcessing"] != true {
				t.Fatal("external transition changed local consent or retained sharing")
			}
			status, response = curationRequest(t, origin, "PATCH", "/profile", map[string]any{"expectedVersion": 7, "displayName": "Renamed owner"})
			if status != 200 {
				t.Fatal("versioned imported profile update failed")
			}
			profile := response["profile"].(map[string]any)
			if profile["version"] != float64(8) || profile["displayName"] != "Renamed owner" || !reflect.DeepEqual(profile["historicalExtension"], extension) {
				t.Fatal("profile change discarded imported extension or version semantics")
			}
			status, response = curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("updated knowledge export failed")
			}
			for _, kind := range []string{"project", "profile"} {
				found := false
				for _, row := range response["data"].(map[string]any)[kind].([]any) {
					if reflect.DeepEqual(row.(map[string]any)["historicalExtension"], extension) {
						found = true
					}
				}
				if !found {
					t.Fatalf("native export lost imported %s extension", kind)
				}
			}
		})
	}
}
