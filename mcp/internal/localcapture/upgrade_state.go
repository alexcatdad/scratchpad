package localcapture

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const upgradeStateVersion = 1

// UpgradeState records only the service identity and whether it was active
// before an application replacement. It never contains queue data or keys.
type UpgradeState struct {
	Version       int    `json:"version"`
	ServicePath   string `json:"servicePath"`
	SocketPath    string `json:"socketPath"`
	DatabasePath  string `json:"databasePath"`
	KeyIdentifier string `json:"keyIdentifier"`
	Restart       bool   `json:"restart"`
}

// UpgradeStatePath returns the private per-user marker shared by the collector
// and MCP. Its location is stable across Homebrew app replacements.
func UpgradeStatePath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil || root == "" {
		return "", ErrStorageFailedSentinel
	}
	return filepath.Join(root, "Scratchpad", "upgrade-in-progress.json"), nil
}

// UpgradeInProgress fails closed when the marker exists or cannot be safely
// inspected. MCP callers must not auto-start a collector during replacement.
func UpgradeInProgress() (bool, error) {
	path, err := UpgradeStatePath()
	if err != nil {
		return false, err
	}
	_, err = readUpgradeState(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, ErrStorageFailedSentinel
	}
	return true, nil
}

// WriteUpgradeState creates the marker without replacing an existing upgrade
// attempt. The caller must hold the collector startup lock for this socket.
func WriteUpgradeState(state UpgradeState) error {
	path, err := UpgradeStatePath()
	if err != nil {
		return err
	}
	return writeUpgradeState(path, state)
}

// ReadUpgradeState returns nil when no upgrade is pending. Unsafe or malformed
// state is an error so callers cannot accidentally restart the old executable.
func ReadUpgradeState() (*UpgradeState, error) {
	path, err := UpgradeStatePath()
	if err != nil {
		return nil, err
	}
	state, err := readUpgradeState(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrStorageFailedSentinel
	}
	return &state, nil
}

// ClearUpgradeState removes only an owner-private regular marker file.
func ClearUpgradeState() error {
	path, err := UpgradeStatePath()
	if err != nil {
		return err
	}
	return removeUpgradeState(path)
}

// AcquireUpgradeFinishLock serializes postflight/recovery attempts so one
// finisher cannot clear a marker while another is still bringing the service
// back. The returned release function must be called once.
func AcquireUpgradeFinishLock() (func(), error) {
	path, err := UpgradeStatePath()
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil || securePrivateDirectory(dir) != nil {
		return nil, ErrStorageFailedSentinel
	}
	lockPath := filepath.Join(dir, "upgrade-finish.lock")
	fd, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, ErrStorageFailedSentinel
	}
	file := os.NewFile(uintptr(fd), lockPath)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ownedByCurrentUser(info) {
		_ = file.Close()
		return nil, ErrStorageFailedSentinel
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrConflictSentinel
		}
		return nil, ErrStorageFailedSentinel
	}
	return func() {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func writeUpgradeState(path string, state UpgradeState) error {
	if state.Version != upgradeStateVersion || !filepath.IsAbs(state.ServicePath) || !filepath.IsAbs(state.SocketPath) || !filepath.IsAbs(state.DatabasePath) || state.KeyIdentifier == "" {
		return ErrConflictSentinel
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil || securePrivateDirectory(dir) != nil {
		return ErrStorageFailedSentinel
	}
	data, err := json.Marshal(state)
	if err != nil {
		return ErrStorageFailedSentinel
	}
	file, err := os.CreateTemp(dir, ".upgrade-state-*")
	if err != nil {
		return ErrStorageFailedSentinel
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return ErrStorageFailedSentinel
	}
	// Linking the complete temporary file publishes the marker atomically and
	// refuses to overwrite a previous interrupted upgrade attempt.
	if err = os.Link(tempPath, path); err != nil {
		return ErrStorageFailedSentinel
	}
	return nil
}

func readUpgradeState(path string) (UpgradeState, error) {
	dir := filepath.Dir(path)
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return UpgradeState{}, os.ErrNotExist
	} else if err != nil || verifyPrivateDirectory(dir) != nil {
		return UpgradeState{}, ErrStorageFailedSentinel
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return UpgradeState{}, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ownedByCurrentUser(info) || info.Size() > 4096 {
		return UpgradeState{}, ErrStorageFailedSentinel
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return UpgradeState{}, ErrStorageFailedSentinel
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var state UpgradeState
	if decoder.Decode(&state) != nil || state.Version != upgradeStateVersion || !filepath.IsAbs(state.ServicePath) || !filepath.IsAbs(state.SocketPath) || !filepath.IsAbs(state.DatabasePath) || state.KeyIdentifier == "" {
		return UpgradeState{}, ErrStorageFailedSentinel
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return UpgradeState{}, ErrStorageFailedSentinel
	}
	return state, nil
}

func removeUpgradeState(path string) error {
	if verifyPrivateDirectory(filepath.Dir(path)) != nil {
		return ErrStorageFailedSentinel
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || !ownedByCurrentUser(info) {
		return ErrStorageFailedSentinel
	}
	if err = os.Remove(path); err != nil {
		return ErrStorageFailedSentinel
	}
	return nil
}
