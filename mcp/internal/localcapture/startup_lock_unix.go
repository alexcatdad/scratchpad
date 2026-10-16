//go:build darwin || linux

package localcapture

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// StartupGuard serializes service initialization for one socket. It protects
// key/database creation and stale-socket cleanup until the listener is bound.
type StartupGuard struct {
	socketPath string
	lockFile   *os.File
	prepared   bool
	closed     bool
}

func AcquireStartupGuard(path string) (*StartupGuard, error) {
	if path == "" {
		path = DefaultSocketPath()
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrStorageFailedSentinel
	}
	dir := filepath.Dir(abs)
	if err = os.MkdirAll(dir, 0700); err != nil || securePrivateDirectory(dir) != nil {
		return nil, ErrStorageFailedSentinel
	}
	fd, err := syscall.Open(abs+".startup-lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, ErrStorageFailedSentinel
	}
	file := os.NewFile(uintptr(fd), abs+".startup-lock")
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ownedByCurrentUser(info) {
		_ = file.Close()
		return nil, ErrStorageFailedSentinel
	}
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		_ = file.Close()
		return nil, ErrConflictSentinel
	}
	if err != nil {
		_ = file.Close()
		return nil, ErrStorageFailedSentinel
	}
	return &StartupGuard{socketPath: abs, lockFile: file}, nil
}

// Prepare rejects an active owner or removes its stale socket while holding
// the startup lock. Call it before opening the queue store.
func (g *StartupGuard) Prepare() error {
	if g == nil || g.closed || g.lockFile == nil {
		return ErrStorageFailedSentinel
	}
	if g.prepared {
		return nil
	}
	if fi, err := os.Lstat(g.socketPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 || fi.Mode()&os.ModeSocket == 0 || !ownedByCurrentUser(fi) {
			return ErrStorageFailedSentinel
		}
		conn, dialErr := net.DialTimeout("unix", g.socketPath, 200*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return ErrConflictSentinel
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, syscall.ENOENT) {
			return ErrStorageFailedSentinel
		}
		if err = os.Remove(g.socketPath); err != nil {
			return ErrStorageFailedSentinel
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrStorageFailedSentinel
	}
	g.prepared = true
	return nil
}

func (g *StartupGuard) Close() error {
	if g == nil || g.closed {
		return nil
	}
	g.closed = true
	if g.lockFile == nil {
		return nil
	}
	fd := int(g.lockFile.Fd())
	unlockErr := syscall.Flock(fd, syscall.LOCK_UN)
	closeErr := g.lockFile.Close()
	g.lockFile = nil
	if unlockErr != nil || closeErr != nil {
		return ErrStorageFailedSentinel
	}
	return nil
}
