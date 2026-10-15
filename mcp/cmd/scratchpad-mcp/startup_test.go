package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
)

type fakeCollectorClient struct {
	mu     sync.Mutex
	status localcapture.ServiceStatus
	err    error
}

func (f *fakeCollectorClient) Status(context.Context) (localcapture.ServiceStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, f.err
}

func (f *fakeCollectorClient) set(status localcapture.ServiceStatus, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.err = status, err
}

func readyCollector(lifecycle localcapture.Lifecycle) localcapture.ServiceStatus {
	return localcapture.ServiceStatus{
		Lifecycle: lifecycle, KeyAvailable: true, StorageAvailable: true,
		QueueSchemaVersion: localcapture.QueueSchemaVersion,
	}
}

func TestCollectorReadinessAcceptsRunningAndPaused(t *testing.T) {
	for _, lifecycle := range []localcapture.Lifecycle{localcapture.LifecycleRunning, localcapture.LifecyclePaused} {
		t.Run(string(lifecycle), func(t *testing.T) {
			client := &fakeCollectorClient{status: readyCollector(lifecycle)}
			started := false
			err := ensureCollectorWithClient(context.Background(), "synthetic.sock", client, func(string) (<-chan error, error) {
				started = true
				return nil, nil
			})
			if err != nil {
				t.Fatalf("ready collector rejected: %v", err)
			}
			if started {
				t.Fatal("readiness check unexpectedly started a second collector")
			}
		})
	}
}

func TestEnsureCollectorDoesNotClearPersistentStop(t *testing.T) {
	client := &fakeCollectorClient{err: localcapture.ErrServiceStoppedSentinel}
	starts := 0
	start := func(socket string) (<-chan error, error) {
		starts++
		if socket != "synthetic.sock" {
			t.Fatalf("unexpected socket %q", socket)
		}
		finished := make(chan error, 1)
		finished <- nil // Ordinary service startup exits when persisted Stop is set.
		return finished, nil
	}
	finished, err := start("synthetic.sock")
	if err != nil {
		t.Fatal(err)
	}
	err = waitForCollector(context.Background(), "synthetic.sock", client, start, finished, 10*time.Millisecond, time.Millisecond)
	if starts != 1 {
		t.Fatalf("persistent Stop triggered %d attempts; want one ordinary launch", starts)
	}
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("persistent Stop was not respected: %v", err)
	}
}

func TestEnsureCollectorReportsPersistedStopExitCode(t *testing.T) {
	client := &fakeCollectorClient{err: localcapture.ErrServiceStoppedSentinel}
	start := func(string) (<-chan error, error) {
		finished := make(chan error, 1)
		finished <- syntheticProcessExit(3)
		return finished, nil
	}
	err := ensureCollectorWithClient(context.Background(), "synthetic.sock", client, start)
	if err == nil || !strings.Contains(err.Error(), "stopped by its saved setting") {
		t.Fatalf("persisted Stop exit was not reported clearly: %v", err)
	}
}

type syntheticProcessExit int

func (e syntheticProcessExit) Error() string { return "synthetic process exit" }
func (e syntheticProcessExit) ExitCode() int { return int(e) }

func TestStoppedCollectorIsActionableAndDoesNotStartAgain(t *testing.T) {
	client := &fakeCollectorClient{status: readyCollector(localcapture.LifecycleStopped)}
	err := collectorReadinessError(client.status)
	if err == nil || !strings.Contains(err.Error(), "Start the service from the tray menu") {
		t.Fatalf("stopped state lacks actionable message: %v", err)
	}
}

