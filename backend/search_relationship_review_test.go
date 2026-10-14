package backend_test

import "testing"

func TestSearchUsesDocumentedRelationshipTypeAndRetainsLegacyAlias(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			projectID := jobProject(t, origin, "Relationship filters", false, false)
			first := curationCapture(t, origin, projectID, "Related first")
			second := curationCapture(t, origin, projectID, "Related second")
			third := curationCapture(t, origin, projectID, "Contradictory third")
			curationCapture(t, origin, projectID, "Unrelated fourth")
			for _, edge := range []struct {
				target map[string]any
				kind   string
			}{{second, "related_to"}, {third, "contradicts"}} {
				status, _ := curationRequest(t, origin, "POST", "/relationships", map[string]any{"fromRecordId": first["id"], "toRecordId": edge.target["id"], "type": edge.kind, "authority": "explicit"})
				if status != 201 {
					t.Fatal("relationship fixture failed")
				}
			}
			for _, query := range []string{"relationshipType=related_to", "relationship=related_to", "relationshipType=related_to&relationship=contradicts"} {
				status, response := searchRecords(t, origin, "projectId="+projectID+"&"+query)
				if status != 200 || len(response["records"].([]any)) != 2 {
					t.Fatalf("documented relationship filter ignored: %s: %d %v", query, status, response)
				}
				seen := map[any]bool{}
				for _, record := range response["records"].([]any) {
					seen[record.(map[string]any)["id"]] = true
				}
				if !seen[first["id"]] || !seen[second["id"]] {
					t.Fatalf("relationship filter returned other endpoints: %s: %v", query, response)
				}
			}
		})
	}
}
