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

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("scratchpad-mcp 0.1.0")
		return
	}
	c, err := scratchpad.NewClient(scratchpad.Config{URL: os.Getenv("SCRATCHPAD_URL"), PublicKeyPath: os.Getenv("SCRATCHPAD_PUBLIC_KEY"), SigningKeyPath: os.Getenv("SCRATCHPAD_SIGNING_KEY"), Mirror: os.Getenv("SCRATCHPAD_MIRROR") == "true", MirrorPath: os.Getenv("SCRATCHPAD_MIRROR_PATH")})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err = scratchpad.NewServer(c).Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
