package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alexcatdad/scratchpad/mcp/internal/scratchpad"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is injected by release builds with -ldflags "-X main.version=<tag>".
var version = "dev"
var buildNumber = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("scratchpad-mcp %s\n", version)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--build-info" {
		fmt.Printf("scratchpad-mcp %s (build %s)\n", version, buildNumber)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "connect" {
		os.Exit(runConnect(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "status" {
		os.Exit(runStatus())
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// Capture always goes through the durable per-user service. An explicitly
	// configured socket is useful for tests and managed installs; blank uses the
	// platform default. Attempt collector startup before serving MCP. A failure
	// keeps MCP available for diagnostics; all operations fail closed.
	socketPath := os.Getenv("SCRATCHPAD_SERVICE_SOCKET")
	if err := ensureCollector(ctx, socketPath, launchCollector); err != nil {
		fmt.Fprintln(os.Stderr, "Scratchpad local capture is unavailable; open the tray app to inspect service status.")
	}
	c, err := scratchpad.NewLocalClient(socketPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	c.Config.Mirror = os.Getenv("SCRATCHPAD_MIRROR") == "true"
	c.Config.MirrorPath = os.Getenv("SCRATCHPAD_MIRROR_PATH")
	if c.Config.MirrorPath == "" {
		c.Config.MirrorPath = "scratchpad/decisions.jsonl"
	}
	if err = scratchpad.NewServerWithVersion(c, version).Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
