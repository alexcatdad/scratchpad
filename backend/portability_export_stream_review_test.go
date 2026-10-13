package backend_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func measuredExportServer(t *testing.T, path, databaseURL string) (string, int) {
	t.Helper()
	cmd := exec.Command(binary, "serve")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_PATH="+path, "SCRATCHPAD_DATABASE_URL="+databaseURL, "SCRATCHPAD_LISTEN_ADDR=127.0.0.1:0", "SCRATCHPAD_PUBLIC_URL=http://localhost:3000", "GOMEMLIMIT=96MiB")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("measured server exit: %v", err)
			}
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
			<-done
			t.Error("measured server did not stop")
		}
	})
	startup := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			var report struct{ Address string }
			json.Unmarshal(scanner.Bytes(), &report)
			startup <- report.Address
		} else {
			startup <- ""
		}
	}()
	select {
	case address := <-startup:
		if address == "" {
			t.Fatal("measured server startup failed")
		}
		return "http://" + address, cmd.Process.Pid
	case <-time.After(10 * time.Second):
		t.Fatal("measured server startup timed out")
	}
	return "", 0
}

func waitExportTempDirectoryEmpty(t *testing.T, directory string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("native export left temporary knowledge files")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPostgresNativeExportKeepsSnapshotPastDefaultWriteDeadlineAndCleansCancellation(t *testing.T) {
	path, databaseURL := portableInstance(t, "postgres")
	spoolDirectory := t.TempDir()
	t.Setenv("TMPDIR", spoolDirectory)
	origin := startServer(t, path, databaseURL)
	project := jobProject(t, origin, "Slow snapshot export", false, false)
	record := curationCapture(t, origin, project, "Immutable export source")
	status, before := curationRequest(t, origin, "POST", "/export", map[string]any{})
	if status != 200 {
		t.Fatal("initial export failed")
	}
	waitExportTempDirectoryEmpty(t, spoolDirectory)
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`ALTER TABLE entities RENAME TO export_snapshot_entities;
CREATE FUNCTION export_read_gate(kind TEXT,value JSONB) RETURNS JSONB LANGUAGE plpgsql VOLATILE AS $$
BEGIN IF kind='record' AND current_setting('transaction_read_only')='on' THEN PERFORM pg_advisory_xact_lock(77823); END IF; RETURN value; END $$;
CREATE VIEW entities AS SELECT kind,id,export_read_gate(kind,data) AS data,version,created_at FROM export_snapshot_entities;
CREATE FUNCTION export_forward_write() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN INSERT INTO export_snapshot_entities(kind,id,data,version,created_at) VALUES(NEW.kind,NEW.id,NEW.data,NEW.version,NEW.created_at);
 ELSE UPDATE export_snapshot_entities SET data=NEW.data,version=NEW.version,created_at=NEW.created_at WHERE kind=OLD.kind AND id=OLD.id;
 END IF; RETURN NEW;
END $$;
CREATE TRIGGER export_write INSTEAD OF INSERT OR UPDATE ON entities FOR EACH ROW EXECUTE FUNCTION export_forward_write()`)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	acquire := func() {
		if _, err := lock.ExecContext(context.Background(), `SELECT pg_advisory_lock(77823)`); err != nil {
			t.Fatal(err)
		}
	}
	release := func() {
		if _, err := lock.ExecContext(context.Background(), `SELECT pg_advisory_unlock(77823)`); err != nil {
			t.Fatal(err)
		}
	}
	defer release()
	barrier := func() {
		deadline := time.Now().Add(5 * time.Second)
		for {
			var waiting bool
			if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=77823 AND NOT granted)`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("export did not reach snapshot barrier")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	type result struct {
		status int
		body   map[string]any
	}
	done := make(chan result, 1)
	acquire()
	go func() {
		status, body := curationRequest(t, origin, "POST", "/export", map[string]any{})
		done <- result{status, body}
	}()
	barrier()
	entries, err := os.ReadDir(spoolDirectory)
	if err != nil || len(entries) == 0 {
		t.Fatal("export did not create its private operational spool")
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatal("temporary knowledge spool was not owner-only")
		}
	}
	status, _ = curationRequest(t, origin, "PATCH", "/records/"+record["id"].(string)+"/metadata", map[string]any{"expectedVersion": 1, "tags": []any{"edited-during-export"}})
	if status != 200 {
		t.Fatal("snapshot export blocked independent public curation")
	}
	// Deliberately hold the SQL fixture beyond the server's ordinary 30-second
	// write timeout. This tests a slow valid export, not a polling sleep.
	timer := time.NewTimer(31 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		t.Fatal("valid slow export ended before its fixture released")
	case <-timer.C:
	}
	release()
	exported := <-done
	if exported.status != 200 || !reflect.DeepEqual(exported.body["data"], before["data"]) {
		t.Fatal("slow export lost its complete immutable read snapshot")
	}
	waitExportTempDirectoryEmpty(t, spoolDirectory)
	status, fresh := curationRequest(t, origin, "POST", "/export", map[string]any{})
	if status != 200 || len(fresh["data"].(map[string]any)["revision"].([]any)) != 1 {
		t.Fatal("later export did not observe committed curation")
	}
	waitExportTempDirectoryEmpty(t, spoolDirectory)
	acquire()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", origin+"/api/v1/export", nil)
	request.Header.Set("Authorization", "Bearer "+restoredToken)
	cancelled := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(request)
		if response != nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
		cancelled <- err
	}()
	barrier()
	cancel()
	select {
	case err := <-cancelled:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("export ignored client cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled export did not finish")
	}
	// The fixture is still locked: cleanup must follow cancellation itself.
	waitExportTempDirectoryEmpty(t, spoolDirectory)
}

func TestNativeExportLateFailureRemainsAtomicAndLeavesNoPrivateFiles(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			spoolDirectory := t.TempDir()
			t.Setenv("TMPDIR", spoolDirectory)
			origin := startServer(t, path, databaseURL)
			project := jobProject(t, origin, "Late export failure", false, false)
			curationCapture(t, origin, project, "Valid earlier export evidence")
			driver, location := "sqlite", path
			if engine == "postgres" {
				driver, location = "pgx", databaseURL
			}
			db, err := sql.Open(driver, location)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('audit','audit_invalid_export','[]',1,'2026-10-01T00:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			status, body := curationRequest(t, origin, "POST", "/export", map[string]any{})
			if status != 503 || body["error"] == nil || body["data"] != nil {
				t.Fatal("late export validation exposed a partial knowledge archive")
			}
			waitExportTempDirectoryEmpty(t, spoolDirectory)
			request, _ := http.NewRequest("POST", origin+"/api/v1/export", nil)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 401 {
				t.Fatal("native export bypassed authentication")
			}
			waitExportTempDirectoryEmpty(t, spoolDirectory)
		})
	}
}

func TestNativeLargeExportUsesBoundedServerMemoryAndRemainsReplayable(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("public process RSS measurement requires Linux or macOS ps")
	}
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin, pid := measuredExportServer(t, path, databaseURL)
			project := jobProject(t, origin, "Large portable history", false, false)
			driver, location := "sqlite", path
			if engine == "postgres" {
				driver, location = "pgx", databaseURL
			}
			db, err := sql.Open(driver, location)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			text := strings.Repeat("Synthetic immutable export evidence. ", 14000)
			for i := 0; i < 48; i++ {
				id := fmt.Sprintf("rec_large_export_%03d", i)
				for kind, value := range map[string]map[string]any{
					"record":   {"id": id, "version": 1, "createdAt": "2026-10-01T00:00:00Z", "recordedAt": "2026-10-01T00:00:00Z", "projectId": project, "type": "finding", "authority": "observed", "title": "Large immutable source", "content": text, "payload": map[string]any{"finding": text, "counter": json.Number("9007199254740993")}},
					"metadata": {"id": id, "recordId": id, "version": 1, "createdAt": "2026-10-01T00:00:00Z", "tags": []any{}, "archived": false},
				} {
					raw, _ := json.Marshal(value)
					if _, err = tx.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES($1,$2,$3,1,'2026-10-01T00:00:00Z')`, kind, id, string(raw)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			peak := int64(0)
			stop, sampled := make(chan struct{}), make(chan error, 1)
			go func() {
				for {
					raw, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
					if err != nil {
						sampled <- err
						return
					}
					kb, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
					if err != nil {
						sampled <- err
						return
					}
					peak = max(peak, kb*1024)
					select {
					case <-stop:
						sampled <- nil
						return
					case <-time.After(10 * time.Millisecond):
					}
				}
			}()
			request, _ := http.NewRequest("POST", origin+"/api/v1/export", nil)
			request.Header.Set("Authorization", "Bearer "+restoredToken)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				close(stop)
				<-sampled
				t.Fatal(err)
			}
			file, err := os.CreateTemp(t.TempDir(), "archive-*.json")
			if err != nil {
				response.Body.Close()
				close(stop)
				<-sampled
				t.Fatal(err)
			}
			defer file.Close()
			written, copyErr := io.Copy(file, response.Body)
			response.Body.Close()
			close(stop)
			if err = <-sampled; err != nil {
				t.Fatal(err)
			}
			t.Logf("%s export: %d bytes; server peak RSS %d MiB", engine, written, peak/(1024*1024))
			if response.StatusCode != 200 || copyErr != nil || written < 40*1024*1024 {
				t.Fatal("large export did not complete")
			}
			if peak > 256*1024*1024 {
				t.Fatalf("large export exceeds broad server memory bound: %d MiB", peak/(1024*1024))
			}
			// Parse one entity at a time after child RSS sampling has stopped.
			file.Seek(0, io.SeekStart)
			decoder := json.NewDecoder(file)
			decoder.Token()
			count := 0
			for decoder.More() {
				key, _ := decoder.Token()
				if key != "data" {
					var value json.RawMessage
					if err = decoder.Decode(&value); err != nil {
						t.Fatal(err)
					}
					continue
				}
				decoder.Token()
				for decoder.More() {
					kind, _ := decoder.Token()
					decoder.Token()
					for decoder.More() {
						var value json.RawMessage
						if err = decoder.Decode(&value); err != nil {
							t.Fatal(err)
						}
						if kind == "record" {
							count++
							if !bytes.Contains(value, []byte(`"counter":9007199254740993`)) {
								t.Fatal("export rounded immutable number")
							}
						}
					}
					decoder.Token()
				}
				decoder.Token()
			}
			if _, err = decoder.Token(); err != nil || count != 48 {
				t.Fatal("large archive lost records or malformed its envelope")
			}
			file.Seek(0, io.SeekStart)
			request, _ = http.NewRequestWithContext(context.Background(), "POST", origin+"/api/v1/import", file)
			request.Header.Set("Authorization", "Bearer "+restoredToken)
			response, err = http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var replay map[string]any
			if json.NewDecoder(response.Body).Decode(&replay) != nil || response.StatusCode != 200 || replay["imported"] != float64(0) || replay["skipped"].(float64) < 96 {
				t.Fatal("large portable archive cannot replay")
			}
		})
	}
}
