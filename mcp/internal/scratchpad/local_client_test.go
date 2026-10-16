package scratchpad

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestLocalClientNeverFallsBackToHTTP(t *testing.T) {
	var hits atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(500) }))
	defer trap.Close()
	t.Setenv("SCRATCHPAD_URL", trap.URL)
	t.Setenv("SCRATCHPAD_PUBLIC_KEY", "/does-not-exist")
	t.Setenv("SCRATCHPAD_SIGNING_KEY", "/does-not-exist")
	c, err := NewLocalClient(filepath.Join(t.TempDir(), "absent.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Config.URL != "" || c.Config.PublicKeyPath != "" || c.Config.SigningKeyPath != "" {
		t.Fatal("local client copied server configuration")
	}
	var out any
	if err = c.request(context.Background(), "GET", "/records/r", nil, "", &out); err == nil {
		t.Fatal("read succeeded without collector")
	}
	if err = c.requestEvidence(context.Background(), map[string]any{"query": "synthetic"}, &out); err == nil {
		t.Fatal("evidence succeeded without collector")
	}
	_, _, err = c.capture(context.Background(), "finding", Common{Scope: Scope{ProjectID: "p", WorkingDirectory: t.TempDir()}, RequestID: "synthetic", Title: "Synthetic", Authority: "observed", Confidence: "high"}, FindingPayload{Finding: "Synthetic"})
	if err == nil {
		t.Fatal("capture succeeded without collector")
	}
	if hits.Load() != 0 {
		t.Fatal("MCP contacted server directly")
	}
}
