// Command scratchpad is the command-line interface to Scratchpad for coding
// agents and scripts. It hosts the same tool registry as scratchpad-mcp
// in-process and calls it over an in-memory MCP transport, so every tool, its
// input validation and its output are identical to the MCP server's. Each call
// is a short-lived process, so it always runs the installed version.
//
// It never starts the capture service: a process started from a sandboxed
// agent shell would inherit the sandbox. When the service is stopped it says
// so and exits; the app (launchd) starts the service.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
	"github.com/alexcatdad/scratchpad/mcp/internal/scratchpad"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is injected by release builds with -ldflags "-X main.version=<tag>".
var version = "dev"

const usage = `Usage:
  scratchpad <tool> [-f key=value]... [-F key=json]... [--input <file>|-] [--text]
  scratchpad tools [<tool>]       List tools, or show one tool's input schema
  scratchpad status               Show the local capture service status
  scratchpad hook --agent <name>  Record one agent hook event from stdin
                                  (claude-code or codex; for hook configuration)
  scratchpad hooks print --agent <name>
                                  Print the hook configuration for an agent
  scratchpad --version

<tool> is any Scratchpad tool, with dashes or underscores
(record-decision, get-project-context, search-memory, ...).
  -f key=value   string field
  -F key=value   JSON field (numbers, booleans, arrays, objects); plain text if not JSON
  --input FILE   read a JSON object of fields from FILE, or stdin with "-"
  --text         print the tool's text content instead of JSON

The project is resolved from the current directory, as for scratchpad-mcp.
`

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, newSession)
	cancel()
	os.Exit(code)
}

// sessionFactory connects an MCP client to an in-process Scratchpad server.
type sessionFactory func(ctx context.Context) (*mcp.ClientSession, error)

func newSession(ctx context.Context) (*mcp.ClientSession, error) {
	socket := os.Getenv("SCRATCHPAD_SERVICE_SOCKET")
	if err := requireService(ctx, localcapture.NewClient(socket)); err != nil {
		return nil, err
	}
	c, err := scratchpad.NewLocalClient(socket)
	if err != nil {
		return nil, err
	}
	c.Config.Mirror = os.Getenv("SCRATCHPAD_MIRROR") == "true"
	c.Config.MirrorPath = os.Getenv("SCRATCHPAD_MIRROR_PATH")
	if c.Config.MirrorPath == "" {
		c.Config.MirrorPath = "scratchpad/decisions.jsonl"
	}
	return connect(ctx, scratchpad.NewServerWithVersion(c, version))
}

// connect runs server and an MCP client over in-memory transports.
func connect(ctx context.Context, server *mcp.Server) (*mcp.ClientSession, error) {
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "scratchpad-cli", Version: version}, nil)
	return client.Connect(ctx, clientTransport, nil)
}

type statusClient interface {
	Status(ctx context.Context) (localcapture.ServiceStatus, error)
}

// requireService fails with an actionable message instead of starting the
// collector from this (possibly sandboxed) process.
func requireService(ctx context.Context, client statusClient) error {
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := client.Status(probe); err != nil {
		if errors.Is(err, localcapture.ErrServiceStoppedSentinel) {
			return errors.New("the Scratchpad capture service is not running; open Scratchpad (open -a Scratchpad) to start it, then retry")
		}
		return fmt.Errorf("the Scratchpad capture service is unavailable; open Scratchpad to inspect it: %w", err)
	}
	return nil
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, open sessionFactory) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	switch args[0] {
	case "--version", "version":
		fmt.Fprintf(stdout, "scratchpad %s\n", version)
		return 0
	case "status":
		return printStatus(ctx, localcapture.NewClient(os.Getenv("SCRATCHPAD_SERVICE_SOCKET")), stdout, stderr)
	case "hook":
		return runHook(ctx, args[1:], stdin, stderr, defaultHookDeps())
	case "hooks":
		return runHooks(args[1:], stdout, stderr, executablePath)
	}
	session, err := open(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "scratchpad: %v\n", err)
		return 1
	}
	defer session.Close()
	if args[0] == "tools" {
		return listTools(ctx, session, args[1:], stdout, stderr)
	}
	name := toolName(args[0])
	fields, text, err := parseFields(args[1:], stdin)
	if err != nil {
		fmt.Fprintf(stderr, "scratchpad: %v\n", err)
		return 2
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: fields})
	if err != nil {
		fmt.Fprintf(stderr, "scratchpad: %s: %v\n", name, err)
		return 1
	}
	if result.IsError {
		fmt.Fprintf(stderr, "scratchpad: %s: %s\n", name, contentText(result))
		return 1
	}
	if text || result.StructuredContent == nil {
		fmt.Fprintln(stdout, contentText(result))
		return 0
	}
	return writeJSON(stdout, stderr, result.StructuredContent)
}

