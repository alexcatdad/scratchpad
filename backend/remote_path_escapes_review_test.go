package backend_test

import "testing"

func TestGitRemoteEquivalentPathEscapesResolveOneProject(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL, _ := githubHistoricalBrowser(t, engine)
			origin := startServer(t, path, databaseURL)
			resolve := func(remote string) string {
				t.Helper()
				status, result := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": remote}}}, restoredToken)
				if status != 200 {
					t.Fatalf("valid remote rejected HTTP%d: %s", status, remote)
				}
				return result["project"].(map[string]any)["id"].(string)
			}
			sets := [][]string{
				{"https://github.com/owner/repo.git", "https://github.com/%6Fwner/%72epo%2egit", "git@github.com:%6fwner/%72epo.git", "ssh://git@GitHub.com:22/owner/temporary/%2E%2E/%72epo.git?ignored=synthetic#fragment"},
				{"https://github.com/owner/étude.git", "https://github.com/owner/%c3%a9tude.git", "git@github.com:owner/%C3%A9tude.git"},
				{"ssh://git@github.com:2222/owner/repo.git", "ssh://git@github.com:02222/%6Fwner/%72epo.git"},
			}
			seen := map[string]bool{}
			for _, remotes := range sets {
				id := resolve(remotes[0])
				if seen[id] {
					t.Fatal("distinct Unicode/custom-port repositories conflated")
				}
				seen[id] = true
				for _, remote := range remotes[1:] {
					if resolve(remote) != id {
						t.Errorf("equivalent escaped remote split project: %s", remote)
					}
				}
			}
			for _, remote := range []string{"https://github.com/owner%2Frepo.git", "https://github.com/owner/%2frepo.git", "git@github.com:owner%2frepo.git", "ssh://git@github.com/owner%5Crepo.git", "https://github.com/owner/%GGrepo.git"} {
				status, _ := projectRequest(t, origin, "POST", "/resolve", map[string]any{"context": map[string]any{"git": map[string]any{"remote": remote}}}, restoredToken)
				if status != 400 {
					t.Errorf("unsafe/malformed path accepted HTTP%d: %s", status, remote)
				}
			}
			status, result := projectRequest(t, origin, "GET", "", nil, restoredToken)
			if status != 200 || len(result["projects"].([]any)) != 3 {
				t.Fatal("path normalization duplicated or created unsafe projects")
			}
			for _, raw := range result["projects"].([]any) {
				if len(raw.(map[string]any)["sources"].([]any)) != 1 {
					t.Fatal("canonical source aliases were duplicated")
				}
			}
		})
	}
}
