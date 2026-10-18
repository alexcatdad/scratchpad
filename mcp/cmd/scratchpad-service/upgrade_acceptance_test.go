package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
)

const upgradeAcceptanceKeyID = "synthetic-service-test"

func TestHomebrewUpgradeAcceptanceHandoffPreservesQueueAndLifecycle(t *testing.T) {
	bin := buildServiceForProcessTest(t)
	for _, initialLifecycle := range []string{"running", "paused", "stopped"} {
		t.Run(initialLifecycle, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			if err := os.MkdirAll(home, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			xdgHome := filepath.Join(root, "xdg-config")
			if err := os.MkdirAll(xdgHome, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("XDG_CONFIG_HOME", xdgHome)

			database := filepath.Join(root, "state", "captures.sqlite")
			socketRoot := shortProcessTempDir(t)
			socket := filepath.Join(socketRoot, "service.sock")
			for _, dir := range []string{filepath.Dir(database), filepath.Dir(socket)} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			keyPath := filepath.Join(root, "queue-key.synthetic")
			key := strings.Repeat("k", 32)
			if err := os.WriteFile(keyPath, []byte(key), 0600); err != nil {
				t.Fatal(err)
			}
			keyHelper := filepath.Join(root, "key-helper.synthetic.sh")
			keyCalls := filepath.Join(root, "key-helper.calls")
			keyHelperScript := fmt.Sprintf(`#!/bin/sh
set -eu
printf "%%s %%s\\n" "$1" "$2" >> "$SCRATCHPAD_UPGRADE_TEST_CALLS"
[ "$#" = 2 ] || exit 1
[ "$1" = get ] || [ "$1" = create ] || exit 1
[ "$2" = %q ] || exit 1
exec /bin/cat "$SCRATCHPAD_UPGRADE_TEST_KEY"
`, upgradeAcceptanceKeyID)
			if err := os.WriteFile(keyHelper, []byte(keyHelperScript), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SCRATCHPAD_UPGRADE_TEST_KEY", keyPath)
			t.Setenv("SCRATCHPAD_UPGRADE_TEST_CALLS", keyCalls)
			service := startUpgradeAcceptanceService(t, bin, database, socket, keyHelper, false)
			if err := startTrackedService(t, service); err != nil {
				t.Fatal(err)
			}
			exits := watchUpgradeAcceptanceService(service)
			client := localcapture.NewClient(socket)
			client.Timeout = 5 * time.Second
			waitForUpgradeLifecycle(t, client, service, localcapture.LifecycleRunning)

			config := localcapture.ServiceConfiguration{
				ServerURL:             "https://127.0.0.1:1",
				CredentialFingerprint: "SHA256:synthetic-upgrade-test",
				PublicKeyPath:         filepath.Join(root, "unused.pub"),
				SigningKeyPath:        filepath.Join(root, "unused.key"),
			}
			if err := client.SetConfiguration(context.Background(), config); err != nil {
				t.Fatalf("set synthetic local-only configuration: %v", err)
			}
			if initialLifecycle == "paused" {
				if err := client.PauseDelivery(context.Background()); err != nil {
					t.Fatalf("pause before replacement: %v", err)
				}
			}

			requestID := "request-homebrew-upgrade-" + initialLifecycle
			capture, err := client.Capture(context.Background(), localcapture.CaptureRequest{
				RequestID: requestID,
				Record: localcapture.Record{
					Type:       "finding",
					Title:      "synthetic upgrade acceptance receipt",
					Authority:  "observed",
					Confidence: "high",
					Payload:    json.RawMessage(`{"fixture":"homebrew-upgrade-acceptance"}`),
				},
				Scope:         localcapture.Scope{ProjectID: "proj_synthetic_upgrade"},
				ClientSavedAt: time.Now().UTC().Format(time.RFC3339Nano),
				Binding:       localcapture.Binding{ServerURL: config.ServerURL, CredentialFingerprint: config.CredentialFingerprint},
			})
			if err != nil {
				t.Fatalf("save synthetic pending receipt: %v process=%s", err, service.output.String())
			}
			if capture.Outcome != localcapture.OutcomeSavedPending || capture.Receipt.ReceiptID == "" || capture.Receipt.State == localcapture.ReceiptDelivered {
				t.Fatalf("capture was not retained as pending: outcome=%s receipt=%+v", capture.Outcome, capture.Receipt)
			}
			if initialLifecycle == "stopped" {
				if err := client.Stop(context.Background()); err != nil {
					t.Fatalf("stop before replacement: %v", err)
				}
				awaitUpgradeAcceptanceExit(t, exits)
			}

			releaseBrewLock, err := localcapture.AcquireHomebrewUpgradeLock()
			if err != nil {
				t.Fatalf("acquire synthetic Homebrew transaction lock: %v", err)
			}
			brewLockActive := true
			defer func() {
				if brewLockActive {
					releaseBrewLock()
				}
			}()
			prepareArgs := []string{"--prepare-upgrade", "--database", database, "--socket", socket, "--key-helper", keyHelper, "--key-identifier", upgradeAcceptanceKeyID}
			if output, err := runUpgradeAcceptanceCommand(bin, prepareArgs...); err != nil {
				t.Fatalf("prepare replacement: %v: %s", err, output)
			}
			if initialLifecycle != "stopped" {
				awaitUpgradeAcceptanceExit(t, exits)
			}
			statePath, err := localcapture.UpgradeStatePath()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(statePath); err != nil {
				t.Fatalf("prepare did not leave recovery marker: %v", err)
			}
			if initialLifecycle == "paused" {
				// Simulate a process that bypassed the ordinary-start fence after
				// prepare. Retrying prepare must quiesce it while keeping the saved
				// pause intent from the original marker.
				resumedService := startUpgradeAcceptanceService(t, bin, database, socket, keyHelper, true)
				if err := startTrackedService(t, resumedService); err != nil {
					t.Fatalf("start synthetic service during replacement: %v", err)
				}
				resumedExits := watchUpgradeAcceptanceService(resumedService)
				waitForUpgradeLifecycle(t, client, resumedService, localcapture.LifecyclePaused)
				if output, err := runUpgradeAcceptanceCommand(bin, prepareArgs...); err != nil {
					t.Fatalf("repeated prepare did not drain the service: %v: %s", err, output)
				}
				awaitUpgradeAcceptanceExit(t, resumedExits)
			} else if output, err := runUpgradeAcceptanceCommand(bin, prepareArgs...); err != nil {
				t.Fatalf("repeated prepare did not preserve the existing handoff: %v: %s", err, output)
			}
			state, stateErr := localcapture.ReadUpgradeState()
			if stateErr != nil || state == nil || state.Restart != (initialLifecycle != "stopped") {
				t.Fatalf("repeated prepare changed the original restart intent: state=%+v err=%v", state, stateErr)
			}
			pendingFinishArgs := []string{"--finish-upgrade", "--database", database, "--socket", socket, "--key-helper", keyHelper, "--key-identifier", upgradeAcceptanceKeyID}
			if output, finishErr := runUpgradeAcceptanceCommand(bin, pendingFinishArgs...); finishErr == nil {
				t.Fatalf("finish unexpectedly ran while Homebrew still held the transaction lock: %s", output)
			} else {
				var exit *exec.ExitError
				if !errors.As(finishErr, &exit) || exit.ExitCode() != localcapture.UpgradePendingExitCode {
					t.Fatalf("finish did not report retryable Homebrew contention: err=%v output=%s", finishErr, output)
				}
			}
			if _, markerErr := os.Stat(statePath); markerErr != nil {
				t.Fatalf("finish during Homebrew transaction discarded its marker: %v", markerErr)
			}
			if _, statusErr := client.Status(context.Background()); !errors.Is(statusErr, localcapture.ErrServiceStoppedSentinel) {
				t.Fatalf("finish during Homebrew transaction restarted the collector: %v", statusErr)
			}
			releaseBrewLock()
			brewLockActive = false

			if initialLifecycle != "stopped" {
				blockedArgs := []string{"--database", database, "--socket", socket, "--key-helper", keyHelper, "--key-identifier", upgradeAcceptanceKeyID}
				output, startErr := runUpgradeAcceptanceCommand(bin, blockedArgs...)
				if startErr == nil {
					t.Fatalf("ordinary service start bypassed the active upgrade marker: %s", output)
				}
				if _, socketErr := os.Lstat(socket); !errors.Is(socketErr, os.ErrNotExist) {
					t.Fatalf("ordinary start created a socket during replacement: %v", socketErr)
				}
				failedFinishArgs := []string{"--finish-upgrade", "--database", database, "--socket", socket, "--key-helper", filepath.Join(root, "missing-helper"), "--key-identifier", upgradeAcceptanceKeyID}
				if output, finishErr := runUpgradeAcceptanceCommand(bin, failedFinishArgs...); finishErr == nil {
					t.Fatalf("finish unexpectedly recovered with a missing key helper: %s", output)
				}
				if _, markerErr := os.Stat(statePath); markerErr != nil {
					t.Fatalf("failed recovery discarded its marker: %v", markerErr)
				}

				if initialLifecycle == "paused" {
					// Keep the installed executable valid, but make the replacement
					// bundle's helper fail at the same path. This exercises finish's
					// restart and marker-retention path before restoring the old helper.
					oldHelper, readErr := os.ReadFile(keyHelper)
					if readErr != nil {
						t.Fatalf("save installed key helper for rollback fixture: %v", readErr)
					}
					if writeErr := os.WriteFile(keyHelper, []byte("#!/bin/sh\nexit 23\n"), 0700); writeErr != nil {
						t.Fatalf("install failing replacement helper: %v", writeErr)
					}
					failedReplacementFinishArgs := []string{"--finish-upgrade", "--database", database, "--socket", socket, "--key-helper", keyHelper, "--key-identifier", upgradeAcceptanceKeyID}
					if output, finishErr := runUpgradeAcceptanceCommand(bin, failedReplacementFinishArgs...); finishErr == nil {
						t.Fatalf("finish unexpectedly accepted the broken replacement helper: %s", output)
					}
					if _, markerErr := os.Stat(statePath); markerErr != nil {
						t.Fatalf("failed replacement restart discarded its recovery marker: %v", markerErr)
					}
					if writeErr := os.WriteFile(keyHelper, oldHelper, 0700); writeErr != nil {
						t.Fatalf("restore previous installed helper: %v", writeErr)
					}
				}
			}

			finishArgs := []string{"--finish-upgrade", "--database", database, "--socket", socket, "--key-helper", keyHelper, "--key-identifier", upgradeAcceptanceKeyID}
			if output, err := runUpgradeAcceptanceCommand(bin, finishArgs...); err != nil {
				t.Fatalf("finish replacement: %v: %s", err, output)
			}
			if _, err = os.Lstat(statePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("successful recovery marker was not removed: %v", err)
			}

			if initialLifecycle == "stopped" {
				if _, err = client.Status(context.Background()); !errors.Is(err, localcapture.ErrServiceStoppedSentinel) {
					t.Fatalf("finish restarted a service that was stopped before replacement: %v", err)
				}
				assertUpgradeAcceptanceReceipt(t, database, keyHelper, capture.Receipt.ReceiptID)
				command := exec.Command(bin, "--database", database, "--socket", socket, "--key-helper", keyHelper, "--key-identifier", upgradeAcceptanceKeyID)
				command.Env = os.Environ()
				output, startErr := command.CombinedOutput()
				var exit *exec.ExitError
				if !errors.As(startErr, &exit) || exit.ExitCode() != localcapture.PersistentlyStoppedStartupExitCode {
					t.Fatalf("ordinary start did not honor the persisted Stop state: err=%v output=%s", startErr, output)
				}
				return
			}

			want := localcapture.LifecycleRunning
			if initialLifecycle == "paused" {
				want = localcapture.LifecyclePaused
			}
			waitForUpgradeLifecycle(t, client, service, want)
			status, err := client.Status(context.Background())
			if err != nil || status.Pending+status.Blocked+status.NeedsSelection < 1 {
				t.Fatalf("undelivered queue was not retained after replacement: status=%+v err=%v", status, err)
			}
			receipt, err := client.LookupReceipt(context.Background(), capture.Receipt.ReceiptID)
			if err != nil || receipt.Content == nil || receipt.Content.Record.Title != "synthetic upgrade acceptance receipt" {
				t.Fatalf("receipt content did not survive replacement: receipt=%+v err=%v", receipt, err)
			}
			if err := client.Stop(context.Background()); err != nil {
				t.Fatalf("stop replacement service after acceptance: %v", err)
			}
			waitForUpgradeServiceStopped(t, client)
		})
	}
}

func startUpgradeAcceptanceService(t *testing.T, bin, database, socket, helper string, upgradeResume bool) *startedService {
	t.Helper()
	args := []string{"--database", database, "--socket", socket, "--key-helper", helper, "--key-identifier", upgradeAcceptanceKeyID}
	if upgradeResume {
		args = append([]string{"--upgrade-resume"}, args...)
	}
	output := &safeBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = output, output
	return &startedService{cmd: cmd, output: output, cancel: cancel, done: make(chan struct{})}
}

func watchUpgradeAcceptanceService(service *startedService) <-chan error {
	exits := make(chan error, 1)
	go func() { exits <- service.wait() }()
	return exits
}

func awaitUpgradeAcceptanceExit(t *testing.T, exits <-chan error) {
	t.Helper()
	select {
	case err := <-exits:
		if err != nil {
			t.Fatalf("service exited unsuccessfully during lifecycle handoff: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("service did not exit after lifecycle request")
	}
}

func waitForUpgradeServiceStopped(t *testing.T, client *localcapture.IPCClient) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, err := client.Status(context.Background())
		if errors.Is(err, localcapture.ErrServiceStoppedSentinel) {
			return
		}
		if err != nil && !errors.Is(err, localcapture.ErrStorageFailedSentinel) {
			t.Fatalf("replacement service failed while stopping: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("replacement service did not stop after Stop request")
}

func runUpgradeAcceptanceCommand(bin string, args ...string) (string, error) {
	command := exec.Command(bin, args...)
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	return string(output), err
}

func waitForUpgradeLifecycle(t *testing.T, client *localcapture.IPCClient, service *startedService, want localcapture.Lifecycle) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, err := client.Status(context.Background())
		if err == nil && status.Lifecycle == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	status, err := client.Status(context.Background())
	calls, _ := os.ReadFile(os.Getenv("SCRATCHPAD_UPGRADE_TEST_CALLS"))
	t.Fatalf("service did not reach %s: status=%+v err=%v process=%s helper-calls=%s", want, status, err, service.output.String(), calls)
}

func assertUpgradeAcceptanceReceipt(t *testing.T, database, keyHelper, receiptID string) {
	t.Helper()
	store, err := localcapture.OpenStore(database, localcapture.HelperKeyProvider{Path: keyHelper, Identifier: upgradeAcceptanceKeyID})
	if err != nil {
		t.Fatalf("open retained queue after stopped upgrade: %v", err)
	}
	defer store.Close()
	runtime, err := localcapture.NewRuntime(context.Background(), localcapture.ServiceOptions{Store: store, DeferWorker: true})
	if err != nil {
		t.Fatalf("open retained queue runtime after stopped upgrade: %v", err)
	}
	defer runtime.Close()
	receipt, err := runtime.LookupReceipt(context.Background(), receiptID)
	if err != nil || receipt.Content == nil || receipt.Content.Record.Title != "synthetic upgrade acceptance receipt" || receipt.State == localcapture.ReceiptDelivered {
		t.Fatalf("stopped upgrade lost its undelivered receipt: receipt=%+v err=%v", receipt, err)
	}
}
