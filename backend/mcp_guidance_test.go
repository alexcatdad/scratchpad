package backend_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRealStdioInitializationRecommendsSecretReferences(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	isolatedDir := t.TempDir()
	isolatedMCP := filepath.Join(isolatedDir, "scratchpad-mcp")
	binary, err := os.ReadFile(mcpBinary)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(isolatedMCP, binary, 0700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(isolatedDir, "home")
	if err = os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	socketRoot := "/private/tmp"
	if _, err = os.Stat(socketRoot); err != nil {
		socketRoot = "/tmp"
	}
	socketDir, err := os.MkdirTemp(socketRoot, "sp-mcp-guidance-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "service.sock")
	cmd := exec.CommandContext(ctx, isolatedMCP)
	cmd.Env = []string{
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + home,
		"TMPDIR=" + isolatedDir,
		"SCRATCHPAD_SERVICE_SOCKET=" + socketPath,
		"SCRATCHPAD_URL=http://localhost:1",
		"SCRATCHPAD_PUBLIC_KEY=",
		"SCRATCHPAD_SIGNING_KEY=",
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); cmd.Process.Kill(); cmd.Wait() }()
	if _, err = input.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-11-25\",\"capabilities\":{},\"clientInfo\":{\"name\":\"synthetic-guidance-check\",\"version\":\"1\"}}}\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(output).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if json.Unmarshal(line, &response) != nil {
		t.Fatal("invalid MCP initialization response")
	}
	for _, phrase := range []string{"secret references", "API tokens", "passwords", "private keys", "secret://provider/main"} {
		if !strings.Contains(response.Result.Instructions, phrase) {
			t.Fatalf("initialization omitted secret guidance: %s", phrase)
		}
	}
}
