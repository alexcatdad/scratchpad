package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const restrictedRemote = "https://github.com/example/scratchpad-e2e-restricted.git"

func projectScenarios(ctx context.Context, session *mcp.ClientSession, workspace, restrictedProject string) ([]capture, error) {
	fixture, err := os.MkdirTemp(filepath.Dir(workspace), "project-scenarios-")
	if err != nil {
		return nil, err
	}
	// Keep these disposable repositories under the workflow's temporary root so
	// the restart phase can replay the exact original working-directory inputs.
	git := func(dir string, args ...string) (string, error) {
		command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		out, err := command.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("Git fixture setup: %w: %s", err, out)
		}
		return strings.TrimSpace(string(out)), nil
	}
	var captures []capture
	record := func(title string, scope map[string]any) (map[string]any, error) {
		args := map[string]any{"requestId": "scenario-" + filepath.Base(fixture) + "-" + title, "title": title, "decision": "Preserve project boundaries", "authority": "observed", "confidence": "high"}
		for key, value := range scope {
			args[key] = value
		}
		out, err := call(ctx, session, "record_decision", args)
		if err != nil {
			return nil, err
		}
		raw, ok := out["record"].(map[string]any)
		if !ok {
			return nil, errors.New("project scenario returned no record")
		}
		id, _ := raw["id"].(string)
		projectID, _ := raw["projectId"].(string)
		if id == "" || projectID == "" {
			return nil, errors.New("project scenario omitted record/project identity")
		}
		gitContext, _ := raw["gitContext"].(map[string]any)
		captures = append(captures, capture{Tool: "record_decision", Arguments: args, RecordID: id, ProjectID: projectID, GitContext: gitContext})
		return out, nil
	}
	// D: the owner has classified this actual Git identity as external, so
	// server policy must deny the locally enabled mirror before any append.
	contractor := filepath.Join(fixture, "contractor")
	if err = os.Mkdir(contractor, 0700); err != nil {
		return nil, err
	}
	if _, err = git(contractor, "init"); err != nil {
		return nil, err
	}
	if _, err = git(contractor, "remote", "add", "origin", restrictedRemote); err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(contractor, "client.txt"), []byte("disposable restricted fixture"), 0600); err != nil {
		return nil, err
	}
	before, err := snapshot(contractor)
	if err != nil {
		return nil, err
	}
	central, err := record("Restricted repository", map[string]any{"workingDirectory": contractor})
	if err != nil {
		return nil, err
	}
	restricted, _ := central["record"].(map[string]any)
	mirror, _ := central["mirror"].(map[string]any)
	if restricted["projectId"] != restrictedProject || mirror["eligible"] != false || mirror["attempted"] == true {
		return nil, fmt.Errorf("restricted project did not deny mirroring: %v", central)
	}
	after, err := snapshot(contractor)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(before, after) {
		return nil, errors.New("restricted capture changed working tree or Git metadata")
	}
	// E: linked checkouts are the same project; per-call directory selection
	// must retain their distinct branch, commit, root and worktree context.
	// The workflow creates its disposable baseline before initial captures so
	// their Git provenance remains identical through the later restart replay.
	branch, err := git(workspace, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return nil, err
	}
	commit, err := git(workspace, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	checkoutRoot, err := git(workspace, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	worktree := filepath.Join(fixture, "feature")
	if _, err = git(workspace, "worktree", "add", "-b", "acceptance-feature", worktree); err != nil {
		return nil, err
	}
	// The parent workflow removes the entire temporary fixture after restart
	// verification; retaining the linked checkout keeps retry provenance stable.
	if _, err = git(worktree, "-c", "user.name=Acceptance", "-c", "user.email=acceptance@example.com", "commit", "--allow-empty", "-m", "Disposable feature revision"); err != nil {
		return nil, err
	}
	featureCommit, err := git(worktree, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	featureRoot, err := git(worktree, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	var projectID any
	for _, item := range []struct {
		title, directory, branch, commit string
		worktree                         bool
	}{{"Main checkout", "", branch, commit, false}, {"Feature worktree", worktree, "acceptance-feature", featureCommit, true}, {"Main checkout after override", "", branch, commit, false}} {
		out, err := record(item.title, map[string]any{"workingDirectory": item.directory})
		if err != nil {
			return nil, err
		}
		raw, _ := out["record"].(map[string]any)
		if projectID == nil {
			projectID = raw["projectId"]
		}
		context, _ := raw["gitContext"].(map[string]any)
		wt, _ := context["worktree"].(map[string]any)
		root := checkoutRoot
		if item.directory != "" {
			root = featureRoot
		}
		if raw["projectId"] != projectID || context["branch"] != item.branch || context["commit"] != item.commit || context["rootPathHint"] != root || wt["detected"] != item.worktree {
			return nil, fmt.Errorf("worktree identity/provenance mismatch: %v", out)
		}
	}
	// F: lack of strong identity is retained durably without inventing a project;
	// an owner can resolve it later without changing the folder.
	nonGit := filepath.Join(fixture, "non-git")
	if err = os.Mkdir(nonGit, 0700); err != nil {
		return nil, err
	}
	heldArguments := map[string]any{"workingDirectory": nonGit, "requestId": "non-git-before-resolution", "title": "Non-Git identity", "decision": "Ask for project identity", "authority": "observed", "confidence": "high"}
	queued, err := call(ctx, session, "record_decision", heldArguments)
	if err != nil {
		return nil, err
	}
	receipt, _ := queued["receipt"].(map[string]any)
	content, _ := receipt["content"].(map[string]any)
	scope, _ := content["scope"].(map[string]any)
	if queued["outcome"] != "saved_pending" || receipt["state"] != "needs_selection" || receipt["centralRecordId"] != nil || receipt["centralProjectId"] != nil || scope["workingDirectory"] != nonGit {
		return nil, fmt.Errorf("non-Git capture was not held for owner selection: %v", queued)
	}
	captures = append(captures, capture{Tool: "record_decision", Arguments: heldArguments, ReceiptID: fmt.Sprint(receipt["receiptId"]), NeedsSelection: true})
	heldReplay, err := call(ctx, session, "record_decision", heldArguments)
	heldReplayReceipt, _ := heldReplay["receipt"].(map[string]any)
	if err != nil || heldReplay["outcome"] != "saved_pending" || heldReplayReceipt["receiptId"] != receipt["receiptId"] || heldReplayReceipt["state"] != "needs_selection" {
		return nil, fmt.Errorf("unchanged non-Git capture replay changed its receipt: %v %v", heldReplay, err)
	}
	resolved, err := call(ctx, session, "resolve_project", map[string]any{"workingDirectory": nonGit, "name": "Owner confirmed non-Git " + filepath.Base(fixture)})
	if err != nil {
		return nil, err
	}
	project, _ := resolved["project"].(map[string]any)
	created, err := record("Explicit non-Git project", map[string]any{"workingDirectory": nonGit, "projectId": project["id"]})
	if err != nil {
		return nil, err
	}
	raw, _ := created["record"].(map[string]any)
	if raw["projectId"] != project["id"] || raw["gitContext"] != nil {
		return nil, fmt.Errorf("non-Git capture lost explicit identity or invented Git provenance: %v", created)
	}
	entries, err := os.ReadDir(nonGit)
	if err != nil || len(entries) != 0 {
		return nil, errors.New("non-Git central-only capture changed its folder")
	}
	for _, saved := range captures {
		if saved.NeedsSelection {
			continue
		}
		retried, err := call(ctx, session, saved.Tool, saved.Arguments)
		if err != nil {
			return nil, err
		}
		raw, _ := retried["record"].(map[string]any)
		if raw["id"] != saved.RecordID {
			return nil, errors.New("project scenario replay created a duplicate")
		}
	}
	after, err = snapshot(contractor)
	if err != nil || !reflect.DeepEqual(before, after) {
		return nil, errors.New("restricted replay changed working tree or Git metadata")
	}
	return captures, nil
}

func snapshot(root string) (map[string][32]byte, error) {
	files := map[string][32]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[relative] = sha256.Sum256(data)
		return nil
	})
	return files, err
}
