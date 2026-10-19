package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
)

const upgradeWaitTimeout = 120 * time.Second

func prepareUpgrade(socketPath, databasePath, helperPath, keyIdentifier, servicePath string) error {
	var err error
	socketPath, err = filepath.Abs(socketPath)
	if err != nil {
		return localcapture.ErrStorageFailedSentinel
	}
	databasePath, err = filepath.Abs(databasePath)
	if err != nil {
		return localcapture.ErrStorageFailedSentinel
	}
	servicePath, err = filepath.Abs(servicePath)
	if err != nil {
		return localcapture.ErrStorageFailedSentinel
	}
	helperPath, err = filepath.Abs(helperPath)
	if err != nil {
		return localcapture.ErrStorageFailedSentinel
	}

	guard, err := localcapture.AcquireStartupGuard(socketPath)
	if err != nil {
		return err
	}
	state := localcapture.UpgradeState{
		Version: 1, ServicePath: servicePath, SocketPath: socketPath,
		DatabasePath: databasePath, KeyIdentifier: keyIdentifier,
	}
	existing, stateErr := localcapture.ReadUpgradeState()
	if stateErr != nil {
		_ = guard.Close()
		return stateErr
	}
	if existing != nil {
		if existing.ServicePath != state.ServicePath || existing.SocketPath != state.SocketPath || existing.DatabasePath != state.DatabasePath || existing.KeyIdentifier != state.KeyIdentifier {
			_ = guard.Close()
			return errors.New("upgrade marker does not match this Scratchpad installation")
		}
		// A prior prepare may have written the marker and then been interrupted
		// before quiescing. Preserve the original lifecycle decision, but continue
		// inspecting and draining the service on every retry.
		state = *existing
	}
	quiesceActive := false
	client := localcapture.NewClient(socketPath)
	client.Timeout = 10 * time.Second
	if status, statusErr := client.Status(context.Background()); statusErr == nil {
		switch status.Lifecycle {
		case localcapture.LifecycleRunning, localcapture.LifecyclePaused:
			if existing == nil {
				state.Restart = true
			}
			quiesceActive = true
		case localcapture.LifecycleStopped:
			if existing == nil {
				state.Restart = false
			}
			quiesceActive = true
		default:
			_ = guard.Close()
			return localcapture.ErrConflictSentinel
		}
	} else if errors.Is(statusErr, localcapture.ErrServiceStoppedSentinel) {
		// A stale socket is safe to remove while holding the same lock used by
		// service startup. A missing socket records no active process to restart.
		if err = guard.Prepare(); err != nil {
			_ = guard.Close()
			return err
		}
	} else {
		_ = guard.Close()
		return statusErr
	}
	if existing == nil {
		if err = localcapture.WriteUpgradeState(state); err != nil {
			_ = guard.Close()
			return err
		}
	}
	if err = guard.Close(); err != nil {
		_ = localcapture.ClearUpgradeState()
		return err
	}
	if !quiesceActive {
		return nil
	}

	client.Timeout = upgradeWaitTimeout
	ctx, cancel := context.WithTimeout(context.Background(), upgradeWaitTimeout)
	defer cancel()
	recoverPrepare := func(cause error) error {
		_ = finishUpgrade(socketPath, databasePath, helperPath, keyIdentifier, servicePath)
		return cause
	}
	if err = client.Quiesce(ctx); err != nil {
		return recoverPrepare(fmt.Errorf("could not quiesce the collector for replacement: %w", err))
	}
	for {
		_, statusErr := client.Status(ctx)
		if errors.Is(statusErr, localcapture.ErrServiceStoppedSentinel) {
			return nil
		}
		if statusErr != nil && !errors.Is(statusErr, localcapture.ErrStorageFailedSentinel) && !errors.Is(statusErr, localcapture.ErrServiceQuiescingSentinel) {
			return recoverPrepare(fmt.Errorf("could not confirm collector shutdown: %w", statusErr))
		}
		select {
		case <-ctx.Done():
			return recoverPrepare(errors.New("timed out waiting for the collector to exit after quiescing"))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func finishUpgrade(socketPath, databasePath, helperPath, keyIdentifier, servicePath string) error {
	releaseLock, err := localcapture.AcquireUpgradeFinishLock()
	if err != nil {
		return err
	}
	defer releaseLock()
	state, err := localcapture.ReadUpgradeState()
	if err != nil {
		return err
	}
	if state == nil {
		return nil
	}
	releaseHomebrewLock, err := localcapture.AcquireHomebrewUpgradeLock()
	if err != nil {
		return err
	}
	defer releaseHomebrewLock()
	currentService, err := filepath.Abs(servicePath)
	if err != nil {
		return localcapture.ErrStorageFailedSentinel
	}
	socketPath, err = filepath.Abs(socketPath)
	if err != nil {
		return localcapture.ErrStorageFailedSentinel
	}
	databasePath, err = filepath.Abs(databasePath)
	if err != nil {
		return localcapture.ErrStorageFailedSentinel
	}
	helperPath, err = filepath.Abs(helperPath)
	if err != nil || currentService != state.ServicePath || socketPath != state.SocketPath || databasePath != state.DatabasePath || keyIdentifier != state.KeyIdentifier {
		return errors.New("upgrade marker does not match this Scratchpad installation")
	}
	if !state.Restart {
		return localcapture.ClearUpgradeState()
	}

	client := localcapture.NewClient(socketPath)
	client.Timeout = 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		status, statusErr := client.Status(ctx)
		if statusErr == nil && (status.Lifecycle == localcapture.LifecycleRunning || status.Lifecycle == localcapture.LifecyclePaused) {
			if state.Restart {
				return localcapture.ClearUpgradeState()
			}
			// The service was stopped before replacement. If something started it
			// during the handoff, quiesce it before removing the auto-start gate.
			client.Timeout = upgradeWaitTimeout
			if err = client.Quiesce(ctx); err != nil {
				return fmt.Errorf("could not quiesce a collector started during replacement: %w", err)
			}
			client.Timeout = time.Second
			continue
		}
		if statusErr == nil && status.Lifecycle == localcapture.LifecycleStopped {
			// Saved Stop takes precedence; wait for its already-committed shutdown.
		} else if statusErr != nil && !errors.Is(statusErr, localcapture.ErrServiceStoppedSentinel) && !errors.Is(statusErr, localcapture.ErrServiceQuiescingSentinel) {
			return statusErr
		}
		if err = waitForServiceExit(ctx, client); err == nil {
			break
		} else if !errors.Is(err, localcapture.ErrConflictSentinel) {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.New("collector did not finish its previous shutdown; retry --finish-upgrade")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !state.Restart {
		return localcapture.ClearUpgradeState()
	}

	command := exec.Command(servicePath, "--upgrade-resume", "--socket", socketPath, "--database", databasePath, "--key-helper", helperPath, "--key-identifier", keyIdentifier)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.Stdin, command.Stdout, command.Stderr = nil, nil, nil
	if err = command.Start(); err != nil {
		return fmt.Errorf("could not start the replacement collector: %w", err)
	}
	processDone := make(chan error, 1)
	go func() { processDone <- command.Wait() }()

	startCtx, startCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer startCancel()
	client.Timeout = time.Second
	for {
		if status, statusErr := client.Status(startCtx); statusErr == nil {
			if status.Lifecycle == localcapture.LifecycleRunning || status.Lifecycle == localcapture.LifecyclePaused {
				return localcapture.ClearUpgradeState()
			}
			if status.Lifecycle == localcapture.LifecycleStopped {
				return localcapture.ClearUpgradeState()
			}
		}
		select {
		case processErr := <-processDone:
			if code, ok := childProcessExitCode(processErr); ok && code == localcapture.PersistentlyStoppedStartupExitCode {
				return localcapture.ClearUpgradeState()
			}
			return errors.New("replacement collector did not start; retry --finish-upgrade after repairing the installation")
		case <-startCtx.Done():
			return errors.New("replacement collector did not become ready; retry --finish-upgrade after repairing the installation")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitForServiceExit(ctx context.Context, client *localcapture.IPCClient) error {
	for {
		status, err := client.Status(ctx)
		if errors.Is(err, localcapture.ErrServiceStoppedSentinel) {
			return nil
		}
		if err == nil {
			if status.Lifecycle == localcapture.LifecycleRunning || status.Lifecycle == localcapture.LifecyclePaused {
				return localcapture.ErrConflictSentinel
			}
			if status.Lifecycle != localcapture.LifecycleStopped && status.Lifecycle != localcapture.LifecycleQuiescing {
				return localcapture.ErrConflictSentinel
			}
		} else if !errors.Is(err, localcapture.ErrServiceQuiescingSentinel) && !errors.Is(err, localcapture.ErrStorageFailedSentinel) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
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