func TestCollectorWaitsForConcurrentStartupAndReadiness(t *testing.T) {
	client := &fakeCollectorClient{err: localcapture.ErrServiceStoppedSentinel}
	started := make(chan struct{})
	finished := make(chan error, 1)
	start := func(socket string) (<-chan error, error) {
		if socket != "/tmp/synthetic.sock" {
			t.Fatalf("unexpected socket %q", socket)
		}
		close(started)
		client.set(readyCollector(localcapture.LifecycleRunning), nil)
		finished <- nil
		return finished, nil
	}
	finishedBy := make(chan error, 1)
	go func() {
		child, err := start("/tmp/synthetic.sock")
		if err == nil {
			err = waitForCollector(context.Background(), "/tmp/synthetic.sock", client, start, child, time.Second, time.Millisecond)
		}
		finishedBy <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("startup callback was not called")
	}
	select {
	case err := <-finishedBy:
		if err != nil {
			t.Fatalf("collector did not become ready: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("readiness did not complete")
	}
}

func TestCollectorReadinessRejectsUnavailableStorage(t *testing.T) {
	status := readyCollector(localcapture.LifecycleRunning)
	status.StorageAvailable = false
	if err := collectorReadinessError(status); err == nil || !strings.Contains(err.Error(), "encrypted capture storage") {
		t.Fatalf("unavailable storage was treated as ready: %v", err)
	}
}

func TestCollectorExecutableComesOnlyFromBundledSibling(t *testing.T) {
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, "scratchpad-mcp")
	if err := os.WriteFile(mcpPath, []byte("synthetic MCP executable"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "scratchpad-service")
	if err := os.WriteFile(path, []byte("synthetic executable"), 0700); err != nil {
		t.Fatal(err)
	}
	resolved, err := bundledCollectorExecutable(mcpPath)
	if err != nil || resolved != path {
		t.Fatalf("bundled sibling executable did not resolve: %q, %v", resolved, err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = bundledCollectorExecutable(mcpPath); err == nil {
		t.Fatal("non-executable sibling was accepted")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	pathEnv := filepath.Join(t.TempDir(), "path-bin")
	if err = os.Mkdir(pathEnv, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(pathEnv, "scratchpad-service"), []byte("synthetic path helper"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathEnv)
	if _, err = bundledCollectorExecutable(mcpPath); err == nil {
		t.Fatal("service executable was discovered from PATH")
	}
}

func TestCollectorEnvironmentPreservesSSHAgentWithSystemPath(t *testing.T) {
	t.Setenv("PATH", "/private/tmp/synthetic-untrusted-bin")
	t.Setenv("SSH_AUTH_SOCK", "/private/tmp/synthetic-agent.sock")
	t.Setenv("DYLD_INSERT_LIBRARIES", "/private/tmp/synthetic-injection.dylib")

	environment := make(map[string]string)
	for _, entry := range collectorEnvironment() {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("invalid environment entry %q", entry)
		}
		environment[name] = value
	}
	if got := environment["SSH_AUTH_SOCK"]; got != "/private/tmp/synthetic-agent.sock" {
		t.Fatalf("SSH agent socket was not preserved: %q", got)
	}
	if got := environment["PATH"]; got != collectorSystemPath {
		t.Fatalf("collector PATH = %q, want fixed system path %q", got, collectorSystemPath)
	}
	if _, ok := environment["DYLD_INSERT_LIBRARIES"]; ok {
		t.Fatal("unapproved dynamic-library environment was passed to collector")
	}
}

func TestCollectorWaitIsBounded(t *testing.T) {
	client := &fakeCollectorClient{err: localcapture.ErrServiceStoppedSentinel}
	start := func(string) (<-chan error, error) { return nil, nil }
	if err := waitForCollector(context.Background(), "synthetic.sock", client, start, nil, 10*time.Millisecond, time.Millisecond); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected bounded startup timeout, got %v", err)
	}
}

func TestCollectorLaunchSurvivesMCPParentExitWithoutLeakingStdio(t *testing.T) {
	dir := t.TempDir()
	socketDir, err := os.MkdirTemp("/private/tmp", "spc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "service.sock")
	pidFile := filepath.Join(dir, "collector.pid")
	t.Cleanup(func() {
		pidBytes, readErr := os.ReadFile(pidFile)
		if readErr != nil {
			return
		}
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
		if parseErr == nil {
			if process, findErr := os.FindProcess(pid); findErr == nil {
				_ = process.Kill()
			}
		}
	})
	servicePath := filepath.Join(dir, "scratchpad-service")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nSCRATCHPAD_FIXTURE_CHILD=1 SCRATCHPAD_FIXTURE_SOCKET=\"$2\" SCRATCHPAD_FIXTURE_PID_FILE=" + shellQuote(pidFile) + " exec " + shellQuote(binary) + " -test.run=^TestCollectorFixtureChildProcess$\n"
	if err = os.WriteFile(servicePath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(binary, "-test.run=^TestCollectorLauncherParentProcess$")
	parent.Env = append(os.Environ(),
		"SCRATCHPAD_LAUNCH_PARENT=1",
		"SCRATCHPAD_LAUNCH_SOCKET="+socket,
		"SCRATCHPAD_LAUNCH_EXECUTABLE="+servicePath,
	)
	var stderr bytes.Buffer
	parent.Stderr = &stderr
	output, err := parent.Output()
	if err != nil {
		fixtureErr, _ := os.ReadFile(pidFile + ".error")
		t.Fatalf("synthetic MCP parent failed: %v; stderr=%q; fixture=%q", err, stderr.String(), fixtureErr)
	}
	if string(output) != "parent-ready\n" {
		t.Fatalf("unexpected MCP parent stdout: %q", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("collector output leaked into MCP stderr: %q", stderr.String())
	}
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("collector child did not survive long enough to write its pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil || pid <= 0 {
		t.Fatalf("invalid collector pid %d: %v", pid, err)
	}
	client := localcapture.NewClient(socket)
	client.Timeout = 100 * time.Millisecond
	deadline := time.Now().Add(2 * time.Second)
	for {
		status, statusErr := client.Status(context.Background())
		if statusErr == nil {
			if err = collectorReadinessError(status); err != nil {
				t.Fatalf("detached collector is not ready: %v", err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("collector did not remain available after MCP parent exit: %v", statusErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCollectorLauncherParentProcess(t *testing.T) {
	if os.Getenv("SCRATCHPAD_LAUNCH_PARENT") != "1" {
		return
	}
	socket := os.Getenv("SCRATCHPAD_LAUNCH_SOCKET")
	executable := os.Getenv("SCRATCHPAD_LAUNCH_EXECUTABLE")
	client := localcapture.NewClient(socket)
	client.Timeout = 100 * time.Millisecond
	starter := func(socket string) (<-chan error, error) { return launchCollectorProcess(executable, socket) }
	if err := ensureCollectorWithClient(context.Background(), socket, client, starter); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Fprintln(os.Stdout, "parent-ready")
	os.Exit(0)
}

func TestCollectorFixtureChildProcess(t *testing.T) {
	if os.Getenv("SCRATCHPAD_FIXTURE_CHILD") != "1" {
		return
	}
	socket := os.Getenv("SCRATCHPAD_FIXTURE_SOCKET")
	if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		_ = os.WriteFile(os.Getenv("SCRATCHPAD_FIXTURE_PID_FILE")+".error", []byte(err.Error()), 0600)
		t.Fatal(err)
	}
	if err = os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(os.Getenv("SCRATCHPAD_FIXTURE_PID_FILE"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, "collector-stdout-sentinel")
	fmt.Fprintln(os.Stderr, "collector-stderr-sentinel")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		connection, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			if ctx.Err() != nil {
				return
			}
			t.Fatal(acceptErr)
		}
		go serveFixtureStatus(connection)
	}
}

func serveFixtureStatus(connection net.Conn) {
	defer connection.Close()
	var size [4]byte
	if _, err := io.ReadFull(connection, size[:]); err != nil {
		return
	}
	length := binary.BigEndian.Uint32(size[:])
	if length == 0 || length > localcapture.MaxFrameBytes {
		return
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(connection, payload); err != nil {
		return
	}
	var request localcapture.Request
	if json.Unmarshal(payload, &request) != nil || request.Op != localcapture.OpStatus {
		return
	}
	result, err := json.Marshal(readyCollector(localcapture.LifecycleRunning))
	if err != nil {
		return
	}
	response, err := json.Marshal(localcapture.Response{Version: request.Version, ID: request.ID, Result: result})
	if err != nil {
		return
	}
	binary.BigEndian.PutUint32(size[:], uint32(len(response)))
	_, _ = connection.Write(append(size[:], response...))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
