// Package agentevents turns the hook payloads of coding agents into one
// neutral event shape and keeps only the events that suggest something went
// wrong: failed tool calls, the user's prompts, the end of each turn,
// interrupts, failed model requests, denied tool calls and session boundaries.
// Successful tool calls and every other hook event are dropped.
//
// The package is pure: it reads no files, clock or environment. The caller
// adds the event ID, the time, Git context and instruction file hashes.
package agentevents

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// SchemaVersion identifies the Event shape.
const SchemaVersion = 1

const (
	// MaxTextBytes bounds prompt, message and error text.
	MaxTextBytes = 16 << 10
	// MaxToolInputBytes bounds the tool input kept with a failed or denied call.
	MaxToolInputBytes = 32 << 10
	toolInputPreview  = 4 << 10
)

// Agent names the coding agent whose hook produced the payload.
type Agent string

const (
	ClaudeCode Agent = "claude-code"
	Codex      Agent = "codex"
)

// Kind is the neutral event type.
type Kind string

const (
	KindSessionStart     Kind = "session_start"
	KindSessionEnd       Kind = "session_end"
	KindPrompt           Kind = "prompt"
	KindToolFailure      Kind = "tool_failure"
	KindPermissionDenied Kind = "permission_denied"
	KindTurnEnd          Kind = "turn_end"
	KindInterrupt        Kind = "interrupt"
	KindModelFailure     Kind = "model_failure"
)

// ErrUnknownAgent reports an agent this package has no mapping for.
var ErrUnknownAgent = errors.New("agentevents: unknown agent")

// ErrInvalidPayload reports a hook payload that is not a JSON object.
var ErrInvalidPayload = errors.New("agentevents: invalid hook payload")

// Tool describes the tool call behind a tool_failure or permission_denied
// event. Output is never kept, only the error text the agent itself received.
type Tool struct {
	Name        string          `json:"name"`
	Input       json.RawMessage `json:"input,omitempty"`
	Error       string          `json:"error,omitempty"`
	Interrupted bool            `json:"interrupted,omitempty"`
	DurationMS  int64           `json:"durationMs,omitempty"`
}

// InstructionFile records which version of an instruction file was present
// when a session started. Neither agent reports this reliably itself.
type InstructionFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Event is one neutral agent event. Normalize fills the fields that come from
// the hook payload; EventID, OccurredAt, InstructionFiles and GitContext are
// the caller's.
type Event struct {
	SchemaVersion    int               `json:"schemaVersion"`
	EventID          string            `json:"eventId,omitempty"`
	Agent            Agent             `json:"agent"`
	Kind             Kind              `json:"kind"`
	SessionID        string            `json:"sessionId"`
	TurnID           string            `json:"turnId,omitempty"`
	OccurredAt       string            `json:"occurredAt,omitempty"`
	Cwd              string            `json:"cwd,omitempty"`
	Model            string            `json:"model,omitempty"`
	PermissionMode   string            `json:"permissionMode,omitempty"`
	AgentID          string            `json:"agentId,omitempty"`
	AgentType        string            `json:"agentType,omitempty"`
	Tool             *Tool             `json:"tool,omitempty"`
	Text             string            `json:"text,omitempty"`
	Reason           string            `json:"reason,omitempty"`
	InstructionFiles []InstructionFile `json:"instructionFiles,omitempty"`
	GitContext       json.RawMessage   `json:"gitContext,omitempty"`
}

// payload is the union of the hook input fields this package reads from
// either agent. Unknown fields are ignored, so a new agent release that adds
// fields does not break intake.
type payload struct {
	HookEventName        string          `json:"hook_event_name"`
	SessionID            string          `json:"session_id"`
	PromptID             string          `json:"prompt_id"`
	TurnID               string          `json:"turn_id"`
	Cwd                  string          `json:"cwd"`
	Model                string          `json:"model"`
	PermissionMode       string          `json:"permission_mode"`
	AgentID              string          `json:"agent_id"`
	AgentType            string          `json:"agent_type"`
	Source               string          `json:"source"`
	Reason               string          `json:"reason"`
	Prompt               string          `json:"prompt"`
	ToolName             string          `json:"tool_name"`
	ToolInput            json.RawMessage `json:"tool_input"`
	ToolResponse         json.RawMessage `json:"tool_response"`
	Error                json.RawMessage `json:"error"`
	ErrorDetails         string          `json:"error_details"`
	IsInterrupt          bool            `json:"is_interrupt"`
	DurationMS           int64           `json:"duration_ms"`
	LastAssistantMessage string          `json:"last_assistant_message"`
	Status               string          `json:"status"`
}

