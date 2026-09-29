package scratchpad

import (
	"context"
	"errors"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type GitContext struct {
	RepositoryIdentity string `json:"repositoryIdentity,omitempty"`
	Remote             string `json:"remote,omitempty"`
	Branch             string `json:"branch,omitempty"`
	Commit             string `json:"commit,omitempty"`
	RootPathHint       string `json:"rootPathHint,omitempty"`
	Dirty              bool   `json:"dirty"`
	Worktree           struct {
		Detected bool   `json:"detected"`
		Name     string `json:"name,omitempty"`
	} `json:"worktree"`
}

func NormalizeRemote(remote string) (string, error) {
	remote = strings.TrimSpace(remote)
	var host, path string
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil {
			return "", err
		}
		if u.Scheme != "ssh" && u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "git" {
			return "", errors.New("unsupported Git remote scheme")
		}
		host = u.Host
		path = u.Path
	} else {
		lhs, rhs, ok := strings.Cut(remote, ":")
		if !ok || strings.Contains(lhs, "/") {
			return "", errors.New("local Git remotes need an explicit project")
		}
		_, host, _ = strings.Cut(lhs, "@")
		if host == "" {
			host = lhs
		}
		path = rhs
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || path == "" || strings.Contains(path, "..") {
		return "", errors.New("invalid Git remote")
	}
	return strings.ToLower(host) + "/" + path, nil
}
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-C", dir}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
func Discover(ctx context.Context, dir string) (*GitContext, error) {
	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, nil
	}
	// Collect checkout provenance before selecting a remote. Explicit project
	// resolution must not discard branch/commit metadata when remotes disagree.
	g := &GitContext{RootPathHint: root}
	g.Branch, _ = git(ctx, root, "symbolic-ref", "--short", "-q", "HEAD")
	g.Commit, _ = git(ctx, root, "rev-parse", "HEAD")
	status, _ := git(ctx, root, "status", "--porcelain")
	g.Dirty = status != ""
	common, _ := git(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	actual, _ := git(ctx, root, "rev-parse", "--absolute-git-dir")
	g.Worktree.Detected = common != actual
	g.Worktree.Name = filepath.Base(root)
	names, err := git(ctx, root, "remote")
	if err != nil {
		return nil, err
	}
	identities := map[string]string{}
	var origin string
	for _, name := range strings.Fields(names) {
		remotes, e := git(ctx, root, "remote", "get-url", "--all", name)
		if e != nil {
			continue
		}
		for _, remote := range strings.Split(remotes, "\n") {
			id, e := NormalizeRemote(remote)
			if e != nil {
				continue
			}
			identities[id] = remote
			if name == "origin" {
				if origin != "" && origin != id {
					return g, &APIError{Code: "PROJECT_IDENTITY_AMBIGUOUS", Message: "origin has competing remote identities"}
				}
				origin = id
			}
		}
	}
	id := origin
	if id == "" {
		if len(identities) > 1 {
			candidates := make([]string, 0, len(identities))
			for candidate := range identities {
				candidates = append(candidates, candidate)
			}
			sort.Strings(candidates)
			return g, &APIError{Code: "PROJECT_IDENTITY_AMBIGUOUS", Message: "Choose a project explicitly; remotes identify different repositories", Details: candidates}
		}
		for candidate := range identities {
			id = candidate
		}
	}
	g.RepositoryIdentity = id
	if id != "" {
		g.Remote = "https://" + id
	}
	return g, nil
}
