package persistence

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"syscall"
)

func inspectPrivate(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe storage permissions")
	}
	if directory {
		if !info.IsDir() {
			return errors.New("unsafe directory")
		}
	} else if !info.Mode().IsRegular() || stat.Nlink != 1 {
		return errors.New("unsafe database file")
	}
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("/bin/ls", "-lde", "--", path).Output()
		if err != nil {
			// Another SQLite process may close and remove a sidecar between
			// lstat and the ACL query. Only optional sidecar callers accept absence.
			if _, missing := os.Lstat(path); errors.Is(missing, os.ErrNotExist) {
				return missing
			}
			return errors.New("unsafe storage ACL")
		}
		if regexp.MustCompile(`(?m)^\s*\d+: `).Match(out) {
			return errors.New("unsafe storage ACL")
		}
	}
	return nil
}
func privateSQLite(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := inspectPrivate(dir, true); err != nil {
		return err
	}
	// Do not open/close an existing inode: POSIX close could release SQLite locks.
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err == nil {
		syscall.Close(fd)
	} else if !errors.Is(err, syscall.EEXIST) {
		return err
	}
	if err := inspectPrivate(path, false); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := inspectPrivate(path+suffix, false); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
