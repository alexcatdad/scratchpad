package localcapture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func agentEvent(id, text string) AgentEventRequest {
	return AgentEventRequest{Event: json.RawMessage(fmt.Sprintf(`{"schemaVersion":1,"eventId":%q,"agent":"codex","kind":"prompt","sessionId":"s1","text":%q}`, id, text))}
}

func TestAgentEventIsRedactedEncryptedAndQueuedInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "captures.sqlite")
	r, store := testRuntime(t, path, &testKeys{}, &testAdapter{identity: testIdentity()})
	defer r.Close()
	ctx := context.Background()

	out, err := r.AgentEvent(ctx, agentEvent("e1", "use "+syntheticToken+" to push"))
	if err != nil || !out.Accepted {
		t.Fatalf("accepted=%v err=%v", out.Accepted, err)
	}
	if _, err = r.AgentEvent(ctx, agentEvent("e2", "second")); err != nil {
		t.Fatal(err)
	}

	batch, err := store.agentEventBatch(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 || !strings.Contains(string(batch[0].event), `"eventId":"e1"`) || !strings.Contains(string(batch[1].event), `"eventId":"e2"`) {
		t.Fatalf("unexpected batch: %v", batch)
	}
	if strings.Contains(string(batch[0].event), syntheticToken) || !strings.Contains(string(batch[0].event), "secret://redacted/github-token") {
		t.Fatalf("event was not redacted: %s", batch[0].event)
	}

	// Nothing readable reaches the database file, and agent events never show
	// up as capture receipts or in the status counts.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{syntheticToken, "second", "eventId", "codex"} {
		if strings.Contains(string(raw), plain) {
			t.Fatalf("database file contains %q in the clear", plain)
		}
	}
	receipts, err := r.ListReceipts(ctx, ListReceiptsRequest{})
	if err != nil || len(receipts.Receipts) != 0 {
		t.Fatalf("receipts=%v err=%v", receipts.Receipts, err)
	}
	status, err := r.Status(ctx)
	if err != nil || status.Pending != 0 {
		t.Fatalf("status=%#v err=%v", status, err)
	}

	if err = store.deleteAgentEventsThrough(batch[0].id); err != nil {
		t.Fatal(err)
	}
	if count, _ := store.agentEventCount(); count != 1 {
		t.Fatalf("count after delete = %d", count)
	}
}

func TestAgentEventRejectsMalformedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "captures.sqlite")
	r, store := testRuntime(t, path, &testKeys{}, &testAdapter{identity: testIdentity()})
	defer r.Close()
	oversized := `{"text":"` + strings.Repeat("x", AgentEventMaxBytes) + `"}`
	for _, event := range []string{``, `   `, `[]`, `"text"`, `{`, `{"a":1} trailing`, oversized} {
		if _, err := r.AgentEvent(context.Background(), AgentEventRequest{Event: json.RawMessage(event)}); !errors.Is(err, ErrConflictSentinel) {
			t.Errorf("event %.20q: err=%v", event, err)
		}
	}
	if count, _ := store.agentEventCount(); count != 0 {
		t.Fatalf("malformed events were stored: %d", count)
	}
}

func TestAgentEventBacklogIsBoundedByRowsAndAge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "captures.sqlite")
	r, store := testRuntime(t, path, &testKeys{}, &testAdapter{identity: testIdentity()})
	defer r.Close()
	now := time.Now()
	for i := range 8 {
		if err := store.acceptAgentEvent([]byte(fmt.Sprintf(`{"n":%d}`, i)), now, 5); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := store.agentEventBatch(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 5 || string(batch[0].event) != `{"n":3}` || string(batch[4].event) != `{"n":7}` {
		t.Fatalf("row cap kept %d events starting at %s", len(batch), batch[0].event)
	}

	if err = store.acceptAgentEvent([]byte(`{"old":true}`), now.Add(-AgentEventMaxAge-time.Minute), 100); err != nil {
		t.Fatal(err)
	}
	if err = store.pruneAgentEvents(now); err != nil {
		t.Fatal(err)
	}
	batch, err = store.agentEventBatch(100)
	if err != nil {
		t.Fatal(err)
	}
	for _, queued := range batch {
		if string(queued.event) == `{"old":true}` {
			t.Fatal("expired event survived pruning")
		}
	}
	if len(batch) != 5 {
		t.Fatalf("pruning removed fresh events: %d left", len(batch))
	}
}

func TestAgentEventRespectsStoppedService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "captures.sqlite")
	r, _ := testRuntime(t, path, &testKeys{}, &testAdapter{identity: testIdentity()})
	defer r.Close()
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AgentEvent(context.Background(), agentEvent("e1", "after stop")); !errors.Is(err, ErrServiceStoppedSentinel) {
		t.Fatalf("err=%v", err)
	}
}

// The agent_events table is additive. The queue schema version is unchanged,
// a store holding events reopens and verifies cleanly, and capture rows are
// untouched, so a binary that predates agent events can still open the file.
func TestAgentEventsLeaveTheQueueSchemaCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "captures.sqlite")
	keys := &testKeys{}
	r, _ := testRuntime(t, path, keys, &testAdapter{identity: testIdentity()})
	if err := r.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := r.Capture(ctx, testRequest("capture-1", "A capture beside agent events")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AgentEvent(context.Background(), agentEvent("e1", "kept across restart")); err != nil {
		t.Fatal(err)
	}
	r.Close()

	version, err := InspectSchemaVersion(path)
	if err != nil || version != QueueSchemaVersion || QueueSchemaVersion != 1 {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	reopened, err := OpenStore(path, keys)
	if err != nil {
		t.Fatalf("store with agent events did not reopen: %v", err)
	}
	defer reopened.Close()
	batch, err := reopened.agentEventBatch(10)
	if err != nil || len(batch) != 1 || !strings.Contains(string(batch[0].event), "kept across restart") {
		t.Fatalf("batch=%v err=%v", batch, err)
	}
	counts, err := reopened.counts()
	if err != nil || counts[ReceiptPending] != 1 {
		t.Fatalf("capture rows changed: %v err=%v", counts, err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var captureRows int
	if err = db.QueryRow("SELECT COUNT(*) FROM capture_items WHERE state NOT IN ('configuration','settings')").Scan(&captureRows); err != nil || captureRows != 1 {
		t.Fatalf("capture_items rows=%d err=%v", captureRows, err)
	}
}

func TestAgentEventOverPrivateIPC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	r, store := testRuntime(t, path, &testKeys{}, &testAdapter{identity: testIdentity()}, false, true)
	socketDir, err := os.MkdirTemp("/tmp", "sp-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "service.sock")
	server, err := ListenIPC(socket, r)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()

	client := NewClient(socket)
	client.Timeout = 300 * time.Millisecond
	started := time.Now()
	out, err := client.AgentEvent(context.Background(), agentEvent("e1", "over the socket"))
	if err != nil || !out.Accepted {
		t.Fatalf("accepted=%v err=%v", out.Accepted, err)
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("intake took %s; a hook cannot wait that long", elapsed)
	}
	if _, err = client.AgentEvent(context.Background(), AgentEventRequest{Event: json.RawMessage(`[]`)}); !errors.Is(err, ErrConflictSentinel) {
		t.Fatalf("malformed event over IPC: %v", err)
	}
	if count, _ := store.agentEventCount(); count != 1 {
		t.Fatalf("stored %d events", count)
	}
	cancel()
	<-done
}
