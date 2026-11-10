package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexcatdad/scratchpad/mcp/internal/agentevents"
	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
)

type fakeHookClient struct {
	events []json.RawMessage
	err    error
}

func (f *fakeHookClient) AgentEvent(_ context.Context, req localcapture.AgentEventRequest) (localcapture.AgentEventResponse, error) {
	f.events = append(f.events, req.Event)
	return localcapture.AgentEventResponse{Accepted: f.err == nil}, f.err
}

func testHookDeps(client hookClient, root string) hookDeps {
	return hookDeps{
		client: client,
		now:    func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 5, time.UTC) },
		newID:  func() (string, error) { return "0123456789abcdef0123456789abcdef", nil },
		discover: func(context.Context, string) (json.RawMessage, string) {
			if root == "" {
				return nil, ""
			}
			return json.RawMessage(`{"repositoryIdentity":"example.invalid/owner/app","dirty":false,"worktree":{"detected":false}}`), root
		},
	}
}

func runHookWith(t *testing.T, deps hookDeps, stdin string, args ...string) (code int, stderr string) {
	t.Helper()
	var errOut bytes.Buffer
	code = runHook(context.Background(), args, strings.NewReader(stdin), &errOut, deps)
	return code, errOut.String()
}

func TestHookRecordsAKeptEventWithContext(t *testing.T) {
	client := &fakeHookClient{}
	payload := `{"hook_event_name":"PostToolUseFailure","session_id":"s1","prompt_id":"p1","cwd":"/work/app","tool_name":"Bash","tool_input":{"command":"ls /nope"},"error":"Exit code 1","is_interrupt":false}`
	code, stderr := runHookWith(t, testHookDeps(client, "/work/app"), payload, "--agent", "claude-code")
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if len(client.events) != 1 {
		t.Fatalf("sent %d events", len(client.events))
	}
	var event agentevents.Event
	if err := json.Unmarshal(client.events[0], &event); err != nil {
		t.Fatal(err)
	}
	if event.Kind != agentevents.KindToolFailure || event.Agent != agentevents.ClaudeCode || event.EventID != "0123456789abcdef0123456789abcdef" ||
		event.OccurredAt != "2026-11-10T12:00:00.000000005Z" || event.Tool == nil || event.Tool.Error != "Exit code 1" ||
		!strings.Contains(string(event.GitContext), "example.invalid/owner/app") || event.InstructionFiles != nil {
		t.Fatalf("unexpected event: %s", client.events[0])
	}
}

func TestHookDropsEventsThatAreNotMistakeSignals(t *testing.T) {
	client := &fakeHookClient{}
	deps := testHookDeps(client, "")
	for _, payload := range []string{
		`{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"Bash","tool_input":{"command":"echo ok"},"tool_response":{"stdout":"ok"}}`,
		`{"hook_event_name":"UserPromptSubmit","session_id":"s1","prompt":"<task-notification>\n<status>completed</status>"}`,
		`{"hook_event_name":"PreCompact","session_id":"s1","trigger":"auto"}`,
	} {
		if code, stderr := runHookWith(t, deps, payload, "--agent=claude-code"); code != 0 || stderr != "" {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
	}
	if len(client.events) != 0 {
		t.Fatalf("sent %d events, want none", len(client.events))
	}
}

// The hook must never disturb the agent: every failure exits 0, with the
// reason on stderr and nothing on stdout (there is no stdout writer at all).
func TestHookAlwaysExitsZero(t *testing.T) {
	valid := `{"hook_event_name":"Stop","session_id":"s1","last_assistant_message":"done"}`
	cases := []struct {
		name, stdin string
		args        []string
		deps        hookDeps
		wantStderr  string
	}{
		{"missing agent", valid, nil, testHookDeps(&fakeHookClient{}, ""), "expected --agent"},
		{"unknown agent", valid, []string{"--agent", "cursor"}, testHookDeps(&fakeHookClient{}, ""), "expected --agent"},
		{"not json", "not json", []string{"--agent", "codex"}, testHookDeps(&fakeHookClient{}, ""), "invalid hook payload"},
		{"oversized", `{"hook_event_name":"Stop","session_id":"s1","pad":"` + strings.Repeat("x", hookInputLimit) + `"}`, []string{"--agent", "codex"}, testHookDeps(&fakeHookClient{}, ""), "larger than 1 MiB"},
		{"service stopped", valid, []string{"--agent", "codex"}, testHookDeps(&fakeHookClient{err: localcapture.ErrServiceStoppedSentinel}, ""), "event not recorded"},
		{"no identifier", valid, []string{"--agent", "codex"}, func() hookDeps {
			deps := testHookDeps(&fakeHookClient{}, "")
			deps.newID = func() (string, error) { return "", errors.New("no entropy") }
			return deps
		}(), "event identifier"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stderr := runHookWith(t, tc.deps, tc.stdin, tc.args...)
			if code != 0 {
				t.Fatalf("exit code %d, want 0", code)
			}
			if !strings.Contains(stderr, tc.wantStderr) {
				t.Fatalf("stderr %q does not mention %q", stderr, tc.wantStderr)
			}
		})
	}
}

