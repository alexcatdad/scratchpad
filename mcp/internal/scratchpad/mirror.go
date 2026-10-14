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
	"syscall"
	"time"
)

// Mirror uses a cross-process directory lock, checks stable record IDs, and syncs
// each append. A crashed writer leaves a lock for explicit operator recovery.
func Mirror(ctx context.Context, root, path string, record map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if info, e := scoped.Lstat(target); e == nil && !info.Mode().IsRegular() {
		return errors.New("mirror target must be a regular file")
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	lock := target + ".lock"
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
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
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := scoped.OpenFile(target, os.O_CREATE|os.O_RDWR|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() {
		return errors.New("opened mirror target must be a regular file")
	}
	current, err := scoped.Lstat(target)
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return errors.New("mirror target changed while opening")
	}
	id, ok := record["id"].(string)
	if !ok || id == "" {
		return errors.New("central record lacks stable ID")
	}
	scanner := bufio.NewScanner(mirrorReader{ctx, f})
	scanner.Buffer(make([]byte, 4096), 16<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var existing map[string]any
		if err = json.Unmarshal(scanner.Bytes(), &existing); err != nil {
			return fmt.Errorf("refusing malformed mirror: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if existing["id"] == id {
			return nil
		}
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
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
		if err := ctx.Err(); err != nil {
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	// Once the append is durably synced, report its actual successful outcome.
	return f.Sync()
}

// Check cancellation at each bounded read, including within a long JSONL line.
type mirrorReader struct {
	ctx  context.Context
	file *os.File
}

func (r mirrorReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > 32<<10 {
		p = p[:32<<10]
	}
	n, err := r.file.Read(p)
	if cancelled := r.ctx.Err(); cancelled != nil {
		return n, cancelled
	}
	return n, err
}
