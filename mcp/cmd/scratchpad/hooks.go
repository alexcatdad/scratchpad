package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/alexcatdad/scratchpad/mcp/internal/agentevents"
)

// The hook events each agent is asked to report. They are the events
// agentevents.Normalize keeps; anything else would only spawn a process to be
// dropped. Codex has no separate failure event, so its PostToolUse is listed
// and successful calls are dropped by the command.
var (
	claudeHookEvents = []string{"SessionStart", "UserPromptSubmit", "PostToolUseFailure", "PermissionDenied", "Stop", "StopFailure", "SessionEnd"}
	codexHookEvents  = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "PostModelRequest", "Stop", "Interrupt", "SessionEnd"}
)

type hookHandler struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Async   bool     `json:"async,omitempty"`
	Timeout int      `json:"timeout,omitempty"`
}

type hookGroup struct {
	Hooks []hookHandler `json:"hooks"`
}

// hookConfiguration builds the hook block for one agent. Every handler is the
// same fixed command, so the definition never changes between releases: Codex
// trusts a hook by its definition and skips a changed one until it is reviewed
// again. The logic lives in the binary the command names.
func hookConfiguration(agent agentevents.Agent, executable string) map[string]any {
	groups := map[string][]hookGroup{}
	switch agent {
	case agentevents.ClaudeCode:
		for _, event := range claudeHookEvents {
			// Exec form: the path is one argument, so spaces need no quoting.
			handler := hookHandler{Type: "command", Command: executable, Args: []string{"hook", "--agent", string(agent)}, Async: event != "SessionEnd"}
			groups[event] = []hookGroup{{Hooks: []hookHandler{handler}}}
		}
	case agentevents.Codex:
		for _, event := range codexHookEvents {
			handler := hookHandler{Type: "command", Command: shellQuote(executable) + " hook --agent " + string(agent), Async: true}
			// Codex runs SessionEnd synchronously and allows it and Interrupt at
			// most three seconds.
			if event == "SessionEnd" || event == "Interrupt" {
				handler.Async, handler.Timeout = false, 3
			}
			groups[event] = []hookGroup{{Hooks: []hookHandler{handler}}}
		}
	}
	return map[string]any{"hooks": groups}
}

func shellQuote(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n'\"\\$`!*?[]{}()<>|&;~#") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

const hooksUsage = `Usage:
  scratchpad hooks print --agent claude-code|codex

Prints the hook configuration that makes the agent report mistake signals to
Scratchpad. It changes no file.
`

// runHooks prints configuration only. Agent settings belong to the user, and
// Codex additionally requires each hook to be reviewed and trusted by hand.
func runHooks(args []string, stdout, stderr io.Writer, executable func() (string, error)) int {
	if len(args) == 0 || args[0] != "print" {
		fmt.Fprint(stderr, hooksUsage)
		return 2
	}
	agent, ok := hookAgent(args[1:])
	if !ok {
		fmt.Fprint(stderr, hooksUsage)
		return 2
	}
	path, err := executable()
	if err != nil || path == "" {
		fmt.Fprintln(stderr, "scratchpad: could not determine the path of this command")
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err = encoder.Encode(hookConfiguration(agent, path)); err != nil {
		fmt.Fprintf(stderr, "scratchpad: %v\n", err)
		return 1
	}
	if agent == agentevents.Codex {
		fmt.Fprintln(stderr, "Merge this into ~/.codex/hooks.json, then run /hooks in Codex and trust the new hooks.")
	} else {
		fmt.Fprintln(stderr, "Merge the \"hooks\" object into ~/.claude/settings.json.")
	}
	fmt.Fprintln(stderr, "Events are stored only for projects whose agentEvents setting is on.")
	return 0
}

func executablePath() (string, error) { return os.Executable() }
