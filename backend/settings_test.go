package backend_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestGlobalSettingsAndProviderControlRollBackTogether(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			driver, dsn := "sqlite", path
			if databaseURL != "" {
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			fault := `CREATE TRIGGER settings_fault BEFORE INSERT ON entities WHEN NEW.kind='audit' AND json_extract(NEW.data,'$.action')='ai.configured' BEGIN SELECT RAISE(ABORT,'synthetic-private-fault'); END`
			if engine == "postgres" {
				fault = `CREATE FUNCTION settings_fault_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='audit' AND NEW.data->>'action'='ai.configured' THEN RAISE EXCEPTION 'synthetic-private-fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER settings_fault BEFORE INSERT ON entities FOR EACH ROW EXECUTE FUNCTION settings_fault_fn()`
			}
			if _, err = db.Exec(fault); err != nil {
				t.Fatal(err)
			}
			status, result := curationRequest(t, origin, "PATCH", "/settings", map[string]any{"expectedVersion": 0, "settings": map[string]any{"aiEnabled": true}})
			raw, _ := json.Marshal(result)
			if status != 503 || strings.Contains(string(raw), "synthetic-private-fault") {
				t.Fatal("coupled settings failure not safe")
			}
			status, result = curationRequest(t, origin, "GET", "/settings", nil)
			if status != 200 || result["settings"].(map[string]any)["version"] != float64(0) || result["settings"].(map[string]any)["aiEnabled"] != false {
				t.Fatal("global update partially committed")
			}
			status, result = curationRequest(t, origin, "GET", "/ai/settings", nil)
			if status != 200 || result["version"] != float64(0) || result["enabled"] != false {
				t.Fatal("provider control partially committed")
			}
			status, result = curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 || len(result["data"].(map[string]any)["audit"].([]any)) != 0 {
				t.Fatal("failed coupling left an audit")
			}
		})
	}
}

func TestGlobalSettingsUseLiveAIStateAndQuotedVersionHeaders(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	origin := startServer(t, path, databaseURL)
	status, _ := curationRequest(t, origin, "PATCH", "/settings", map[string]any{"expectedVersion": 0, "settings": map[string]any{"aiEnabled": true}})
	if status != 200 {
		t.Fatal("global fixture failed")
	}
	status, _ = curationRequest(t, origin, "PATCH", "/ai/settings", map[string]any{"expectedVersion": 1, "enabled": false})
	if status != 200 {
		t.Fatal("direct provider update failed")
	}
	status, result := curationRequest(t, origin, "GET", "/settings", nil)
	if status != 200 || result["settings"].(map[string]any)["aiEnabled"] != false {
		t.Fatal("global state contradicted live provider settings")
	}
	request, _ := http.NewRequest("PATCH", origin+"/api/v1/settings", strings.NewReader(`{"settings":{}}`))
	request.Header.Set("Authorization", "Bearer "+restoredToken)
	request.Header.Set("If-Match", `"1"`)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("quoted global version header unsupported: %d", response.StatusCode)
	}
}

func TestGlobalSettingsControlAIAndOnlyNewNormalProjectDefaults(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			for _, phase := range []string{"initial", "restart"} {
				t.Run(phase, func(t *testing.T) {
					origin := startServer(t, path, databaseURL)
					status, response := curationRequest(t, origin, "GET", "/settings", nil)
					if status != 200 {
						t.Fatal("global settings unavailable")
					}
					settings := response["settings"].(map[string]any)
					if phase == "restart" {
						if settings["version"] != float64(2) || settings["aiEnabled"] != true {
							t.Fatal("global settings not retained")
						}
						return
					}
					if settings["version"] != float64(0) || settings["aiEnabled"] != false {
						t.Fatal("initial settings changed")
					}
					status, old := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Existing normal"}, restoredToken)
					if status != 200 {
						t.Fatal("project fixture failed")
					}
					previous := old["project"].(map[string]any)
					defaults := map[string]any{"enabledRecordTypes": []any{"finding"}, "repoMirroring": map[string]any{"enabled": true, "recordTypes": []any{"finding"}}, "crossProjectAnalysis": false, "aiProcessing": true}
					status, response = curationRequest(t, origin, "PATCH", "/settings", map[string]any{"expectedVersion": 0, "settings": map[string]any{"aiEnabled": true, "defaultProjectSettings": defaults}})
					if status != 200 || response["settings"].(map[string]any)["version"] != float64(1) {
						t.Fatal("global settings mutation failed")
					}
					status, ai := curationRequest(t, origin, "GET", "/ai/settings", nil)
					if status != 200 || ai["enabled"] != true || ai["version"] != float64(1) {
						t.Fatal("global AI control did not update provider configuration")
					}
					status, current := projectRequest(t, origin, "GET", "/"+previous["id"].(string), nil, restoredToken)
					if status != 200 || !reflect.DeepEqual(previous["settings"], current["project"].(map[string]any)["settings"]) {
						t.Fatal("new defaults rewrote existing project consent")
					}
					for _, kind := range []string{"normal", "external"} {
						status, created := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "New " + kind, "kind": kind}, restoredToken)
						if status != 200 {
							t.Fatal("new project failed")
						}
						value := created["project"].(map[string]any)["settings"].(map[string]any)
						if kind == "normal" {
							if !reflect.DeepEqual(value, defaults) {
								t.Fatal("new normal project did not inherit defaults")
							}
						} else if value["aiProcessing"] != false || value["crossProjectAnalysis"] != false || value["repoMirroring"].(map[string]any)["enabled"] != false {
							t.Fatal("external project inherited internal consent")
						}
					}
					status, _ = curationRequest(t, origin, "PATCH", "/settings", map[string]any{"expectedVersion": 1, "settings": map[string]any{}})
					if status != 200 {
						t.Fatal("second settings version failed")
					}
					status, _ = curationRequest(t, origin, "PATCH", "/settings", map[string]any{"expectedVersion": 1, "settings": map[string]any{"aiEnabled": false}})
					if status != 409 {
						t.Fatal("stale global update accepted")
					}
					status, archive := curationRequest(t, origin, "POST", "/export", map[string]any{})
					if status != 200 {
						t.Fatal("audit export unavailable")
					}
					raw, _ := json.Marshal(archive)
					if !strings.Contains(string(raw), "settings.updated") || !strings.Contains(string(raw), "ai.configured") {
						t.Fatal("coupled settings updates were not audited")
					}
				})
			}
		})
	}
}
