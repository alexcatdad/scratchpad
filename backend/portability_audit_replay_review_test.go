package backend_test

import (
	"reflect"
	"testing"
)

func TestNativeConfiguredExportReplaysWithoutChangingPrivateAuditHistory(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			archive := portableArchive()
			audit := archive["data"].(map[string]any)["audit"].([]any)[0].(map[string]any)
			audit["entityType"], audit["action"] = "ai_settings", "ai.configured"
			audit["next"] = map[string]any{"baseUrl": "https://synthetic-provider.invalid/v1", "apiKey": "synthetic-private-key"}
			archive["data"].(map[string]any)["audit"] = []any{audit, map[string]any{"id": "audit_settings_replay", "version": 1, "createdAt": audit["createdAt"], "entityId": audit["entityId"], "entityType": "settings", "action": "settings.updated", "next": map[string]any{"id": "global", "aiEnabled": false, "apiKey": "synthetic-private-key"}}}
			if status, _ := curationRequest(t, origin, "POST", "/import", archive); status != 200 {
				t.Fatal("historical fixture import failed")
			}
			if status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 0, "enabled": false, "scheduleMinutes": 0, "baseUrl": "https://synthetic-provider.invalid/v1"}); status != 200 {
				t.Fatal("configuration fixture failed")
			}
			_, before := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
			status, exported := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("configured export failed")
			}
			status, result := curationRequest(t, origin, "POST", "/import", exported)
			if status != 200 || result["imported"] != float64(0) || result["skipped"].(float64) < 1 {
				t.Fatalf("configured export did not replay: HTTP%d", status)
			}
			status, after := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
			if status != 200 || !reflect.DeepEqual(before["audit"], after["audit"]) {
				t.Fatal("replay replaced private immutable audit snapshots")
			}
			freshPath, freshURL := portableInstance(t, engine)
			fresh := startServer(t, freshPath, freshURL)
			status, result = curationRequest(t, fresh, "POST", "/import", exported)
			if status != 200 || result["imported"].(float64) < 1 {
				t.Fatal("redacted audit archive cannot import into fresh instance")
			}
			_, baseline := curationRequest(t, origin, "POST", "/export", map[string]any{})
			audit["next"].(map[string]any)["apiKey"] = "synthetic-different-private-key"
			status, result = curationRequest(t, origin, "POST", "/import", archive)
			if status != 409 || result["error"].(map[string]any)["code"] != "CONFLICT" {
				t.Fatal("projection hid a changed unredacted audit snapshot")
			}
			for _, event := range exported["data"].(map[string]any)["audit"].([]any) {
				if event.(map[string]any)["id"] == audit["id"] {
					event.(map[string]any)["actor"] = map[string]any{"kind": "changed"}
				}
			}
			status, result = curationRequest(t, origin, "POST", "/import", exported)
			if status != 409 || result["error"].(map[string]any)["code"] != "CONFLICT" {
				t.Fatal("redaction hid a changed portable audit field")
			}
			_, unchanged := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if !reflect.DeepEqual(baseline["data"], unchanged["data"]) {
				t.Fatal("audit conflict partially changed imported history")
			}
		})
	}
}
