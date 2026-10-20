package backend_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
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

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	server := exec.Command("/bin/sh", "-c", "umask 022; exec \"$@\"", "sh", binary, "serve")
	server.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL=", "SCRATCHPAD_DATABASE_PATH="+path, "SCRATCHPAD_LISTEN_ADDR="+address, "SCRATCHPAD_PUBLIC_URL=http://"+address)
	stdout, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var serverLogs bytes.Buffer
	server.Stderr = &serverLogs
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- server.Wait() }()
	t.Cleanup(func() {
		_ = server.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-waited:
			if err != nil && !strings.Contains(err.Error(), "signal: terminated") {
				t.Errorf("server exited unsuccessfully: %v %s", err, serverLogs.String())
			}
		case <-time.After(10 * time.Second):
			_ = server.Process.Kill()
			<-waited
			t.Error("server did not shut down")
		}
	})
	line := make(chan []byte, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			line <- scanner.Bytes()
		} else {
			line <- nil
		}
	}()
	select {
	case data := <-line:
		var report struct {
			Address string `json:"address"`
		}
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatalf("server startup report unavailable: %s %s", data, serverLogs.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("server startup timed out: %s", serverLogs.String())
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		companion := path + suffix
		deadline := time.Now().Add(2 * time.Second)
		for {
			if _, err := os.Stat(companion); err == nil {
				assertPrivateMode(t, companion, 0600)
				break
			} else if time.Now().After(deadline) {
				t.Fatalf("live server did not create %s", suffix)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
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
