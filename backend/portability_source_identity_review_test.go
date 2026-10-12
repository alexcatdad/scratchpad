package backend_test

import (
	"reflect"
	"strings"
	"testing"
)

func TestResolvedLongSourceIdentitiesSurviveNativeRoundTrip(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			var projects []string
			paths := []string{strings.Repeat("a", 600) + "/repo", strings.Repeat("界", 80) + "/repo", "owner/" + strings.Repeat("r", 700)}
			if engine == "sqlite" {
				paths = append(paths, "owner/"+strings.Repeat("r", 100001))
			}
			for _, remotePath := range paths {
				status, resolved := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": "https://example.com/" + remotePath + ".git"}}}, restoredToken)
				if status != 200 {
					t.Fatal("supported remote resolution failed")
				}
				projects = append(projects, resolved["project"].(map[string]any)["id"].(string))
			}
			status, exported := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 200 {
				t.Fatal("resolved source export failed")
			}
			status, replay := curationRequest(t, origin, "POST", "/import", exported)
			if status != 200 || replay["imported"] != float64(0) || replay["skipped"].(float64) < 2 {
				t.Fatal("resolved long-source archive did not replay")
			}
			freshPath, freshURL := portableInstance(t, engine)
			fresh := startServer(t, freshPath, freshURL)
			status, _ = curationRequest(t, fresh, "POST", "/import", exported)
			if status != 200 {
				t.Fatalf("own resolved source archive rejected: HTTP%d", status)
			}
			for i, remotePath := range paths {
				status, resolved := projectRequest(t, fresh, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": "git@example.com:" + remotePath + ".git"}}}, restoredToken)
				if status != 200 || resolved["project"].(map[string]any)["id"] != projects[i] {
					t.Fatal("import lost normalized source identity resolution")
				}
			}
			_, restored := curationRequest(t, fresh, "POST", "/export", map[string]any{})
			if !reflect.DeepEqual(exported["data"].(map[string]any)["source"], restored["data"].(map[string]any)["source"]) {
				t.Fatal("native restore rewrote long normalized source identity")
			}
			if !reflect.DeepEqual(exported["data"].(map[string]any)["project"], restored["data"].(map[string]any)["project"]) {
				t.Fatal("native restore changed derived repository names or slugs")
			}
		})
	}
}

func TestNativeSourceIdentityRejectsInvalidValuesAtomically(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			_, before := curationRequest(t, origin, "POST", "/export", map[string]any{})
			for index, invalid := range []any{nil, "", " \t\n", 123, true, []any{"identity"}, map[string]any{"identity": "value"}} {
				archive := portableArchive()
				archive["data"].(map[string]any)["source"].([]any)[0].(map[string]any)["identity"] = invalid
				status, result := curationRequest(t, origin, "POST", "/import", archive)
				if status != 400 || result["error"].(map[string]any)["code"] != "IMPORT_INVALID" {
					t.Fatalf("invalid source identity case%d: HTTP%d %v", index, status, result)
				}
				status, after := curationRequest(t, origin, "POST", "/export", map[string]any{})
				if status != 200 || !reflect.DeepEqual(before["data"], after["data"]) {
					t.Fatal("invalid source identity partially imported archive")
				}
			}
		})
	}
}
