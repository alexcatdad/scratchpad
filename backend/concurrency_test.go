package backend_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSlowContextReaderDoesNotBlockIndependentCapture(t *testing.T) {
	path, _, _ := githubHistoricalBrowser(t, "sqlite")
	origin := startServer(t, path, "")
	status, project := projectRequest(t, origin, "POST", "/resolve-explicit", map[string]any{"name": "Concurrent context"}, restoredToken)
	if status != 200 && status != 201 {
		t.Fatalf("project resolution failed: %d", status)
	}
	id := project["project"].(map[string]any)["id"].(string)
	for n := range 30 {
		status, _ := captureRequest(t, origin, id, fmt.Sprintf("large-context-%d", n), map[string]any{"type": "finding", "title": fmt.Sprintf("Large finding %d", n), "authority": "observed", "payload": map[string]any{"finding": strings.Repeat("Synthetic evidence. ", 4500)}})
		if status != 201 {
			t.Fatalf("large capture failed: %d", status)
		}
	}
	// Bound the socket's receive window so the real context response remains in
	// flight while a different authenticated client submits its capture.
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err == nil {
			err = conn.(*net.TCPConn).SetReadBuffer(1024)
			if err != nil {
				conn.Close()
			}
		}
		return conn, err
	}}
	defer transport.CloseIdleConnections()
	reader := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", origin+"/api/v1/projects/"+id+"/context", nil)
	req.Header.Set("Authorization", "Bearer "+restoredToken)
	res, err := reader.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("context failed: %d", res.StatusCode)
	}
	if _, err = io.ReadFull(res.Body, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"projectId": id, "record": map[string]any{"type": "finding", "title": "Independent capture", "authority": "observed", "payload": map[string]any{"finding": "Capture while another client reads context."}}})
	req, _ = http.NewRequest("POST", origin+"/api/v1/records", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+restoredToken)
	writer := &http.Client{Timeout: 3 * time.Second}
	written, err := writer.Do(req)
	if err != nil {
		t.Fatalf("independent capture blocked behind context reader: %v", err)
	}
	defer written.Body.Close()
	var body map[string]any
	if written.StatusCode != 201 || json.NewDecoder(written.Body).Decode(&body) != nil {
		t.Fatalf("independent capture failed: %d", written.StatusCode)
	}
	if body["record"].(map[string]any)["title"] != "Independent capture" {
		t.Fatal("concurrent capture did not preserve its content")
	}
}
