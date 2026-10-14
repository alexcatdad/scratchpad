package backend_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProductionRequestLogsContainOnlySafeMetadata(t *testing.T) {
	path, databaseURL := portableInstance(t, "sqlite")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "serve")
	cmd.Env = append(os.Environ(), "NODE_ENV=production", "SCRATCHPAD_DATABASE_PATH="+path, "SCRATCHPAD_DATABASE_URL="+databaseURL, "SCRATCHPAD_LISTEN_ADDR=127.0.0.1:0", "SCRATCHPAD_PUBLIC_URL=http://localhost:3000")
	var logs bytes.Buffer
	cmd.Stderr = &logs
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() error {
		if stopped {
			return nil
		}
		stopped = true
		_ = cmd.Process.Signal(syscall.SIGTERM)
		return cmd.Wait()
	}
	t.Cleanup(func() { _ = stop() })
	var report map[string]any
	if json.NewDecoder(stdout).Decode(&report) != nil {
		t.Fatal("server did not report startup")
	}
	request, _ := http.NewRequest("GET", "http://"+report["address"].(string)+"/api/v1/records/missing?token=QUERY_SECRET_WITNESS", strings.NewReader("BODY_SECRET_WITNESS"))
	request.Header.Set("Authorization", "Bearer "+restoredToken)
	request.Header.Set("X-Private-Input", "HEADER_SECRET_WITNESS")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("request fixture did not return its missing-record status")
	}
	if err = stop(); err != nil {
		t.Fatal("server did not stop cleanly")
	}
	for _, secret := range []string{restoredToken, "QUERY_SECRET_WITNESS", "BODY_SECRET_WITNESS", "HEADER_SECRET_WITNESS"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("request log exposed private input")
		}
	}
	found := false
	for _, line := range strings.Split(logs.String(), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil {
			continue
		}
		if record["method"] != "GET" || record["path"] != "/api/v1/records/missing" {
			continue
		}
		duration, ok := record["durationMs"].(float64)
		if len(record) != 4 || record["status"] != float64(404) || !ok || duration < 0 {
			t.Fatal("request log omitted or misreported response metadata")
		}
		found = true
	}
	if !found {
		t.Fatal("production server did not emit request metadata")
	}
}