// Normalize maps one hook payload to an Event. keep is false for payloads that
// are valid but not kept, such as a successful tool call.
func Normalize(agent Agent, raw []byte) (event Event, keep bool, err error) {
	if agent != ClaudeCode && agent != Codex {
		return Event{}, false, fmt.Errorf("%w: %q", ErrUnknownAgent, agent)
	}
	var p payload
	if json.Unmarshal(raw, &p) != nil || p.HookEventName == "" || p.SessionID == "" {
		return Event{}, false, ErrInvalidPayload
	}
	event = Event{
		SchemaVersion:  SchemaVersion,
		Agent:          agent,
		SessionID:      p.SessionID,
		TurnID:         p.PromptID,
		Cwd:            p.Cwd,
		Model:          p.Model,
		PermissionMode: p.PermissionMode,
		AgentID:        p.AgentID,
		AgentType:      p.AgentType,
	}
	if p.TurnID != "" {
		event.TurnID = p.TurnID
	}
	switch p.HookEventName {
	case "SessionStart":
		event.Kind, event.Reason = KindSessionStart, p.Source
	case "SessionEnd":
		event.Kind, event.Reason = KindSessionEnd, p.Reason
	case "UserPromptSubmit":
		if agent == ClaudeCode && harnessPrompt(p.Prompt) {
			return Event{}, false, nil
		}
		event.Kind, event.Text = KindPrompt, truncate(p.Prompt)
	case "Stop":
		event.Kind, event.Text = KindTurnEnd, truncate(p.LastAssistantMessage)
	case "Interrupt":
		event.Kind = KindInterrupt
	default:
		if agent == ClaudeCode {
			keep = claudeOnly(&event, &p)
		} else {
			keep = codexOnly(&event, &p)
		}
		return event, keep, nil
	}
	return event, true, nil
}

func claudeOnly(event *Event, p *payload) bool {
	switch p.HookEventName {
	case "PostToolUseFailure":
		event.Kind = KindToolFailure
		event.Tool = &Tool{Name: p.ToolName, Input: boundInput(p.ToolInput), Error: truncate(text(p.Error)), Interrupted: p.IsInterrupt, DurationMS: p.DurationMS}
	case "PermissionDenied":
		event.Kind, event.Reason = KindPermissionDenied, p.Reason
		event.Tool = &Tool{Name: p.ToolName, Input: boundInput(p.ToolInput)}
	case "StopFailure":
		// Here last_assistant_message is the rendered API error, not model output.
		event.Kind, event.Reason = KindModelFailure, text(p.Error)
		event.Text = truncate(p.ErrorDetails)
	default:
		return false
	}
	return true
}

func codexOnly(event *Event, p *payload) bool {
	switch p.HookEventName {
	case "PostToolUse":
		message, failed := codexToolFailure(p.ToolResponse)
		if !failed {
			return false
		}
		event.Kind = KindToolFailure
		event.Tool = &Tool{Name: p.ToolName, Input: boundInput(p.ToolInput), Error: truncate(message)}
	case "PostModelRequest":
		if p.Status != "failed" {
			return false
		}
		event.Kind, event.Reason = KindModelFailure, p.Status
		event.Text = truncate(text(p.Error))
	default:
		return false
	}
	return true
}

// harnessPrefixes mark prompts Claude Code submits on its own: background task
// results, messages from other sessions, monitor events and command echoes.
// The payload carries no field that separates these from typed prompts, so the
// leading tag is the only signal. They are not user corrections and are dropped.
var harnessPrefixes = []string{
	"<task-notification>",
	"<system-reminder>",
	"<side-sessions-event>",
	"<ci-monitor-event>",
	"<local-command-caveat>",
	"<command-name>",
	"<command-message>",
}

func harnessPrompt(prompt string) bool {
	prompt = strings.TrimLeft(prompt, " \t\r\n")
	for _, prefix := range harnessPrefixes {
		if strings.HasPrefix(prompt, prefix) {
			return true
		}
	}
	return false
}

// codexToolFailure decides whether a Codex PostToolUse result is a failure.
// Codex has no separate failure event and its documentation does not specify
// the result shape, so this recognizes the conventional signals: a non-zero
// exit code, success false, or an MCP result flagged isError.
func codexToolFailure(response json.RawMessage) (string, bool) {
	var result struct {
		ExitCode      *int            `json:"exit_code"`
		ExitCodeCamel *int            `json:"exitCode"`
		Success       *bool           `json:"success"`
		IsError       bool            `json:"isError"`
		Stderr        string          `json:"stderr"`
		Output        string          `json:"output"`
		Error         json.RawMessage `json:"error"`
	}
	if json.Unmarshal(response, &result) != nil {
		return "", false
	}
	code := result.ExitCode
	if code == nil {
		code = result.ExitCodeCamel
	}
	failed := result.IsError || (code != nil && *code != 0) || (result.Success != nil && !*result.Success)
	if !failed {
		return "", false
	}
	message := text(result.Error)
	if message == "" {
		message = result.Stderr
	}
	if message == "" {
		message = result.Output
	}
	if code != nil && *code != 0 {
		message = strings.TrimSpace(fmt.Sprintf("Exit code %d\n%s", *code, message))
	}
	return message, true
}

// text reads a JSON value that is usually a string. Any other value is kept as
// its JSON text so nothing is silently lost.
func text(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return string(raw)
}

func truncate(value string) string {
	if len(value) <= MaxTextBytes {
		return value
	}
	cut := MaxTextBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return fmt.Sprintf("%s\n… [truncated %d bytes]", value[:cut], len(value)-cut)
}

// boundInput keeps a tool input whole when it is small and replaces a large
// one, such as a Write call carrying a whole file, with a short preview.
func boundInput(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" || !json.Valid(raw) {
		return nil
	}
	if len(raw) <= MaxToolInputBytes {
		return raw
	}
	cut := toolInputPreview
	for cut > 0 && !utf8.RuneStart(raw[cut]) {
		cut--
	}
	bounded, err := json.Marshal(struct {
		Truncated bool   `json:"truncated"`
		Bytes     int    `json:"bytes"`
		Preview   string `json:"preview"`
	}{true, len(raw), string(raw[:cut])})
	if err != nil {
		return nil
	}
	return bounded
}