func TestHookIsReachableFromTheCommandLine(t *testing.T) {
	// No service socket exists in tests, so the send fails; the command still
	// exits 0 and prints nothing to stdout.
	t.Setenv("SCRATCHPAD_SERVICE_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))
	code, stdout, stderr := runCLI(t, nil, `{"hook_event_name":"Stop","session_id":"s1","last_assistant_message":"done"}`, "hook", "--agent", "codex")
	if code != 0 || stdout != "" || !strings.Contains(stderr, "event not recorded") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestSessionStartHashesInstructionFilesUpToTheRepositoryRoot(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	cwd := filepath.Join(root, "services", "api")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "AGENTS.md"), "root rules")
	write(filepath.Join(root, ".claude", "rules", "b.md"), "rule b")
	write(filepath.Join(root, ".claude", "rules", "a.md"), "rule a")
	write(filepath.Join(cwd, "CLAUDE.md"), "nested rules")
	write(filepath.Join(home, ".codex", "AGENTS.md"), "user rules")
	write(filepath.Join(filepath.Dir(root), "AGENTS.md"), "outside the repository")
	t.Cleanup(func() { _ = os.Remove(filepath.Join(filepath.Dir(root), "AGENTS.md")) })

	client := &fakeHookClient{}
	deps := testHookDeps(client, root)
	deps.home = home
	payload, _ := json.Marshal(map[string]string{"hook_event_name": "SessionStart", "session_id": "s1", "cwd": cwd, "source": "startup"})
	if code, stderr := runHookWith(t, deps, string(payload), "--agent", "codex"); code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var event agentevents.Event
	if err := json.Unmarshal(client.events[0], &event); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, file := range event.InstructionFiles {
		if len(file.SHA256) != 64 {
			t.Fatalf("bad hash for %s", file.Path)
		}
		got = append(got, file.Path)
	}
	want := []string{
		filepath.Join(cwd, "CLAUDE.md"),
		filepath.Join(root, "AGENTS.md"),
		filepath.Join(root, ".claude", "rules", "a.md"),
		filepath.Join(root, ".claude", "rules", "b.md"),
		filepath.Join(home, ".codex", "AGENTS.md"),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("instruction files\n got %v\nwant %v", got, want)
	}
}

func TestProjectDirs(t *testing.T) {
	cases := []struct {
		cwd, root string
		want      []string
	}{
		{"", "/repo", nil},
		{"/repo/a/b", "", []string{"/repo/a/b"}},
		{"/repo/a/b", "/repo", []string{"/repo/a/b", "/repo/a", "/repo"}},
		{"/repo", "/repo", []string{"/repo"}},
		{"/elsewhere/x", "/repo", []string{"/elsewhere/x"}},
	}
	for _, tc := range cases {
		got := projectDirs(tc.cwd, tc.root)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("projectDirs(%q,%q) = %v, want %v", tc.cwd, tc.root, got, tc.want)
		}
	}
}
