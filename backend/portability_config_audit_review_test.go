package backend_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNativeExportExcludesConfigurationAuditSnapshotsWithoutRewritingHistory(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			archive := portableArchive()
			data := archive["data"].(map[string]any)
			original := data["audit"].([]any)[0].(map[string]any)
			var events []any
			for i, action := range []string{"ai.configured", "secrets.converted", "historical.configuration"} {
				event := map[string]any{"id": "audit_config_" + action, "version": 1, "createdAt": original["createdAt"], "entityId": original["entityId"], "entityType": "ai_settings", "action": action, "actor": map[string]any{"kind": "system"}, "provenance": map[string]any{"source": "historical"}, "previous": map[string]any{"baseUrl": "https://synthetic-private-provider.invalid/v1", "apiKey": "synthetic-private-provider-token"}, "next": map[string]any{"model": "synthetic-private-model", "sequence": i}}
				events = append(events, event)
			}
			events = append(events, original)
			policy := map[string]any{"id": "global", "version": 1, "createdAt": original["createdAt"], "aiEnabled": false, "apiKey": "synthetic-private-provider-token", "baseUrl": "https://synthetic-private-provider.invalid"}
			events = append(events, map[string]any{"id": "audit_global_policy", "version": 1, "createdAt": original["createdAt"], "entityId": original["entityId"], "entityType": "settings", "action": "settings.updated", "previous": policy, "next": policy})
			data["audit"] = events
			if status, _ := curationRequest(t, origin, "POST", "/import", archive); status != 200 {
				t.Fatal("historical configuration audit fixture rejected")
			}
			status, before := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
			if status != 200 {
				t.Fatal("immutable audit detail unavailable")
			}
			for version := 0; version < 2; version++ {
				status, _ := curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": version, "enabled": false, "scheduleMinutes": 0, "baseUrl": "https://synthetic-private-provider.invalid/v1", "model": "synthetic-private-model"})
				if status != 200 {
					t.Fatal("public configuration audit fixture failed")
				}
			}
			status, exported := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("export unavailable")
			}
			encoded, _ := json.Marshal(exported)
			for _, private := range []string{"synthetic-private-provider", "synthetic-private-model"} {
				if strings.Contains(string(encoded), private) {
					t.Fatal("native knowledge export included provider configuration audit snapshots")
				}
			}
			found := 0
			for _, value := range exported["data"].(map[string]any)["audit"].([]any) {
				event := value.(map[string]any)
				if event["entityType"] == "ai_settings" {
					found++
					if _, ok := event["previous"]; ok {
						t.Fatal("configuration previous snapshot remained portable")
					}
					if _, ok := event["next"]; ok {
						t.Fatal("configuration next snapshot remained portable")
					}
					if event["actor"] == nil || event["action"] == nil || event["id"] == nil {
						t.Fatal("configuration audit identity or provenance lost")
					}
					if strings.HasPrefix(event["id"].(string), "audit_config_") && !reflect.DeepEqual(event["provenance"], map[string]any{"source": "historical"}) {
						t.Fatal("historical configuration provenance lost")
					}
				}
				if event["id"] == "audit_global_policy" {
					for _, key := range []string{"previous", "next"} {
						policy := event[key].(map[string]any)
						if policy["id"] != "global" || policy["aiEnabled"] != false {
							t.Fatal("portable policy audit lost safe fields")
						}
					}
				}
			}
			if found != 5 {
				t.Fatal("configuration audit history omitted")
			}
			status, after := curationRequest(t, origin, "GET", "/records/rec:portable", nil)
			if status != 200 || !reflect.DeepEqual(before["audit"], after["audit"]) {
				t.Fatal("export rewrote persisted immutable audit history")
			}
		})
	}
}
