package backend_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSQLitePrivateCreationUnderPermissiveUmask(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "private")
	path := filepath.Join(dir, "memory.sqlite")
	cmd := exec.Command("/bin/sh", "-c", "umask 022; exec \"$@\"", "sh", binary, "migrate")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL=", "SCRATCHPAD_DATABASE_PATH="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("migration under permissive umask failed: %v %s", err, out)
	}
	assertPrivateMode(t, dir, 0700)
	assertPrivateMode(t, path, 0600)
}

func TestSQLiteRejectsUnsafeDirectoryAndSidecars(t *testing.T) {
	for _, variant := range []string{"directory", "shm", "journal"} {
		t.Run(variant, func(t *testing.T) {
			path := legacyFixture(t)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch variant {
			case "directory":
				if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
			case "shm", "journal":
				companion := path + "-" + variant
				if err := os.WriteFile(companion, []byte("synthetic private database companion"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(companion, 0644); err != nil {
					t.Fatal(err)
				}
			}
			out, err := migrationCommand(path).CombinedOutput()
			if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
				t.Fatalf("unsafe %s accepted: %v %s", variant, err, out)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("rejected %s changed the database", variant)
			}
		})
	}
}

func TestSQLiteRejectsFIFOWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "memory.sqlite")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "migrate")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL=", "SCRATCHPAD_DATABASE_PATH="+path)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("opening FIFO blocked instead of refusing it: %s", out)
	}
	if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
		t.Fatalf("FIFO accepted: %v %s", err, out)
	}
}

func TestSQLiteRejectsDarwinACL(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("filesystem ACL behavior is Darwin-specific")
	}
	path := legacyFixture(t)
	if err := exec.Command("/bin/chmod", "+a", "everyone allow read", path).Run(); err != nil {
		t.Skipf("filesystem does not support adding an ACL: %v", err)
	}
	t.Cleanup(func() { _ = exec.Command("/bin/chmod", "-N", path).Run() })
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := migrationCommand(path).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
		t.Fatalf("ACL-protected database accepted: %v %s", err, out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejecting ACL-protected database changed its contents")
	}
}

func assertPrivateMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
