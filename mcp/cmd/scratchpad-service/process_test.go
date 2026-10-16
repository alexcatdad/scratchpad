package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

type startedService struct {
	cmd    *exec.Cmd
	output *safeBuffer
	cancel context.CancelFunc
	mu     sync.Mutex
	waited bool
	done   chan struct{}
	err    error
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func (b *safeBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Len()
}

func startSyntheticService(bin, database, socket, helper, keyPath string, explicit bool, extraEnv ...string) *startedService {
	args := []string{"--database", database, "--socket", socket, "--key-helper", helper, "--key-identifier", "synthetic-service-test"}
	if explicit {
		args = append(args, "--explicit-start")
	}
	output := &safeBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), append([]string{"SCRATCHPAD_SYNTHETIC_KEY=" + keyPath}, extraEnv...)...)
	cmd.Stdout, cmd.Stderr = output, output
	return &startedService{cmd: cmd, output: output, cancel: cancel, done: make(chan struct{})}
}

func startTrackedService(t *testing.T, service *startedService) error {
	t.Helper()
	if err := service.cmd.Start(); err != nil {
		return err
	}
	t.Cleanup(service.cleanup)
	return nil
}

func (s *startedService) wait() error {
	s.mu.Lock()
	if s.waited {
		done := s.done
		s.mu.Unlock()
		<-done
		s.mu.Lock()
		err := s.err
		s.mu.Unlock()
		return err
	}
	s.waited = true
	s.mu.Unlock()
	err := s.cmd.Wait()
	s.mu.Lock()
	s.err = err
	close(s.done)
	s.mu.Unlock()
	return err
}

func (s *startedService) cleanup() {
	s.cancel()
	_ = s.wait()
}

func TestStartedServiceCleanupCancelsBlockedProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "/bin/sleep", "30")
	service := &startedService{cmd: cmd, cancel: cancel, done: make(chan struct{})}
	if err := startTrackedService(t, service); err != nil {
		t.Fatal(err)
	}

	cleaned := make(chan struct{})
	go func() {
		service.cleanup()
		close(cleaned)
	}()
	select {
	case <-cleaned:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not cancel and reap the blocked process")
	}
	if err := service.wait(); err == nil {
		t.Fatal("cancelled process unexpectedly exited successfully")
	}
}

type serviceExit struct {
	service *startedService
	err     error
}

func waitService(t *testing.T, exits <-chan serviceExit, service *startedService, timeout time.Duration) error {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case result := <-exits:
			if result.service == service {
				return result.err
			}
		case <-timer.C:
			t.Fatalf("service process did not exit within %s", timeout)
		}
	}
}

