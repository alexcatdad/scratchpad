package agentevents

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, agent Agent, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", string(agent), name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The Claude Code fixtures mirror payloads recorded from a real session, with
// synthetic content.
func TestNormalizeClaudeCodeFixtures(t *testing.T) {
	const session = "11111111-2222-4333-8444-555555555555"
	cases := []struct {
		file string
		keep bool
		want Event
	}{
		{"session_start.json", true, Event{Kind: KindSessionStart, Reason: "startup", Model: "claude-opus-5"}},
		{"session_end.json", true, Event{Kind: KindSessionEnd, Reason: "prompt_input_exit"}},
		{"user_prompt_submit.json", true, Event{Kind: KindPrompt, TurnID: "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff", PermissionMode: "auto", Text: "No, not like that. Use the existing helper instead."}},
		{"user_prompt_submit_system.json", false, Event{}},
		{"post_tool_use.json", false, Event{}},
		{"post_tool_use_failure.json", true, Event{Kind: KindToolFailure, TurnID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", PermissionMode: "auto", Tool: &Tool{
			Name: "Bash", Error: "Exit code 1\nls: /nonexistent-dir: No such file or directory", DurationMS: 36,
			Input: json.RawMessage(`{"command":"ls /nonexistent-dir","description":"List a directory that does not exist"}`),
		}}},
		{"subagent_post_tool_use_failure.json", true, Event{Kind: KindToolFailure, TurnID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", PermissionMode: "auto", AgentID: "a0000000000000001", AgentType: "Explore", Tool: &Tool{
			Name: "Read", Error: "File does not exist.", Input: json.RawMessage(`{"file_path":"/work/app/missing.go"}`),
		}}},
		{"permission_denied.json", true, Event{Kind: KindPermissionDenied, TurnID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", PermissionMode: "auto", Reason: "[Interfere With Workloads]", Tool: &Tool{
			Name: "Bash", Input: json.RawMessage(`{"command":"pkill -f other-service","description":"Stop another process"}`),
		}}},
		{"stop.json", true, Event{Kind: KindTurnEnd, TurnID: "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff", PermissionMode: "auto", Text: "Done. I switched to the existing helper."}},
		{"stop_failure.json", true, Event{Kind: KindModelFailure, Reason: "rate_limit", Text: "429 Too Many Requests"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			got, keep, err := Normalize(ClaudeCode, fixture(t, ClaudeCode, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			if keep != tc.keep {
				t.Fatalf("keep = %v, want %v", keep, tc.keep)
			}
			if !keep {
				return
			}
			want := tc.want
			want.SchemaVersion, want.Agent, want.SessionID, want.Cwd = SchemaVersion, ClaudeCode, session, "/work/app"
			assertEvent(t, got, want)
		})
	}
}

// Codex shapes follow its hooks reference. The failed tool result shape is not
// documented; these cases pin the signals codexToolFailure recognizes.
func TestNormalizeCodex(t *testing.T) {
	base := `"session_id":"thr_1","transcript_path":null,"cwd":"/work/app","model":"gpt-test","turn_id":"turn_1","permission_mode":"default"`
	cases := []struct {
		name, payload string
		keep          bool
		want          Event
	}{
		{"session start", `{` + base + `,"hook_event_name":"SessionStart","source":"resume"}`, true, Event{Kind: KindSessionStart, Reason: "resume"}},
		{"session end", `{` + base + `,"hook_event_name":"SessionEnd","reason":"other"}`, true, Event{Kind: KindSessionEnd, Reason: "other"}},
		{"prompt", `{` + base + `,"hook_event_name":"UserPromptSubmit","prompt":"<task-notification> is plain text to Codex"}`, true, Event{Kind: KindPrompt, Text: "<task-notification> is plain text to Codex"}},
		{"turn end", `{` + base + `,"hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"done"}`, true, Event{Kind: KindTurnEnd, Text: "done"}},
		{"turn end without message", `{` + base + `,"hook_event_name":"Stop","last_assistant_message":null}`, true, Event{Kind: KindTurnEnd}},
		{"interrupt", `{` + base + `,"hook_event_name":"Interrupt"}`, true, Event{Kind: KindInterrupt}},
		{"model request failed", `{` + base + `,"hook_event_name":"PostModelRequest","status":"failed","error":"stream closed","token_usage":null}`, true, Event{Kind: KindModelFailure, Reason: "failed", Text: "stream closed"}},
		{"model request completed", `{` + base + `,"hook_event_name":"PostModelRequest","status":"completed","error":null}`, false, Event{}},
		{"model request starting", `{` + base + `,"hook_event_name":"PreModelRequest"}`, false, Event{}},
		{"command failed", `{` + base + `,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"call_1","tool_input":{"command":"ls /nonexistent-dir"},"tool_response":{"exit_code":1,"stderr":"ls: /nonexistent-dir: No such file or directory"}}`, true, Event{Kind: KindToolFailure, Tool: &Tool{
			Name: "Bash", Input: json.RawMessage(`{"command":"ls /nonexistent-dir"}`), Error: "Exit code 1\nls: /nonexistent-dir: No such file or directory",
		}}},
		{"command succeeded", `{` + base + `,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"echo ok"},"tool_response":{"exit_code":0,"output":"ok"}}`, false, Event{}},
		{"mcp tool error", `{` + base + `,"hook_event_name":"PostToolUse","tool_name":"mcp__fs__read","tool_input":{"path":"x"},"tool_response":{"isError":true,"error":"not found"}}`, true, Event{Kind: KindToolFailure, Tool: &Tool{
			Name: "mcp__fs__read", Input: json.RawMessage(`{"path":"x"}`), Error: "not found",
		}}},
		{"plain text result", `{` + base + `,"hook_event_name":"PostToolUse","tool_name":"update_plan","tool_input":{},"tool_response":"Plan updated"}`, false, Event{}},
		{"before tool", `{` + base + `,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`, false, Event{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, keep, err := Normalize(Codex, []byte(tc.payload))
			if err != nil {
				t.Fatal(err)
			}
			if keep != tc.keep {
				t.Fatalf("keep = %v, want %v", keep, tc.keep)
			}
			if !keep {
				return
			}
			want := tc.want
			want.SchemaVersion, want.Agent, want.SessionID, want.Cwd = SchemaVersion, Codex, "thr_1", "/work/app"
			want.Model, want.TurnID, want.PermissionMode = "gpt-test", "turn_1", "default"
			assertEvent(t, got, want)
		})
	}
}

