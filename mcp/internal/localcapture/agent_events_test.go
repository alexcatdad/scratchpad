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

type agentEventTestAdapter struct {
	*testAdapter
	deliver func(context.Context, Binding, []json.RawMessage) error
}

func (a *agentEventTestAdapter) DeliverAgentEvents(ctx context.Context, binding Binding, events []json.RawMessage) error {
	return a.deliver(ctx, binding, events)
}

func agentEventRuntime(t *testing.T, deliver func(context.Context, Binding, []json.RawMessage) error) (*Runtime, *Store) {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "private", "captures.sqlite"), &testKeys{})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &agentEventTestAdapter{testAdapter: &testAdapter{identity: testIdentity()}, deliver: deliver}
	r, err := NewRuntime(context.Background(), ServiceOptions{Store: store, Adapter: adapter, RetryMin: 10 * time.Millisecond, RetryMax: 20 * time.Millisecond})
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	if err = r.SetConfiguration(context.Background(), ServiceConfiguration{ServerURL: "https://scratchpad.invalid", CredentialFingerprint: "SHA256:synthetic", PublicKeyPath: "/tmp/synthetic.pub", SigningKeyPath: "/tmp/synthetic.key"}); err != nil {
		t.Fatal(err)
	}
	return r, store
}

func waitForAgentEventCount(t *testing.T, store *Store, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		count, err := store.agentEventCount()
		if err != nil {
			t.Fatal(err)
		}
		if count == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue holds %d agent events, want %d", count, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAgentEventsAreDeliveredInBatchesAndThenDropped(t *testing.T) {
	type call struct {
		binding Binding
		ids     []string
	}
	calls := make(chan call, 16)
	r, store := agentEventRuntime(t, func(_ context.Context, binding Binding, events []json.RawMessage) error {
		var ids []string
		for _, event := range events {
			var decoded struct {
				EventID string `json:"eventId"`
			}
			if err := json.Unmarshal(event, &decoded); err != nil {
				return err
			}
			ids = append(ids, decoded.EventID)
		}
		calls <- call{binding, ids}
		return nil
	})
	if err := r.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	const total = agentEventBatchSize + 5
	for i := range total {
		if _, err := r.AgentEvent(context.Background(), agentEvent(fmt.Sprintf("e%03d", i), "queued")); err != nil {
			t.Fatal(err)
		}
	}
	// Paused delivery holds agent events as it holds captures.
	time.Sleep(60 * time.Millisecond)
	if count, _ := store.agentEventCount(); count != total || len(calls) != 0 {
		t.Fatalf("paused service delivered events: count=%d calls=%d", count, len(calls))
	}
	if err := r.ResumeDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForAgentEventCount(t, store, 0)

	first, second := <-calls, <-calls
	if len(first.ids) != agentEventBatchSize || first.ids[0] != "e000" || len(second.ids) != 5 || second.ids[4] != fmt.Sprintf("e%03d", total-1) {
		t.Fatalf("unexpected batches: %d starting %s, then %d", len(first.ids), first.ids[0], len(second.ids))
	}
	if first.binding.ServerURL != "https://scratchpad.invalid" || first.binding.CredentialFingerprint != "SHA256:synthetic" || first.binding.SigningKeyPath != "/tmp/synthetic.key" {
		t.Fatalf("unexpected binding: %#v", first.binding)
	}
	status, err := r.Status(context.Background())
	if err != nil || status.Pending != 0 || status.LastDelivery != nil {
		t.Fatalf("agent event delivery changed capture status: %#v err=%v", status, err)
	}
}

func TestAgentEventsSurviveFailedDeliveryAndAreRetried(t *testing.T) {
	attempts := make(chan int, 16)
	failures := 2
	r, store := agentEventRuntime(t, func(_ context.Context, _ Binding, events []json.RawMessage) error {
		attempts <- len(events)
		if failures > 0 {
			failures--
			return errors.New("server unreachable")
		}
		return nil
	})
	if err := r.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if _, err := r.AgentEvent(context.Background(), agentEvent(fmt.Sprintf("e%d", i), "retry")); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.ResumeDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForAgentEventCount(t, store, 0)
	if len(attempts) != 3 {
		t.Fatalf("delivery attempts = %d, want 3", len(attempts))
	}
	for range 3 {
		if size := <-attempts; size != 3 {
			t.Fatalf("a retry sent %d events, want the same 3", size)
		}
	}
}

func TestAgentEventBatchRespectsTheByteBudget(t *testing.T) {
	sizes := make(chan int, 8)
	r, store := agentEventRuntime(t, func(_ context.Context, _ Binding, events []json.RawMessage) error {
		total := 0
		for _, event := range events {
			total += len(event)
		}
		sizes <- total
		return nil
	})
	if err := r.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("x", AgentEventMaxBytes-200)
	for i := range 10 {
		if _, err := r.AgentEvent(context.Background(), agentEvent(fmt.Sprintf("big%d", i), large)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.ResumeDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForAgentEventCount(t, store, 0)
	close(sizes)
	batches := 0
	for size := range sizes {
		batches++
		if size > agentEventBatchBytes {
			t.Fatalf("a batch carried %d bytes, over the %d budget", size, agentEventBatchBytes)
		}
	}
	if batches < 2 {
		t.Fatalf("ten large events went out in %d batch", batches)
	}
}
