package scratchpad

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Mirror uses a cross-process directory lock, checks stable record IDs, and syncs
// each append. A crashed writer leaves a lock for explicit operator recovery.
func Mirror(ctx context.Context, root, path string, record map[string]any) error {
	if filepath.IsAbs(path) || path == "." || strings.HasPrefix(filepath.Clean(path), "..") {
		return errors.New("mirror path must stay inside repository")
	}
	scoped, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer scoped.Close()
	target := filepath.Clean(path)
	if err := scoped.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	if info, e := scoped.Lstat(target); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("mirror file cannot be a symlink")
	}
	lock := target + ".lock"
	for {
		err = scoped.Mkdir(lock, 0700)
		if err == nil {
			break
		}
		if !os.IsExist(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer scoped.Remove(lock)
	f, err := scoped.OpenFile(target, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	id, ok := record["id"].(string)
	if !ok || id == "" {
		return errors.New("central record lacks stable ID")
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	for scanner.Scan() {
		var existing map[string]any
		if err = json.Unmarshal(scanner.Bytes(), &existing); err != nil {
			return fmt.Errorf("refusing malformed mirror: %w", err)
		}
		if existing["id"] == id {
			return nil
		}
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > 0 {
		var last [1]byte
		if _, err = f.ReadAt(last[:], info.Size()-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			if _, err = f.Write([]byte("\n")); err != nil {
				return err
			}
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}