func TestProcessConcurrentStartsShareOneCollectorAndPreserveStop(t *testing.T) {
	root := shortProcessTempDir(t)
	database := filepath.Join(root, "queue.sqlite")
	socket := filepath.Join(root, "service.sock")
	keyPath := filepath.Join(root, "key.synthetic")
	if err := os.WriteFile(keyPath, []byte(strings.Repeat("k", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(root, "key-helper.blocking.synthetic.sh")
	enteredPath := filepath.Join(root, "key-helper-entered")
	releasePath := filepath.Join(root, "key-helper-release")
	callsPath := filepath.Join(root, "key-helper-calls")
	script := `#!/bin/sh
[ "$#" = 2 ] || exit 1
[ "$1" = get ] || [ "$1" = create ] || exit 1
[ "$2" = synthetic-service-test ] || exit 1
printf 'call\n' >> "$SCRATCHPAD_SYNTHETIC_CALLS"
touch "$SCRATCHPAD_SYNTHETIC_ENTERED"
while [ ! -f "$SCRATCHPAD_SYNTHETIC_RELEASE" ]; do /bin/sleep 0.01; done
exec /bin/cat "$SCRATCHPAD_SYNTHETIC_KEY"
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	bin := buildServiceForProcessTest(t)
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err = stale.Close(); err != nil {
		t.Fatal(err)
	}

	extraEnv := []string{"SCRATCHPAD_SYNTHETIC_ENTERED=" + enteredPath, "SCRATCHPAD_SYNTHETIC_RELEASE=" + releasePath, "SCRATCHPAD_SYNTHETIC_CALLS=" + callsPath}
	first := startSyntheticService(bin, database, socket, helper, keyPath, false, extraEnv...)
	if err = startTrackedService(t, first); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = os.Stat(enteredPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first service did not reach synthetic key helper")
		}
		time.Sleep(10 * time.Millisecond)
	}
	second := startSyntheticService(bin, database, socket, helper, keyPath, false, extraEnv...)
	if err = startTrackedService(t, second); err != nil {
		t.Fatal(err)
	}
	secondExit := make(chan error, 1)
	go func() { secondExit <- second.wait() }()
	select {
	case err = <-secondExit:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || !strings.Contains(second.output.String(), "another instance may be active") {
			t.Fatalf("competing startup did not fail with active-owner conflict: err=%v output=%q", err, second.output.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("competing startup blocked instead of returning a bounded conflict")
	}
	calls, readErr := os.ReadFile(callsPath)
	if readErr != nil || strings.Count(string(calls), "call\n") != 1 {
		t.Fatalf("second process entered key/database initialization before the first bound the socket: calls=%q err=%v", calls, readErr)
	}
	if err = os.WriteFile(releasePath, nil, 0600); err != nil {
		t.Fatal(err)
	}

	exits := make(chan serviceExit, 1)
	go func() { exits <- serviceExit{service: first, err: first.wait()} }()
	clients := []*localcapture.IPCClient{localcapture.NewClient(socket), localcapture.NewClient(socket)}
	deadline = time.Now().Add(8 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		_, firstErr := clients[0].Status(context.Background())
		_, secondErr := clients[1].Status(context.Background())
		if firstErr == nil && secondErr == nil {
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("concurrent clients did not reach one ready service: outputs=%q / %q", first.output.String(), second.output.String())
	}

	if first.output.Len() != 0 {
		t.Fatalf("service owner unexpectedly emitted startup error: %q", first.output.String())
	}
	for _, client := range clients {
		if _, err = client.Status(context.Background()); err != nil {
			t.Fatalf("clients did not share the live service: %v", err)
		}
	}
	if err = clients[0].Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = waitService(t, exits, first, 5*time.Second); err != nil {
		t.Fatalf("Stop acknowledgement did not complete service shutdown: %v", err)
	}

	ordinary := startSyntheticService(bin, database, socket, helper, keyPath, false, extraEnv...)
	if err = startTrackedService(t, ordinary); err != nil {
		t.Fatal(err)
	}
	if err = ordinary.wait(); err == nil {
		t.Fatal("ordinary startup did not report persisted Stop")
	} else {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != localcapture.PersistentlyStoppedStartupExitCode || !strings.Contains(ordinary.output.String(), "saved lifecycle setting") {
			t.Fatalf("ordinary startup returned the wrong stopped outcome: %v output=%q", err, ordinary.output.String())
		}
	}
	if _, err = clients[0].Status(context.Background()); err == nil {
		t.Fatal("ordinary startup restarted service after persistent Stop")
	}

	explicit := startSyntheticService(bin, database, socket, helper, keyPath, true, extraEnv...)
	if err = startTrackedService(t, explicit); err != nil {
		t.Fatal(err)
	}
	exitResults := make(chan serviceExit, 1)
	go func() { exitResults <- serviceExit{service: explicit, err: explicit.wait()} }()
	deadline = time.Now().Add(5 * time.Second)
	var status localcapture.ServiceStatus
	for time.Now().Before(deadline) {
		status, err = clients[0].Status(context.Background())
		if err == nil && status.Lifecycle == localcapture.LifecycleRunning {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || status.Lifecycle != localcapture.LifecycleRunning {
		t.Fatalf("explicit start did not clear Stop: status=%#v err=%v output=%q", status, err, explicit.output.String())
	}
	if err = clients[0].Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = waitService(t, exitResults, explicit, 5*time.Second); err != nil {
		t.Fatalf("explicit Start/Stop lifecycle did not stop cleanly: %v", err)
	}
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
