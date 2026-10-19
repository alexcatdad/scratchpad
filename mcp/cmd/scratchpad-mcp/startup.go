package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
)

const (
	collectorStartupTimeout = 12 * time.Second
	collectorPollInterval   = 100 * time.Millisecond
	collectorProbeTimeout   = 300 * time.Millisecond
	collectorMaxStarts      = 3
	collectorSystemPath     = "/usr/bin:/bin:/usr/sbin:/sbin"
)

type collectorStatusClient interface {
	Status(context.Context) (localcapture.ServiceStatus, error)
}

type collectorStarter func(socketPath string) (<-chan error, error)

func launchCollector(socketPath string) (<-chan error, error) {
	executable, err := resolveCollectorExecutable()
	if err != nil {
		return nil, err
	}
	return launchCollectorProcess(executable, socketPath)
}

func launchCollectorProcess(executable, socketPath string) (<-chan error, error) {
	args := make([]string, 0, 2)
	if socketPath != "" {
		args = append(args, "--socket", socketPath)
	}
	command := exec.Command(executable, args...)
	command.Env = collectorEnvironment()
	// A long-lived child must not keep a stdio MCP client's pipes open.
	// nil stdout/stderr connect to the null device and cannot pollute MCP stdout.
	command.Stdout = nil
	command.Stderr = nil
	if err := detachCollector(command); err != nil {
		return nil, fmt.Errorf("configure Scratchpad collector process: %w", err)
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start Scratchpad collector: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	return done, nil
}

func ensureCollector(ctx context.Context, socketPath string, start collectorStarter) error {
	client := localcapture.NewClient(socketPath)
	client.Timeout = collectorProbeTimeout
	return ensureCollectorWithClient(ctx, socketPath, client, start)
}

func ensureCollectorWithClient(ctx context.Context, socketPath string, client collectorStatusClient, start collectorStarter) error {
	upgrading, upgradeErr := localcapture.UpgradeInProgress()
	if upgradeErr != nil || upgrading {
		return errors.New("Scratchpad collector is being replaced; retry after the app upgrade completes.")
	}
	startupContext, cancel := context.WithTimeout(ctx, collectorStartupTimeout)
	defer cancel()
	ctx = startupContext
	status, err := client.Status(ctx)
	if err == nil {
		return collectorReadinessError(status)
	}
	if !errors.Is(err, localcapture.ErrServiceStoppedSentinel) {
		return fmt.Errorf("Scratchpad collector is unavailable; open Scratchpad to inspect its service status: %w", err)
	}

	finished, err := start(socketPath)
	if err != nil {
		return fmt.Errorf("could not start the bundled Scratchpad collector: %w", err)
	}
	deadline, _ := ctx.Deadline()
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return errors.New("timed out waiting for the Scratchpad collector; open Scratchpad to inspect its service status.")
	}
	return waitForCollector(ctx, socketPath, client, start, finished, remaining, collectorPollInterval)
}

func waitForCollector(ctx context.Context, socketPath string, client collectorStatusClient, start collectorStarter, finished <-chan error, timeout, interval time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	starts := 1
	var childExit error
	childExited := false
	for {
		status, err := client.Status(ctx)
		if err == nil {
			return collectorReadinessError(status)
		}
		select {
		case exitErr, ok := <-finished:
			finished = nil
			if ok {
				if exitCode, hasExitCode := childProcessExitCode(exitErr); hasExitCode && exitCode == localcapture.PersistentlyStoppedStartupExitCode {
					return errors.New("Scratchpad collection is stopped by its saved setting. Start the service from the tray menu to capture through MCP.")
				}
				childExited = true
				childExit = exitErr
			}
			// A competing MCP client may have won startup. Continue probing even
			// when this child failed to acquire the database or socket.
		case <-ticker.C:
			if finished == nil && childExited && childExit != nil && starts < collectorMaxStarts {
				finished, childExit = nil, nil
				childExited = false
				var startErr error
				finished, startErr = start(socketPath)
				if startErr != nil {
					return fmt.Errorf("could not retry starting the bundled Scratchpad collector: %w", startErr)
				}
				starts++
			}
		case <-deadline.C:
			if childExited && childExit != nil {
				return errors.New("Scratchpad collector did not become ready; open Scratchpad to inspect its service status or unlock the login keychain.")
			}
			return errors.New("timed out waiting for the Scratchpad collector; collection may be stopped or the service may need attention.")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func childProcessExitCode(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var exited interface{ ExitCode() int }
	if !errors.As(err, &exited) {
		return 0, false
	}
	return exited.ExitCode(), true
}

func collectorReadinessError(status localcapture.ServiceStatus) error {
	switch status.Lifecycle {
	case localcapture.LifecycleRunning, localcapture.LifecyclePaused:
	default:
		if status.Lifecycle == localcapture.LifecycleStopped {
			return errors.New("Scratchpad collection is stopped. Start the service from the tray menu to capture through MCP.")
		}
		return errors.New("Scratchpad collector is restarting or unavailable; retry after the tray service is ready.")
	}
	if !status.KeyAvailable || !status.StorageAvailable {
		return errors.New("Scratchpad collector cannot access its encrypted capture storage; open Scratchpad to inspect the service status.")
	}
	if status.QueueSchemaVersion != localcapture.QueueSchemaVersion {
		return errors.New("Scratchpad collector and MCP versions are incompatible; update Scratchpad before capturing.")
	}
	return nil
}

func resolveCollectorExecutable() (string, error) {
	mcpExecutable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the bundled Scratchpad MCP executable: %w", err)
	}
	mcpExecutable, err = filepath.EvalSymlinks(mcpExecutable)
	if err != nil {
		return "", fmt.Errorf("resolve the bundled Scratchpad MCP executable: %w", err)
	}
	return bundledCollectorExecutable(mcpExecutable)
}

func bundledCollectorExecutable(mcpExecutable string) (string, error) {
	return verifyCollectorExecutable(filepath.Join(filepath.Dir(mcpExecutable), "scratchpad-service"))
}

func verifyCollectorExecutable(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve Scratchpad collector path: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("find the bundled Scratchpad collector at %s: %w", absolute, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("bundled Scratchpad collector is not an executable regular file: %s", absolute)
	}
	return absolute, nil
}

func collectorEnvironment() []string {
	environment := []string{"PATH=" + collectorSystemPath}
	for _, name := range []string{"HOME", "USER", "LOGNAME", "TMPDIR", "XDG_CONFIG_HOME", "XDG_RUNTIME_DIR", "SSH_AUTH_SOCK"} {
		if value, ok := os.LookupEnv(name); ok {
			if name == "SSH_AUTH_SOCK" && value == "" {
				continue
			}
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}
