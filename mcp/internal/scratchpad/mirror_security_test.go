package scratchpad

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMirrorSpecialFiles(t *testing.T) {
	if root := os.Getenv("SCRATCHPAD_MIRROR_SPECIAL_ROOT"); root != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if os.Getenv("SCRATCHPAD_MIRROR_RACE") == "1" {
			go func() {
				time.Sleep(50 * time.Millisecond)
				_ = os.Remove(filepath.Join(root, "mirror.jsonl"))
				_ = exec.Command("mkfifo", filepath.Join(root, "mirror.jsonl")).Run()
				_ = os.Remove(filepath.Join(root, "mirror.jsonl.lock"))
			}()
		}
		if err := Mirror(ctx, root, "mirror.jsonl", map[string]any{"id": "synthetic"}); err == nil {
			t.Fatal("accepted special target")
		}
		if _, err := os.Stat(filepath.Join(root, "mirror.jsonl.lock")); !os.IsNotExist(err) {
			t.Fatal("lock not released")
		}
		return
	}
	for _, kind := range []string{"fifo", "directory", "race-fifo"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "mirror.jsonl")
			if kind == "fifo" {
				if err := exec.Command("mkfifo", path).Run(); err != nil {
					t.Fatal(err)
				}
			} else if kind == "race-fifo" {
				if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path+".lock", 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMirrorSpecialFiles$")
			command.Env = append(os.Environ(), "SCRATCHPAD_MIRROR_SPECIAL_ROOT="+root)
			if kind == "race-fifo" {
				command.Env = append(command.Env, "SCRATCHPAD_MIRROR_RACE=1")
			}
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("special-file mirror failed or blocked: %s %v", output, err)
			}
		})
	}
}

func TestMirrorCancelledScanReleasesLock(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mirror.jsonl")
	// One large valid line forces multiple scanner reads before decoding.
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("{\"id\":\"existing\",\"synthetic\":\"")
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for i := range chunk {
		chunk[i] = 'x'
	}
	for range 12 {
		if _, err = f.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.WriteString("\"}\n"); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	err = Mirror(ctx, root, "mirror.jsonl", map[string]any{"id": "new"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation ignored")
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatal("lock not released")
	}
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer deadlineCancel()
	start = time.Now()
	if err = Mirror(deadlineCtx, root, "mirror.jsonl", map[string]any{"id": "new"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("large scan ignored deadline: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("scan exceeded deadline tolerance")
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatal("deadline left a lock")
	}
	after, _ := os.Stat(path)
	if after.Size() != before.Size() {
		t.Fatal("cancelled scan changed mirror")
	}
	if err = os.WriteFile(path, []byte("{\"id\":\"existing\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = Mirror(context.Background(), root, "mirror.jsonl", map[string]any{"id": "new"}); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
}

func TestMirrorRejectsDevice(t *testing.T) {
	if err := Mirror(context.Background(), "/dev", "null", map[string]any{"id": "synthetic"}); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("device target: %v", err)
	}
}

func TestAcceptanceSpecialMirrorPreservesCentralCaptureAndRetry(t *testing.T) {
	dir := initializedRepository(t)
	target := filepath.Join(dir, "scratchpad/decisions.jsonl")
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("mkfifo", target).Run(); err != nil {
		t.Fatal(err)
	}
	session, fixture := acceptanceSession(t, dir, true)
	args := decisionArgs("special-file-stable")
	start := time.Now()
	first := acceptanceCall(t, session, "record_decision", args)
	if time.Since(start) > time.Second {
		t.Fatal("special-file capture did not return promptly")
	}
	record := first["record"].(map[string]any)
	mirror := first["mirror"].(map[string]any)
	if record["id"] == "" || mirror["succeeded"] != false || !strings.Contains(mirror["error"].(string), "saved centrally") {
		t.Fatalf("central capture missing or mirror failure hidden: %+v", first)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	second := acceptanceCall(t, session, "record_decision", args)
	if second["record"].(map[string]any)["id"] != record["id"] || second["mirror"].(map[string]any)["succeeded"] != true {
		t.Fatalf("retry changed central identity or failed mirror: %+v", second)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.records) != 1 || fixture.reports != 2 {
		t.Fatal("capture retry duplicated central identity or omitted partial outcome")
	}
	data, err := os.ReadFile(target)
	if err != nil || strings.Count(string(data), "\n") != 1 {
		t.Fatalf("ordinary retry mirror: %v", err)
	}
}
