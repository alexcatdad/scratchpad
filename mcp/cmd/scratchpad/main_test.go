package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
	"github.com/alexcatdad/scratchpad/mcp/internal/scratchpad"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// offlineSession hosts the real tool registry with a client whose service
// socket does not exist, so listing works and calls fail like a stopped service.
func offlineSession(t *testing.T) sessionFactory {
	t.Helper()
	return func(ctx context.Context) (*mcp.ClientSession, error) {
		c, err := scratchpad.NewLocalClient(filepath.Join(t.TempDir(), "missing.sock"))
		if err != nil {
			return nil, err
		}
		return connect(ctx, scratchpad.NewServerWithVersion(c, "test"))
	}
}

func runCLI(t *testing.T, open sessionFactory, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, strings.NewReader(stdin), &out, &errOut, open)
	return code, out.String(), errOut.String()
}

// Every tool the MCP server registers is reachable from the CLI, because the
// CLI lists and calls the same in-process registry.
func TestEveryMCPToolIsACommand(t *testing.T) {
	ctx := context.Background()
	session, err := offlineSession(t)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) < 20 {
		t.Fatalf("expected the full Scratchpad registry, got %d tools", len(listed.Tools))
	}
	code, stdout, stderr := runCLI(t, offlineSession(t), "", "tools")
	if code != 0 {
		t.Fatalf("tools exited %d: %s", code, stderr)
	}
	for _, tool := range listed.Tools {
		command := strings.ReplaceAll(tool.Name, "_", "-")
		if !strings.Contains(stdout, command+" ") {
			t.Errorf("tool %s missing from `scratchpad tools`", command)
		}
		code, schema, _ := runCLI(t, offlineSession(t), "", "tools", command)
		if code != 0 || !strings.Contains(schema, `"input"`) {
			t.Errorf("`scratchpad tools %s` did not describe the tool", command)
		}
	}
}

func TestCallFailsClosedWhenTheServiceIsMissing(t *testing.T) {
	code, _, stderr := runCLI(t, offlineSession(t), "", "search-memory", "-f", "query=x")
	if code != 1 || !strings.Contains(stderr, "search_memory") {
		t.Fatalf("expected a failed call, got %d %q", code, stderr)
	}
	code, _, stderr = runCLI(t, offlineSession(t), "", "no-such-tool")
	if code != 1 || stderr == "" {
		t.Fatalf("expected an unknown-tool error, got %d %q", code, stderr)
	}
}

func TestHelpVersionAndBadArguments(t *testing.T) {
	if code, out, _ := runCLI(t, offlineSession(t), "", "--help"); code != 0 || !strings.Contains(out, "Usage:") {
		t.Fatalf("help: %d %q", code, out)
	}
	if code, out, _ := runCLI(t, offlineSession(t), "", "--version"); code != 0 || !strings.HasPrefix(out, "scratchpad ") {
		t.Fatalf("version: %d %q", code, out)
	}
	if code, _, stderr := runCLI(t, offlineSession(t), "", "search-memory", "--bogus"); code != 2 || !strings.Contains(stderr, "unknown argument") {
		t.Fatalf("bad argument: %d %q", code, stderr)
	}
	if code, _, _ := runCLI(t, offlineSession(t), "", "tools", "nope"); code != 1 {
		t.Fatalf("unknown tool schema should fail, got %d", code)
	}
	failing := func(context.Context) (*mcp.ClientSession, error) { return nil, errors.New("service down") }
	if code, _, stderr := runCLI(t, failing, "", "search-memory"); code != 1 || !strings.Contains(stderr, "service down") {
		t.Fatalf("session error: %d %q", code, stderr)
	}
}

func TestParseFields(t *testing.T) {
	input := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(input, []byte(`{"title":"From file","tags":["a"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fields, text, err := parseFields([]string{
		"--input", input, "-f", "summary=a=b", "-F", "limit=3", "-F", "note=plain text", "--text",
	}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if !text || fields["title"] != "From file" || fields["summary"] != "a=b" || fields["limit"] != float64(3) ||
		fields["note"] != "plain text" || len(fields["tags"].([]any)) != 1 {
		t.Fatalf("unexpected fields %#v text=%v", fields, text)
	}
	fields, _, err = parseFields([]string{"--input", "-"}, strings.NewReader(`{"query":"stdin"}`))
	if err != nil || fields["query"] != "stdin" {
		t.Fatalf("stdin input: %#v %v", fields, err)
	}
	for _, bad := range [][]string{{"-f"}, {"-f", "novalue"}, {"--input", "-"}, {"--input"}, {"--input", "/nonexistent"}} {
		if _, _, err := parseFields(bad, strings.NewReader("not json")); err == nil {
			t.Errorf("expected an error for %v", bad)
		}
	}
	if toolName("record-decision") != "record_decision" {
		t.Fatal("dashes should map to underscores")
	}
}

type fakeStatus struct{ err error }

func (f fakeStatus) Status(context.Context) (localcapture.ServiceStatus, error) {
	return localcapture.ServiceStatus{Lifecycle: "running"}, f.err
}

func TestRequireServiceNeverStartsTheCollector(t *testing.T) {
	ctx := context.Background()
	if err := requireService(ctx, fakeStatus{}); err != nil {
		t.Fatal(err)
	}
	err := requireService(ctx, fakeStatus{err: localcapture.ErrServiceStoppedSentinel})
	if err == nil || !strings.Contains(err.Error(), "open Scratchpad") {
		t.Fatalf("stopped service should point at the app, got %v", err)
	}
	if err := requireService(ctx, fakeStatus{err: errors.New("boom")}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("other errors should be wrapped, got %v", err)
	}
	var out, errOut bytes.Buffer
	if code := printStatus(ctx, fakeStatus{}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "running") {
		t.Fatalf("status: %d %q", code, out.String())
	}
	if code := printStatus(ctx, fakeStatus{err: errors.New("down")}, &out, &errOut); code != 1 {
		t.Fatalf("status error should exit 1, got %d", code)
	}
}
