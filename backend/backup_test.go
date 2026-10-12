package backend_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func backupCommand(path, databaseURL, destination string) *exec.Cmd {
	cmd := exec.Command(binary, "backup", destination)
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_PATH="+path, "SCRATCHPAD_DATABASE_URL="+databaseURL)
	return cmd
}

func TestSQLiteBackupRefusesUnsafeOrExistingPathsWithoutChangingFiles(t *testing.T) {
	for _, mode := range []string{"relative", "same-source", "source-sidecar", "existing", "symlink-destination", "stale-sidecar", "unsafe-parent", "unsafe-source", "source-hardlink", "missing-source", "unsupported-source"} {
		t.Run(mode, func(t *testing.T) {
			path := legacyFixture(t)
			dir := t.TempDir()
			os.Chmod(dir, 0700)
			destination := filepath.Join(dir, "snapshot.sqlite")
			retained := filepath.Join(dir, "retained.txt")
			os.WriteFile(retained, []byte("retain existing file"), 0600)
			switch mode {
			case "relative":
				destination = "relative-snapshot.sqlite"
			case "same-source":
				destination = path
			case "source-sidecar":
				destination = path + "-wal"
			case "existing":
				destination = retained
			case "symlink-destination":
				if err := os.Symlink(retained, destination); err != nil {
					t.Fatal(err)
				}
			case "stale-sidecar":
				if err := os.WriteFile(destination+"-journal", []byte("retain stale sidecar"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unsafe-parent":
				os.Chmod(dir, 0755)
			case "unsafe-source":
				os.Chmod(path, 0644)
			case "source-hardlink":
				if err := os.Link(path, filepath.Join(filepath.Dir(path), "alias.sqlite")); err != nil {
					t.Fatal(err)
				}
			case "missing-source":
				path = filepath.Join(filepath.Dir(path), "missing.sqlite")
			case "unsupported-source":
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(3,'2026-10-01T00:00:00Z')`); err != nil {
					t.Fatal(err)
				}
				db.Close()
			}
			before, _ := os.ReadFile(path)
			out, err := backupCommand(path, "", destination).CombinedOutput()
			if err == nil {
				t.Fatalf("unsafe backup accepted: %s", mode)
			}
			if strings.Contains(string(out), "synthetic-credential") {
				t.Fatal("backup leaked configuration")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("failed backup changed its source")
			}
			data, err := os.ReadFile(retained)
			if err != nil || string(data) != "retain existing file" {
				t.Fatal("failed backup overwrote an existing file")
			}
			if mode == "stale-sidecar" {
				data, err := os.ReadFile(destination + "-journal")
				if err != nil || string(data) != "retain stale sidecar" {
					t.Fatal("backup changed an existing sidecar")
				}
			}
			if mode == "missing-source" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("backup initialized a missing source")
				}
			}
			if mode != "same-source" && mode != "existing" && mode != "symlink-destination" {
				if _, err := os.Lstat(destination); !os.IsNotExist(err) {
					t.Fatal("failed backup published a partial snapshot")
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".scratchpad-backup-") {
					t.Fatal("failed backup left staging artifacts")
				}
			}
		})
	}
}

func TestPostgresBackupDelegatesWithoutConnectingOrPrintingConfiguration(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	source, destination := filepath.Join(dir, "missing.sqlite"), filepath.Join(dir, "snapshot.sqlite")
	out, err := backupCommand(source, "postgres://synthetic-user:synthetic-credential@127.0.0.1:1/private", destination).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "BACKUP_EXTERNAL") || strings.Contains(string(out), "synthetic-credential") {
		t.Fatal("PostgreSQL backup did not delegate safely")
	}
	for _, path := range []string{source, destination} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("PostgreSQL backup touched SQLite storage")
		}
	}
}

func TestSQLiteConcurrentBackupsPublishOneSnapshotWithoutOverwrite(t *testing.T) {
	path := legacyFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	destination := filepath.Join(dir, "snapshot.sqlite")
	commands := []*exec.Cmd{backupCommand(path, "", destination), backupCommand(path, "", destination)}
	outputs := []bytes.Buffer{{}, {}}
	for index, cmd := range commands {
		cmd.Stdout, cmd.Stderr = &outputs[index], &outputs[index]
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				cmd.Process.Kill()
				cmd.Wait()
			}
		})
	}
	successes := 0
	for index, cmd := range commands {
		if err := cmd.Wait(); err == nil {
			successes++
			var report map[string]any
			if json.Unmarshal(outputs[index].Bytes(), &report) != nil || report["schemaVersion"] != float64(1) {
				t.Fatal("concurrent backup reported invalid snapshot")
			}
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent backups overwrote their destination: %d successes", successes)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("concurrent backups changed source schema")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "snapshot.sqlite" {
		t.Fatal("concurrent backups left staging artifacts")
	}
	if out, err := backupCommand(destination, "", filepath.Join(dir, "verified-copy.sqlite")).CombinedOutput(); err != nil {
		t.Fatalf("published snapshot is not independently readable: %v %s", err, out)
	}
}

func TestSQLiteBackupCancellationCleansStagingAndRetainsSource(t *testing.T) {
	path := legacyFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("ROLLBACK")
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	destination := filepath.Join(dir, "snapshot.sqlite")
	cmd := backupCommand(path, "", destination)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	exited := false
	t.Cleanup(func() {
		if !exited {
			cmd.Process.Kill()
			<-done
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		entries, _ := os.ReadDir(dir)
		started := false
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".scratchpad-backup-") {
				started = true
			}
		}
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backup did not enter its owned staging operation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		exited = true
		if err == nil {
			t.Fatal("cancelled backup succeeded")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("backup did not stop after cancellation")
	}
	if _, err = os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("cancelled backup published a snapshot")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("cancelled backup left a partial staging file")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("cancelled backup changed its source")
	}
}

func restoreSnapshotFixture(t *testing.T, snapshot string) string {
	t.Helper()
	data, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	path := filepath.Join(dir, "restored.sqlite")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSQLiteBackupPreservesPreUpgradeSchemaAndAuthenticatedHistory(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	seedRestoredHistory(t, db)
	db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	destination := filepath.Join(dir, "snapshot.sqlite")
	out, err := backupCommand(path, "", destination).CombinedOutput()
	if err != nil {
		t.Fatalf("pre-upgrade backup failed: %v %s", err, out)
	}
	var report map[string]any
	if json.Unmarshal(out, &report) != nil || report["database"] != "sqlite" || report["schemaVersion"] != float64(1) {
		t.Fatal("backup did not retain the source schema version")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("backup migrated or changed its source")
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("backup permissions are not private")
	}
	snapshot, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	origin := startServer(t, restoreSnapshotFixture(t, destination), "")
	status, detail := fetchRecord(t, origin, restoredToken)
	if status != 200 {
		t.Fatal("backup lost authenticated history")
	}
	var record map[string]any
	json.Unmarshal([]byte(restoredRecord), &record)
	if !reflect.DeepEqual(detail["record"], record) || len(detail["revisions"].([]any)) != 1 || len(detail["evidence"].([]any)) != 1 || len(detail["audit"].([]any)) != 1 {
		t.Fatal("backup rewrote or lost immutable history")
	}
	retained, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(snapshot, retained) {
		t.Fatal("restore changed the retained rollback snapshot")
	}
}

func TestSQLiteOnlineBackupRestoresWALRetryAndCurationState(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	source := startServer(t, path, databaseURL)
	status, project := projectRequest(t, source, "POST", "/resolve-explicit", map[string]any{"name": "Live snapshot"}, restoredToken)
	if status != 200 {
		t.Fatal("project fixture failed")
	}
	projectID := project["project"].(map[string]any)["id"].(string)
	input := map[string]any{"type": "finding", "title": "Durable live snapshot", "authority": "observed", "payload": map[string]any{"finding": "Include committed WAL history."}}
	status, capture := captureRequest(t, source, projectID, "snapshot-retry", input)
	if status != 201 {
		t.Fatal("capture fixture failed")
	}
	recordID := capture["record"].(map[string]any)["id"].(string)
	status, _ = curationRequest(t, source, "PATCH", "/records/"+recordID+"/metadata", map[string]any{"tags": []any{"snapshot"}, "expectedVersion": 1})
	if status != 200 {
		t.Fatal("curation fixture failed")
	}
	status, before := curationRequest(t, source, "GET", "/records/"+recordID, nil)
	if status != 200 {
		t.Fatal("record fixture failed")
	}
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatal("fixture does not contain a live WAL")
	}
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	destination := filepath.Join(dir, "snapshot.sqlite")
	if out, err := backupCommand(path, "", destination).CombinedOutput(); err != nil {
		t.Fatalf("online backup failed: %v %s", err, out)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(destination + suffix); !os.IsNotExist(err) {
			t.Fatal("backup depends on a sidecar")
		}
	}
	curationCapture(t, source, projectID, "Created after snapshot")
	restored := startServer(t, restoreSnapshotFixture(t, destination), "")
	status, detail := curationRequest(t, restored, "GET", "/records/"+recordID, nil)
	if status != 200 || !reflect.DeepEqual(before, detail) {
		t.Fatal("restored snapshot lost auth, raw capture, curation or audits")
	}
	status, replay := captureRequest(t, restored, projectID, "snapshot-retry", input)
	if status != 201 || !reflect.DeepEqual(capture, replay) {
		t.Fatal("operational backup lost capture retry identity")
	}
	status, listing := searchRecords(t, restored, "q=committed&projectId="+projectID)
	if status != 200 || len(listing["records"].([]any)) != 1 {
		t.Fatal("snapshot lost its lexical index")
	}
	status, listing = searchRecords(t, restored, "q=Created%20after%20snapshot&projectId="+projectID)
	if status != 200 || len(listing["records"].([]any)) != 0 {
		t.Fatal("snapshot included later writes")
	}
}
