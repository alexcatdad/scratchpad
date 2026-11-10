package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alexcatdad/scratchpad/mcp/internal/agentevents"
)

func printHooks(t *testing.T, path string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = runHooks(args, &out, &errOut, func() (string, error) { return path, nil })
	return code, out.String(), errOut.String()
}

type printedHooks struct {
	Hooks map[string][]struct {
		Matcher string        `json:"matcher"`
		Hooks   []hookHandler `json:"hooks"`
	} `json:"hooks"`
}

func TestHooksPrintClaudeCode(t *testing.T) {
	const path = "/Applications/Scratch Pad.app/Contents/Helpers/scratchpad"
	code, stdout, stderr := printHooks(t, path, "print", "--agent", "claude-code")
	if code != 0 || !strings.Contains(stderr, "~/.claude/settings.json") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var printed printedHooks
	if err := json.Unmarshal([]byte(stdout), &printed); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}
	if len(printed.Hooks) != len(claudeHookEvents) {
		t.Fatalf("events = %d, want %d", len(printed.Hooks), len(claudeHookEvents))
	}
	for _, event := range claudeHookEvents {
		groups := printed.Hooks[event]
		if len(groups) != 1 || len(groups[0].Hooks) != 1 || groups[0].Matcher != "" {
			t.Fatalf("%s: unexpected groups %#v", event, groups)
		}
		handler := groups[0].Hooks[0]
		// Exec form keeps a path with spaces as one argument.
		if handler.Type != "command" || handler.Command != path || strings.Join(handler.Args, " ") != "hook --agent claude-code" {
			t.Fatalf("%s: unexpected handler %#v", event, handler)
		}
		if handler.Async != (event != "SessionEnd") {
			t.Fatalf("%s: async=%v", event, handler.Async)
		}
	}
}

func TestHooksPrintCodex(t *testing.T) {
	code, stdout, stderr := printHooks(t, "/Applications/Scratch Pad.app/Contents/Helpers/scratchpad", "print", "--agent=codex")
	if code != 0 || !strings.Contains(stderr, "/hooks") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var printed printedHooks
	if err := json.Unmarshal([]byte(stdout), &printed); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}
	if len(printed.Hooks) != len(codexHookEvents) {
		t.Fatalf("events = %d, want %d", len(printed.Hooks), len(codexHookEvents))
	}
	const command = `'/Applications/Scratch Pad.app/Contents/Helpers/scratchpad' hook --agent codex`
	for _, event := range codexHookEvents {
		handler := printed.Hooks[event][0].Hooks[0]
		if handler.Type != "command" || handler.Command != command || handler.Args != nil {
			t.Fatalf("%s: unexpected handler %#v", event, handler)
		}
		bounded := event == "SessionEnd" || event == "Interrupt"
		if handler.Async == bounded || (handler.Timeout == 3) != bounded {
			t.Fatalf("%s: async=%v timeout=%d", event, handler.Async, handler.Timeout)
		}
	}
}

// Every configured event must be one the hook command can keep; otherwise the
// agent would spawn a process for nothing on each occurrence.
func TestConfiguredHookEventsAreOnesTheCommandKeeps(t *testing.T) {
	samples := map[string]string{
		"PostToolUse":      `"tool_name":"Bash","tool_input":{},"tool_response":{"exit_code":1}`,
		"PostModelRequest": `"status":"failed","error":"x"`,
		"UserPromptSubmit": `"prompt":"hello"`,
	}
	for agent, events := range map[agentevents.Agent][]string{agentevents.ClaudeCode: claudeHookEvents, agentevents.Codex: codexHookEvents} {
		for _, event := range events {
			payload := `{"hook_event_name":"` + event + `","session_id":"s"`
			if extra := samples[event]; extra != "" {
				payload += "," + extra
			}
			if _, keep, err := agentevents.Normalize(agent, []byte(payload+"}")); err != nil || !keep {
				t.Errorf("%s %s: keep=%v err=%v", agent, event, keep, err)
			}
		}
	}
}

func TestHooksPrintRejectsBadUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"install"}, {"print"}, {"print", "--agent", "cursor"}} {
		if code, stdout, stderr := printHooks(t, "/bin/scratchpad", args...); code != 2 || stdout != "" || !strings.Contains(stderr, "Usage:") {
			t.Errorf("%v: code=%d stdout=%q", args, code, stdout)
		}
	}
	var out, errOut bytes.Buffer
	if code := runHooks([]string{"print", "--agent", "codex"}, &out, &errOut, func() (string, error) { return "", errors.New("unknown") }); code != 1 || out.Len() != 0 {
		t.Fatalf("missing executable path: code=%d", code)
	}
}

func TestShellQuote(t *testing.T) {
	for input, want := range map[string]string{
		"/usr/local/bin/scratchpad": "/usr/local/bin/scratchpad",
		"/Apps/Scratch Pad/bin":     "'/Apps/Scratch Pad/bin'",
		"/it's/here":                `'/it'\''s/here'`,
		"":                          "''",
	} {
		if got := shellQuote(input); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", input, got, want)
		}
	}
}

func TestHooksIsReachableFromTheCommandLine(t *testing.T) {
	code, stdout, _ := runCLI(t, nil, "", "hooks", "print", "--agent", "claude-code")
	if code != 0 || !strings.Contains(stdout, `"PostToolUseFailure"`) {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}
