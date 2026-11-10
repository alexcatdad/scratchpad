package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alexcatdad/scratchpad/mcp/internal/agentevents"
	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
	"github.com/alexcatdad/scratchpad/mcp/internal/scratchpad"
)

const (
	hookInputLimit       = 1 << 20
	hookGitBudget        = 400 * time.Millisecond
	hookSendBudget       = 300 * time.Millisecond
	instructionFileLimit = 32
	instructionFileBytes = 1 << 20
)

type hookClient interface {
	AgentEvent(context.Context, localcapture.AgentEventRequest) (localcapture.AgentEventResponse, error)
}

// hookDeps are the hook command's effects, replaced in tests.
type hookDeps struct {
	client   hookClient
	now      func() time.Time
	newID    func() (string, error)
	discover func(ctx context.Context, dir string) (gitContext json.RawMessage, root string)
	home     string
}

func defaultHookDeps() hookDeps {
	client := localcapture.NewClient(os.Getenv("SCRATCHPAD_SERVICE_SOCKET"))
	client.Timeout = hookSendBudget
	home, _ := os.UserHomeDir()
	return hookDeps{client: client, now: time.Now, newID: randomEventID, discover: discoverGit, home: home}
}

func randomEventID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func discoverGit(ctx context.Context, dir string) (json.RawMessage, string) {
	ctx, cancel := context.WithTimeout(ctx, hookGitBudget)
	defer cancel()
	git, err := scratchpad.Discover(ctx, dir)
	if err != nil || git == nil {
		return nil, ""
	}
	encoded, err := json.Marshal(git)
	if err != nil {
		return nil, ""
	}
	return encoded, git.RootPathHint
}

// runHook records one agent hook event. It is called by the agent's harness on
// every configured event, so it never blocks, never prints to stdout (some
// events add stdout to the model's context) and always exits 0: a failure to
// record must not disturb the agent. Problems go to stderr, which the harness
// keeps in its debug log.
func runHook(ctx context.Context, args []string, stdin io.Reader, stderr io.Writer, deps hookDeps) int {
	agent, ok := hookAgent(args)
	if !ok {
		fmt.Fprintln(stderr, "scratchpad hook: expected --agent claude-code or --agent codex")
		return 0
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, hookInputLimit+1))
	if err != nil || len(raw) > hookInputLimit {
		fmt.Fprintln(stderr, "scratchpad hook: input unreadable or larger than 1 MiB; event not recorded")
		return 0
	}
	event, keep, err := agentevents.Normalize(agent, raw)
	if err != nil {
		fmt.Fprintf(stderr, "scratchpad hook: %v\n", err)
		return 0
	}
	if !keep {
		return 0
	}
	if event.EventID, err = deps.newID(); err != nil {
		fmt.Fprintln(stderr, "scratchpad hook: could not create an event identifier")
		return 0
	}
	event.OccurredAt = deps.now().UTC().Format(time.RFC3339Nano)
	root := ""
	if event.Cwd != "" {
		event.GitContext, root = deps.discover(ctx, event.Cwd)
	}
	if event.Kind == agentevents.KindSessionStart {
		event.InstructionFiles = instructionFiles(event.Cwd, root, deps.home)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		fmt.Fprintln(stderr, "scratchpad hook: could not encode the event")
		return 0
	}
	send, cancel := context.WithTimeout(ctx, hookSendBudget)
	defer cancel()
	if _, err = deps.client.AgentEvent(send, localcapture.AgentEventRequest{Event: encoded}); err != nil {
		fmt.Fprintf(stderr, "scratchpad hook: event not recorded: %v\n", err)
	}
	return 0
}

func hookAgent(args []string) (agentevents.Agent, bool) {
	name := ""
	switch {
	case len(args) == 2 && args[0] == "--agent":
		name = args[1]
	case len(args) == 1 && strings.HasPrefix(args[0], "--agent="):
		name = strings.TrimPrefix(args[0], "--agent=")
	}
	agent := agentevents.Agent(name)
	return agent, agent == agentevents.ClaudeCode || agent == agentevents.Codex
}

// instructionFiles hashes the instruction files in force for a session: the
// project files from the working directory up to the repository root, and the
// user-level files. Neither agent reports reliably which version was loaded,
// and the analysis needs it to tell whether a changed rule reduced a mistake.
func instructionFiles(cwd, root, home string) []agentevents.InstructionFile {
	var paths []string
	for _, dir := range projectDirs(cwd, root) {
		paths = append(paths,
			filepath.Join(dir, "AGENTS.md"),
			filepath.Join(dir, "CLAUDE.md"),
			filepath.Join(dir, "CLAUDE.local.md"),
			filepath.Join(dir, ".claude", "CLAUDE.md"),
		)
		rules, _ := filepath.Glob(filepath.Join(dir, ".claude", "rules", "*.md"))
		sort.Strings(rules)
		paths = append(paths, rules...)
	}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(home, ".codex", "AGENTS.md"))
	}
	var files []agentevents.InstructionFile
	seen := map[string]bool{}
	for _, path := range paths {
		if len(files) == instructionFileLimit {
			break
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		if sum, ok := hashFile(path); ok {
			files = append(files, agentevents.InstructionFile{Path: path, SHA256: sum})
		}
	}
	return files
}

// projectDirs lists cwd and its parents up to and including root. Without a
// repository root, or when cwd is outside it, only cwd is used.
func projectDirs(cwd, root string) []string {
	if cwd == "" {
		return nil
	}
	cwd = filepath.Clean(cwd)
	if root == "" {
		return []string{cwd}
	}
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, cwd)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return []string{cwd}
	}
	dirs := []string{cwd}
	for dir := cwd; dir != root; {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
		dirs = append(dirs, dir)
	}
	return dirs
}

func hashFile(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > instructionFileBytes {
		return "", false
	}
	data, err := os.ReadFile(path) //nolint:gosec // a fixed set of instruction file names under the session's own directories
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), true
}
