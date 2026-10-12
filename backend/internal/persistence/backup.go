package persistence

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
)

// Backup deliberately bypasses Open: an operator must be able to preserve the
// pre-upgrade database before any migration is attempted.
func Backup(ctx context.Context, path, databaseURL, destination string) (int, error) {
	if databaseURL != "" {
		return 0, errors.New("BACKUP_EXTERNAL: use PostgreSQL pg_dump for a full operational backup.")
	}
	if !filepath.IsAbs(destination) {
		return 0, errors.New("VALIDATION_FAILED: backup destination must be an absolute path.")
	}
	if path == "" {
		path = "data/scratchpad.sqlite"
	}
	if path == ":memory:" {
		return 0, errors.New("BACKUP_FAILED: a persisted source database is required.")
	}
	source, err := filepath.Abs(path)
	if err != nil {
		return 0, errors.New("BACKUP_FAILED: source database is unavailable or unsafe.")
	}
	destination = filepath.Clean(destination)
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if destination == source+suffix {
			return 0, errors.New("CONFLICT: backup destination must differ from source storage files.")
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if _, err = os.Lstat(destination + suffix); err == nil {
			return 0, errors.New("CONFLICT: backup destination or sidecar already exists.")
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, errors.New("BACKUP_FAILED: backup destination is unavailable.")
		}
	}
	version, err := sqliteSnapshot(ctx, source, destination)
	if err != nil {
		return 0, errors.New("BACKUP_FAILED: source or destination is unavailable, unsafe or unsupported.")
	}
	return version, nil
}

func existingSQLite(path string) error {
	if err := inspectPrivate(filepath.Dir(path), true); err != nil {
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

func sqliteSnapshot(ctx context.Context, source, destination string) (int, error) {
	if err := existingSQLite(source); err != nil {
		return 0, err
	}
	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return 0, err
	}
	if err := inspectPrivate(dir, true); err != nil {
		return 0, err
	}
	file, err := os.CreateTemp(dir, ".scratchpad-backup-*.sqlite")
	if err != nil {
		return 0, err
	}
	staged := file.Name()
	if err = file.Close(); err != nil {
		os.Remove(staged)
		return 0, err
	}
	defer os.Remove(staged)
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(source)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return 0, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN"); err != nil {
		return 0, err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	version, err := validate(ctx, conn, "sqlite")
	if err != nil {
		return 0, err
	}
	err = conn.Raw(func(raw any) error {
		backuper, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("SQLite backup is unavailable")
		}
		backup, err := backuper.NewBackup("file:" + url.PathEscape(staged))
		if err != nil {
			return err
		}
		finished := false
		defer func() {
			if !finished {
				backup.Finish()
			}
		}()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err := backup.Step(128)
			if err != nil {
				var sqliteError *sqlite.Error
				if !errors.As(err, &sqliteError) || (sqliteError.Code()&255 != 5 && sqliteError.Code()&255 != 6) {
					return err
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(20 * time.Millisecond):
					continue
				}
			}
			if !more {
				finished = true
				return backup.Finish()
			}
		}
	})
	if err != nil {
		return 0, err
	}
	if err := inspectPrivate(staged, false); err != nil {
		return 0, err
	}
	// Verify the closed main file alone, so success never depends on a WAL file.
	verification, err := sql.Open("sqlite", "file:"+url.PathEscape(staged)+"?mode=ro&immutable=1")
	if err != nil {
		return 0, err
	}
	verification.SetMaxOpenConns(1)
	var integrity string
	err = verification.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity)
	verification.Close()
	if err != nil {
		return 0, err
	}
	if integrity != "ok" {
		return 0, errors.New("backup integrity check failed")
	}
	snapshot, err := os.OpenFile(staged, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	err = snapshot.Sync()
	snapshot.Close()
	if err != nil {
		return 0, err
	}
	// Link is an atomic no-replace publication; rename could overwrite a file
	// created after the initial destination check.
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err = os.Link(staged, destination); err != nil {
		return 0, err
	}
	if err = os.Remove(staged); err != nil {
		return 0, err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return 0, err
	}
	err = directory.Sync()
	directory.Close()
	if err != nil {
		return 0, err
	}
	return version, nil
}
