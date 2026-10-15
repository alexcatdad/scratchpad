package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
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
	c, err := scratchpad.NewClient(scratchpad.Config{URL: os.Getenv("SCRATCHPAD_URL"), PublicKeyPath: os.Getenv("SCRATCHPAD_PUBLIC_KEY"), SigningKeyPath: os.Getenv("SCRATCHPAD_SIGNING_KEY"), Mirror: os.Getenv("SCRATCHPAD_MIRROR") == "true", MirrorPath: os.Getenv("SCRATCHPAD_MIRROR_PATH")})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Capture always goes through the durable per-user service. An explicitly
	// configured socket is useful for tests and managed installs; blank uses the
	// platform default. A missing service is reported by each capture tool.
	c.CaptureService = localcapture.NewClient(os.Getenv("SCRATCHPAD_SERVICE_SOCKET"))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err = scratchpad.NewServerWithVersion(c, version).Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
