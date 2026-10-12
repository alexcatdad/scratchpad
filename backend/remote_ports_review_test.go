package backend_test

import "testing"

func TestGitRemoteDefaultPortsShareCanonicalProjectAndKeepCustomPorts(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			resolve := func(remote string) string {
				t.Helper()
				status, response := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": remote}}}, restoredToken)
				if status != 200 {
					t.Fatalf("Git remote resolution failed: %s HTTP %d", remote, status)
				}
				return response["project"].(map[string]any)["id"].(string)
			}
			canonical := resolve("git@github.com:owner/repo.git")
			for _, remote := range []string{
				"ssh://git@github.com:22/owner/repo.git",
				"ssh://git@github.com:00022/owner/repo.git",
				"git://github.com:9418/owner/repo.git",
				"git://github.com:09418/owner/repo.git",
				"https://github.com:00443/owner/repo.git",
				"http://github.com:00080/owner/repo.git",
			} {
				if resolve(remote) != canonical {
					t.Errorf("default port split canonical project: %s", remote)
				}
			}
			sshCustom := resolve("ssh://git@github.com:2222/owner/repo.git")
			gitCustom := resolve("git://github.com:19418/owner/repo.git")
			if sshCustom == canonical || gitCustom == canonical || sshCustom == gitCustom {
				t.Fatal("custom Git ports were conflated with the default identity")
			}
			if resolve("ssh://git@github.com:02222/owner/repo.git") != sshCustom || resolve("git://github.com:019418/owner/repo.git") != gitCustom {
				t.Fatal("equivalent decimal custom ports split project identity")
			}
			ipv6 := resolve("ssh://git@[::1]/owner/repo.git")
			if resolve("ssh://git@[::1]:22/owner/repo.git") != ipv6 {
				t.Fatal("IPv6 default SSH port split project identity")
			}
			status, response := projectRequest(t, origin, "GET", "", nil, restoredToken)
			if status != 200 || len(response["projects"].([]any)) != 4 {
				t.Fatal("canonical/default/custom Git resolution created extra projects")
			}
			for _, value := range response["projects"].([]any) {
				if len(value.(map[string]any)["sources"].([]any)) != 1 {
					t.Fatal("equivalent remote resolutions duplicated source identity")
				}
			}
		})
	}
}
