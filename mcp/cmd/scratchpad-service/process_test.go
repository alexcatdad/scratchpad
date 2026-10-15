package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
)

func buildServiceForProcessTest(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "scratchpad-service.synthetic")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build service subprocess: %v: %s", err, output)
	}
	return bin
}

func shortProcessTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sps-ct-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func syntheticProcessKeyHelper(t *testing.T, root string) (string, string) {
	t.Helper()
	keyPath := filepath.Join(root, "key.synthetic")
	if err := os.WriteFile(keyPath, []byte(strings.Repeat("k", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(root, "key-helper.synthetic.sh")
	script := `#!/bin/sh
[ "$#" = 2 ] || exit 1
[ "$1" = get ] || [ "$1" = create ] || exit 1
[ "$2" = synthetic-service-test ] || exit 1
exec /bin/cat "$SCRATCHPAD_SYNTHETIC_KEY"
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return helper, keyPath
}

func runServiceAndCaptureStartup(t *testing.T, bin, database, socket, helper, keyPath string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, "--database", database, "--socket", socket, "--key-helper", helper, "--key-identifier", "synthetic-service-test")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_SYNTHETIC_KEY="+keyPath)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func TestProcessRejectsInsecureDatabasePermissions(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission mode checks are not meaningful for root")
	}
	root := t.TempDir()
	database := filepath.Join(root, "queue.sqlite")
	key := strings.Repeat("k", 32)
	store, err := localcapture.OpenStore(database, processTestKeys{key: []byte(key)})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(database, 0644); err != nil {
		t.Fatal(err)
	}
	helper, keyPath := syntheticProcessKeyHelper(t, root)
	output, runErr := runServiceAndCaptureStartup(t, buildServiceForProcessTest(t), database, filepath.Join(shortProcessTempDir(t), "service.sock"), helper, keyPath)
	if runErr == nil || !strings.Contains(output, "private capture storage") || strings.Contains(output, root) || strings.Contains(output, "synthetic") {
		t.Fatalf("service did not safely reject insecure database permissions: output=%q err=%v", output, runErr)
	}
	if mode, statErr := os.Stat(database); statErr != nil || mode.Mode().Perm() != 0644 {
		t.Fatalf("service altered rejected database permissions: mode=%v err=%v", mode, statErr)
	}
}

func TestProcessRejectsInaccessibleExistingSocketWithoutReplacingIt(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("socket permission checks are not meaningful for root")
	}
	root := shortProcessTempDir(t)
	socket := filepath.Join(root, "service.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err = os.Chmod(socket, 0000); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(socket)
	if err != nil {
		t.Fatal(err)
	}
	helper, keyPath := syntheticProcessKeyHelper(t, root)
	output, runErr := runServiceAndCaptureStartup(t, buildServiceForProcessTest(t), filepath.Join(root, "queue.sqlite"), socket, helper, keyPath)
	if runErr == nil || !strings.Contains(output, "private capture storage") || strings.Contains(output, root) || strings.Contains(output, "synthetic") {
		t.Fatalf("service did not safely reject inaccessible socket: output=%q err=%v", output, runErr)
	}
	after, err := os.Lstat(socket)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("service replaced inaccessible existing socket: before=%v after=%v err=%v", before, after, err)
	}
}

type processTestKeys struct{ key []byte }

func (k processTestKeys) Load(context.Context) ([]byte, error) {
	return append([]byte(nil), k.key...), nil
}
func (k processTestKeys) Create(context.Context) ([]byte, error) {
	return append([]byte(nil), k.key...), nil
}
