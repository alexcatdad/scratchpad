package backend_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLegacyBlankLinesDoNotAllocateAnEntireLineIndex(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("process RSS measurement requires Linux or macOS")
	}
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin, pid := measuredExportServer(t, path, databaseURL)
			project := jobProject(t, origin, "Blank-line import memory", false, false)
			const blanks = 8 * 1024 * 1024
			line := `{"finding":"Evidence after blank lines","counter":1.0}`
			body, _ := json.Marshal(map[string]any{"format": "jsonl", "projectId": project, "jsonl": strings.Repeat("\n", blanks) + line})
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
			request, _ := http.NewRequest("POST", origin+"/api/v1/import", bytes.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+restoredToken)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				close(stop)
				<-sampled
				t.Fatal(err)
			}
			encoded, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			close(stop)
			if err = <-sampled; err != nil {
				t.Fatal(err)
			}
			t.Logf("%s blank-line import: peak server RSS %d MiB", engine, peak/(1024*1024))
			if peak > 512*1024*1024 {
				t.Fatalf("blank lines exceed broad importer memory bound: %d MiB", peak/(1024*1024))
			}
			var result map[string]any
			if readErr != nil || json.Unmarshal(encoded, &result) != nil || response.StatusCode != 200 || result["imported"] != float64(1) {
				t.Fatal("bounded blank-line import failed")
			}
			entry := result["records"].([]any)[0].(map[string]any)
			if entry["line"] != float64(blanks+1) {
				t.Fatal("blank-line iteration changed source line numbering")
			}
			status, detail := curationRequest(t, origin, "GET", "/records/"+entry["recordId"].(string), nil)
			if status != 200 || detail["record"].(map[string]any)["provenance"].(map[string]any)["import"].(map[string]any)["rawLine"] != line {
				t.Fatal("blank-line iteration changed the immutable source line")
			}
		})
	}
}
