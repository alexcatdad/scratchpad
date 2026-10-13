package backend_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPublicOriginRejectsNoncanonicalNumericIPv4(t *testing.T) {
	for _, origin := range []string{
		"https://127.1", "https://2130706433", "https://0x7f000001",
		"https://0177.0.0.1", "https://127.000.000.001", "https://127.0.0.1.", "https://１２７.１",
		"https://127.0.0.0x", "https://api.0x",
		"http://127.1", "http://0x7f000001",
	} {
		t.Run(origin, func(t *testing.T) {
			path := legacyFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "serve")
			command.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL=", "SCRATCHPAD_DATABASE_PATH="+path, "SCRATCHPAD_LISTEN_ADDR=127.0.0.1:0", "SCRATCHPAD_PUBLIC_URL="+origin)
			output, err := command.CombinedOutput()
			if err == nil || ctx.Err() != nil || !strings.Contains(string(output), "CONFIG_INVALID") {
				t.Fatalf("noncanonical numeric public origin was not refused: %s", origin)
			}
		})
	}
}