func assertEvent(t *testing.T, got, want Event) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("event mismatch\n got %s\nwant %s", gotJSON, wantJSON)
	}
}

func TestNormalizeDropsEveryHarnessPromptPrefix(t *testing.T) {
	for _, prefix := range harnessPrefixes {
		payload, err := json.Marshal(map[string]string{"hook_event_name": "UserPromptSubmit", "session_id": "s", "prompt": "\n " + prefix + "\nbody"})
		if err != nil {
			t.Fatal(err)
		}
		if _, keep, err := Normalize(ClaudeCode, payload); err != nil || keep {
			t.Errorf("prefix %s: keep=%v err=%v", prefix, keep, err)
		}
	}
	// Pasted content and markup the user typed are real prompts.
	for _, prompt := range []string{`<pasted_content id="1">log</pasted_content id="1">`, "<div> does not center", "fix <task-notification> parsing"} {
		payload, _ := json.Marshal(map[string]string{"hook_event_name": "UserPromptSubmit", "session_id": "s", "prompt": prompt})
		if _, keep, err := Normalize(ClaudeCode, payload); err != nil || !keep {
			t.Errorf("prompt %q: keep=%v err=%v", prompt, keep, err)
		}
	}
}

func TestNormalizeRejectsBadInput(t *testing.T) {
	if _, _, err := Normalize("cursor", []byte(`{"hook_event_name":"Stop","session_id":"s"}`)); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("unknown agent: %v", err)
	}
	for _, payload := range []string{``, `[]`, `"text"`, `{`, `{"session_id":"s"}`, `{"hook_event_name":"Stop"}`} {
		if _, _, err := Normalize(ClaudeCode, []byte(payload)); !errors.Is(err, ErrInvalidPayload) {
			t.Errorf("payload %q: err=%v", payload, err)
		}
	}
}

func TestNormalizeBoundsTextAndToolInput(t *testing.T) {
	long := strings.Repeat("é", MaxTextBytes)
	payload, _ := json.Marshal(map[string]string{"hook_event_name": "UserPromptSubmit", "session_id": "s", "prompt": long})
	event, keep, err := Normalize(ClaudeCode, payload)
	if err != nil || !keep {
		t.Fatalf("keep=%v err=%v", keep, err)
	}
	if !strings.HasSuffix(event.Text, "… [truncated 16384 bytes]") || len(event.Text) > MaxTextBytes+64 || !json.Valid(mustJSON(t, event.Text)) {
		t.Fatalf("text not bounded cleanly: len=%d tail=%q", len(event.Text), event.Text[len(event.Text)-40:])
	}

	content := strings.Repeat("x", MaxToolInputBytes)
	payload, _ = json.Marshal(map[string]any{"hook_event_name": "PostToolUseFailure", "session_id": "s", "tool_name": "Write", "error": "disk full",
		"tool_input": map[string]string{"file_path": "/work/app/big.txt", "content": content}})
	event, keep, err = Normalize(ClaudeCode, payload)
	if err != nil || !keep {
		t.Fatalf("keep=%v err=%v", keep, err)
	}
	var bounded struct {
		Truncated bool
		Bytes     int
		Preview   string
	}
	if json.Unmarshal(event.Tool.Input, &bounded) != nil || !bounded.Truncated || bounded.Bytes <= MaxToolInputBytes || len(bounded.Preview) != toolInputPreview {
		t.Fatalf("tool input not bounded: %.120s", event.Tool.Input)
	}
	if len(event.Tool.Input) > MaxToolInputBytes {
		t.Fatalf("bounded input is still %d bytes", len(event.Tool.Input))
	}
}

func mustJSON(t *testing.T, value string) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
