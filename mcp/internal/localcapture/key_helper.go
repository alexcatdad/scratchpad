package localcapture

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

// HelperKeyProvider delegates key storage to the bundled native Keychain
// helper. Helper output is accepted only when it is exactly one raw AES-256
// key; all diagnostics are intentionally discarded.
type HelperKeyProvider struct {
	Path       string
	Identifier string
}

func (p HelperKeyProvider) Load(ctx context.Context) ([]byte, error)   { return p.run(ctx, "get") }
func (p HelperKeyProvider) Create(ctx context.Context) ([]byte, error) { return p.run(ctx, "create") }

func (p HelperKeyProvider) run(ctx context.Context, operation string) ([]byte, error) {
	if strings.TrimSpace(p.Path) == "" || strings.TrimSpace(p.Identifier) == "" {
		return nil, ErrKeyUnavailableSentinel
	}
	cmd := exec.CommandContext(ctx, p.Path, operation, p.Identifier)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
			return nil, ErrKeyUnlockRequiredSentinel
		}
		return nil, ErrKeyUnavailableSentinel
	}
	data := stdout.Bytes()
	if len(data) != 32 {
		return nil, ErrKeyUnavailableSentinel
	}
	return append([]byte(nil), data...), nil
}
