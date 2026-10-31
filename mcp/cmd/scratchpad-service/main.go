package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/alexcatdad/scratchpad/mcp/internal/delivery"
	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
)

const keyIdentifier = "com.alexcatdad.scratchpad.capture-queue.v1"

// Release builds inject these values with the same -X convention as scratchpad-mcp.
var version = "dev"
var buildNumber = "dev"
var errPersistentlyStopped = errors.New("service is persistently stopped")

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("scratchpad-service %s (build %s; queue-schema %d)\n", version, buildNumber, localcapture.QueueSchemaVersion)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, safeStartupMessage(err))
		if errors.Is(err, localcapture.ErrUpgradePendingSentinel) {
			os.Exit(localcapture.UpgradePendingExitCode)
		}
		if errors.Is(err, errPersistentlyStopped) {
			os.Exit(localcapture.PersistentlyStoppedStartupExitCode)
		}
		os.Exit(1)
	}
}

func safeStartupMessage(err error) string {
	switch {
	case errors.Is(err, localcapture.ErrKeyUnlockRequiredSentinel):
		return "Scratchpad local service is waiting for the login keychain to be unlocked."
	case errors.Is(err, localcapture.ErrKeyUnavailableSentinel):
		return "Scratchpad local service could not unlock its encrypted capture queue."
	case errors.Is(err, localcapture.ErrConflictSentinel):
		return "Scratchpad local service could not acquire its socket; another instance may be active."
	case errors.Is(err, localcapture.ErrUpgradePendingSentinel):
		return "Scratchpad app replacement is still in progress; retry after Homebrew finishes."
	case errors.Is(err, errPersistentlyStopped):
		return "Scratchpad local service is stopped by its saved lifecycle setting."
	case errors.Is(err, localcapture.ErrStorageFailedSentinel):
		return "Scratchpad local service could not open its private capture storage."
	default:
		return "Scratchpad local service could not start or stopped safely."
	}
}

func run() error {
	defaultDB, err := defaultDatabasePath()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	defaultHelper := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "Helpers", "scratchpad-keychain.app", "Contents", "MacOS", "scratchpad-keychain"))
	dbPath := flag.String("database", defaultDB, "")
	socketPath := flag.String("socket", localcapture.DefaultSocketPath(), "")
	helperPath := flag.String("key-helper", defaultHelper, "")
	identifier := flag.String("key-identifier", keyIdentifier, "")
	upgradeServicePath := flag.String("upgrade-service-path", "", "")
	explicitStart := flag.Bool("explicit-start", false, "")
	prepareUpgradeFlag := flag.Bool("prepare-upgrade", false, "")
	finishUpgradeFlag := flag.Bool("finish-upgrade", false, "")
	upgradeResume := flag.Bool("upgrade-resume", false, "")
	printSchema := flag.Bool("schema-version", false, "")
	flag.Parse()
	if *prepareUpgradeFlag && *finishUpgradeFlag {
		return localcapture.ErrConflictSentinel
	}
	if *prepareUpgradeFlag {
		servicePath := executable
		if *upgradeServicePath != "" {
			servicePath = *upgradeServicePath
		}
		return prepareUpgrade(*socketPath, *dbPath, *helperPath, *identifier, servicePath)
	}
	if *finishUpgradeFlag {
		return finishUpgrade(*socketPath, *dbPath, *helperPath, *identifier, executable)
	}
	if *upgradeResume {
		state, stateErr := localcapture.ReadUpgradeState()
		if stateErr != nil || state == nil || !state.Restart {
			return localcapture.ErrConflictSentinel
		}
	}
	if *printSchema {
		version, inspectErr := localcapture.InspectSchemaVersion(*dbPath)
		if inspectErr != nil {
			return inspectErr
		}
		fmt.Fprintln(os.Stdout, version)
		return nil
	}
	if !*upgradeResume {
		upgradeInProgress, checkErr := localcapture.UpgradeInProgress()
		if checkErr != nil || upgradeInProgress {
			return localcapture.ErrConflictSentinel
		}
	}
	startup, err := localcapture.AcquireStartupGuard(*socketPath)
	if err != nil {
		return err
	}
	defer startup.Close()
	if err = startup.Prepare(); err != nil {
		return err
	}
	upgradeInProgress, err := localcapture.UpgradeInProgress()
	if err != nil || (upgradeInProgress && !*upgradeResume) {
		return localcapture.ErrConflictSentinel
	}

	store, err := localcapture.OpenStore(*dbPath, localcapture.HelperKeyProvider{Path: *helperPath, Identifier: *identifier})
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	runtime, err := localcapture.NewRuntime(ctx, localcapture.ServiceOptions{
		Store: store, Adapter: delivery.NewAdapter(), ExplicitStart: *explicitStart, ExitAfterQuiesce: true, DeferWorker: true,
		Version: version, Build: buildNumber,
	})
	if err != nil {
		_ = store.Close()
		return err
	}
	if runtime.InitiallyStopped() {
		runtime.Close()
		return errPersistentlyStopped
	}
	server, err := startup.ListenIPC(runtime)
	if err != nil {
		runtime.Close()
		return err
	}
	if err = startup.Close(); err != nil {
		_ = server.Close()
		runtime.Close()
		return err
	}
	if err = runtime.StartWorker(ctx); err != nil {
		_ = server.Close()
		runtime.Close()
		return err
	}
	serveErr := server.Serve(ctx)
	runtime.Close()
	if serveErr != nil && ctx.Err() == nil {
		return serveErr
	}
	return nil
}

func defaultDatabasePath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Scratchpad", "captures.sqlite"), nil
}
