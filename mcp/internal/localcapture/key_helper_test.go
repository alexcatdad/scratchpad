package localcapture

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelperKeyProviderUsesOperationAndIdentifierAndPreservesUnlockState(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "argv.synthetic")
	key := strings.Repeat("k", 32)
	keyPath := filepath.Join(dir, "key.synthetic")
	if err := os.WriteFile(keyPath, []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dir, "helper.synthetic.sh")
	script := `#!/bin/sh
printf '%s\n%s\n' "$1" "$2" > "$SCRATCHPAD_HELPER_ARGV"
[ "$2" = com.alexcatdad.scratchpad.capture-queue.v1 ] || exit 1
[ "$1" = get ] || [ "$1" = create ] || exit 1
exec /bin/cat "$SCRATCHPAD_HELPER_KEY"
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCRATCHPAD_HELPER_ARGV", marker)
	t.Setenv("SCRATCHPAD_HELPER_KEY", keyPath)
	got, err := (HelperKeyProvider{Path: helper, Identifier: "com.alexcatdad.scratchpad.capture-queue.v1"}).Load(context.Background())
	if err != nil || string(got) != key {
		t.Fatalf("helper load failed: len=%d err=%v", len(got), err)
	}
	argv, err := os.ReadFile(marker)
	if err != nil || string(argv) != "get\ncom.alexcatdad.scratchpad.capture-queue.v1\n" {
		t.Fatalf("helper argv mismatch: %q err=%v", argv, err)
	}
	created, err := (HelperKeyProvider{Path: helper, Identifier: "com.alexcatdad.scratchpad.capture-queue.v1"}).Create(context.Background())
	if err != nil || string(created) != key {
		t.Fatalf("idempotent helper create did not return the existing key: len=%d err=%v", len(created), err)
	}
	argv, err = os.ReadFile(marker)
	if err != nil || string(argv) != "create\ncom.alexcatdad.scratchpad.capture-queue.v1\n" {
		t.Fatalf("helper create argv mismatch: %q err=%v", argv, err)
	}

	unlocking := filepath.Join(dir, "locked.synthetic.sh")
	if err = os.WriteFile(unlocking, []byte("#!/bin/sh\nexit 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err = (HelperKeyProvider{Path: unlocking, Identifier: "synthetic"}).Load(context.Background())
	if err != ErrKeyUnlockRequiredSentinel {
		t.Fatalf("exit 2 was not preserved as keychain unlock required: %v", err)
	}
	missing := filepath.Join(dir, "missing.synthetic.sh")
	if err = os.WriteFile(missing, []byte("#!/bin/sh\nexit 3\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err = (HelperKeyProvider{Path: missing, Identifier: "synthetic"}).Load(context.Background())
	if err != ErrKeyUnavailableSentinel {
		t.Fatalf("exit 3 was not classified unavailable: %v", err)
	}
}

func TestOpenStorePreservesKeychainUnlockRequirement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	keys := &testKeys{}
	store, err := OpenStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	keys.loadErr = ErrKeyUnlockRequiredSentinel
	if _, err = OpenStore(path, keys); err != ErrKeyUnlockRequiredSentinel {
		t.Fatalf("OpenStore collapsed keychain unlock requirement: %v", err)
	}
}