// toolName accepts record-decision or record_decision.
func toolName(arg string) string { return strings.ReplaceAll(arg, "-", "_") }

func parseFields(args []string, stdin io.Reader) (map[string]any, bool, error) {
	fields := map[string]any{}
	text := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", arg)
			}
			i++
			return args[i], nil
		}
		switch arg {
		case "--text":
			text = true
		case "-f", "-F":
			pair, err := next()
			if err != nil {
				return nil, false, err
			}
			key, value, ok := strings.Cut(pair, "=")
			if !ok || key == "" {
				return nil, false, fmt.Errorf("%s expects key=value, got %q", arg, pair)
			}
			fields[key] = value
			if arg == "-F" {
				var typed any
				if json.Unmarshal([]byte(value), &typed) == nil {
					fields[key] = typed
				}
			}
		case "--input":
			path, err := next()
			if err != nil {
				return nil, false, err
			}
			var data []byte
			if path == "-" {
				data, err = io.ReadAll(stdin)
			} else {
				data, err = os.ReadFile(path) //nolint:gosec // the caller names the input file
			}
			if err != nil {
				return nil, false, err
			}
			var object map[string]any
			if err = json.Unmarshal(data, &object); err != nil {
				return nil, false, fmt.Errorf("--input must be a JSON object: %w", err)
			}
			for k, v := range object {
				fields[k] = v
			}
		default:
			return nil, false, fmt.Errorf("unknown argument %q (see scratchpad --help)", arg)
		}
	}
	return fields, text, nil
}

func listTools(ctx context.Context, session *mcp.ClientSession, args []string, stdout, stderr io.Writer) int {
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		fmt.Fprintf(stderr, "scratchpad: %v\n", err)
		return 1
	}
	tools := listed.Tools
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	if len(args) > 0 {
		want := toolName(args[0])
		for _, tool := range tools {
			if tool.Name == want {
				return writeJSON(stdout, stderr, map[string]any{
					"name": strings.ReplaceAll(tool.Name, "_", "-"), "description": tool.Description, "input": tool.InputSchema,
				})
			}
		}
		fmt.Fprintf(stderr, "scratchpad: no tool %q\n", args[0])
		return 1
	}
	for _, tool := range tools {
		summary, _, _ := strings.Cut(tool.Description, ". ")
		fmt.Fprintf(stdout, "%-24s %s\n", strings.ReplaceAll(tool.Name, "_", "-"), summary)
	}
	return 0
}

func printStatus(ctx context.Context, client statusClient, stdout, stderr io.Writer) int {
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	status, err := client.Status(probe)
	if err != nil {
		fmt.Fprintf(stderr, "scratchpad: capture service unavailable: %v\n", err)
		return 1
	}
	return writeJSON(stdout, stderr, status)
}

func contentText(result *mcp.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func writeJSON(stdout, stderr io.Writer, value any) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintf(stderr, "scratchpad: %v\n", err)
		return 1
	}
	return 0
}
