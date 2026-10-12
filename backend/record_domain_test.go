package backend_test

import (
	"reflect"
	"testing"
)

func TestCaptureRetainsFlexiblePayloadAndOptionalContext(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			status, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Typed capture"}, restoredToken)
			if status != 200 {
				t.Fatal("project resolution failed")
			}
			id := project["project"].(map[string]any)["id"].(string)
			payload := map[string]any{"finding": "Retain flexible evidence.", "custom": map[string]any{"null": nil, "values": []any{float64(1), true, "evidence"}}}
			at := "2026-10-12T10:00:00.123456789Z"
			body := map[string]any{"projectId": id, "gitContext": map[string]any{}, "record": map[string]any{"type": "finding", "title": "Typed capture", "authority": "observed", "happenedAt": at, "payload": payload, "actor": map[string]any{"kind": "user", "client": "synthetic-client"}}}
			status, saved := curationRequest(t, origin, "POST", "/records", body)
			if status != 201 {
				t.Fatalf("capture failed: %d", status)
			}
			record := saved["record"].(map[string]any)
			context, present := record["gitContext"].(map[string]any)
			if !present || len(context) != 0 || record["happenedAt"] != at || record["confidence"] != "unknown" || !reflect.DeepEqual(record["payload"], payload) {
				t.Fatal("capture changed optional context, timestamp, defaults or flexible payload")
			}
			actor := record["actor"].(map[string]any)
			if actor["kind"] != "agent" || actor["client"] != "synthetic-client" || actor["credentialFingerprint"] != "credential_restored" {
				t.Fatal("capture lost submitted client or authoritative actor provenance")
			}
			status, detail := curationRequest(t, origin, "GET", "/records/"+record["id"].(string), nil)
			if status != 200 || !reflect.DeepEqual(detail["record"], record) {
				t.Fatal("capture did not persist its exact public fields")
			}
		})
	}
}
