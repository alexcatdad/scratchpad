package localcapture

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testKeys struct {
	mu             sync.Mutex
	key            []byte
	creates, loads int
	loadErr        error
}

func (k *testKeys) Load(context.Context) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.loads++
	if k.loadErr != nil {
		return nil, k.loadErr
	}
	return append([]byte(nil), k.key...), nil
}
func (k *testKeys) Create(context.Context) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.creates++
	if k.key == nil {
		k.key = make([]byte, 32)
		_, _ = rand.Read(k.key)
	}
	return append([]byte(nil), k.key...), nil
}

type testAdapter struct {
	calls            atomic.Int32
	authCalls        atomic.Int32
	identity         AuthenticatedIdentity
	authenticate     func(context.Context, Binding) (AuthenticatedIdentity, error)
	verifyConnection func(context.Context, Binding) (AuthenticatedIdentity, error)
	resolveProject   func(context.Context, Binding, string, json.RawMessage) (string, error)
	deliver          func(context.Context, DeliveryItem) (DeliveryResult, error)
}

func (a *testAdapter) Authenticate(ctx context.Context, binding Binding) (AuthenticatedIdentity, error) {
	a.authCalls.Add(1)
	if a.authenticate != nil {
		return a.authenticate(ctx, binding)
	}
	return a.identity, nil
}
func (a *testAdapter) VerifyConnection(ctx context.Context, binding Binding) (AuthenticatedIdentity, error) {
	if a.verifyConnection != nil {
		return a.verifyConnection(ctx, binding)
	}
	return a.Authenticate(ctx, binding)
}
func (a *testAdapter) ResolveProject(ctx context.Context, binding Binding, credentialID string, gitContext json.RawMessage) (string, error) {
	if a.resolveProject != nil {
		return a.resolveProject(ctx, binding, credentialID, gitContext)
	}
	return "proj_synthetic", nil
}
func (a *testAdapter) ListProjects(context.Context, Binding) ([]Project, error) {
	return []Project{{ID: "proj_synthetic", Name: "Synthetic"}}, nil
}
func (a *testAdapter) Deliver(ctx context.Context, item DeliveryItem) (DeliveryResult, error) {
	a.calls.Add(1)
	if a.deliver != nil {
		return a.deliver(ctx, item)
	}
	return successResult(), nil
}

func successResult() DeliveryResult {
	return DeliveryResult{RecordID: "rec_synthetic", ProjectID: "proj_synthetic", ServerRecordedAt: time.Now().UTC().Format(time.RFC3339Nano), CentralRecord: json.RawMessage(`{"id":"rec_synthetic"}`), CentralProject: json.RawMessage(`{"id":"proj_synthetic"}`)}
}
func testRuntime(t *testing.T, path string, keys *testKeys, adapter *testAdapter, opts ...bool) (*Runtime, *Store) {
	t.Helper()
	store, err := OpenStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	explicit := len(opts) > 0 && opts[0]
	exitAfterQuiesce := len(opts) > 1 && opts[1]
	r, err := NewRuntime(context.Background(), ServiceOptions{Store: store, Adapter: adapter, ExplicitStart: explicit, ExitAfterQuiesce: exitAfterQuiesce, RetryMin: 10 * time.Millisecond, RetryMax: 20 * time.Millisecond})
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if !r.InitiallyStopped() {
		if err = r.SetConfiguration(context.Background(), ServiceConfiguration{ServerURL: "https://scratchpad.invalid", CredentialFingerprint: "SHA256:synthetic", PublicKeyPath: "/tmp/synthetic.pub", SigningKeyPath: "/tmp/synthetic.key"}); err != nil {
			r.Close()
			t.Fatal(err)
		}
	}
	return r, store
}

func testDeferredRuntime(t *testing.T, path string, keys *testKeys, adapter *testAdapter) (*Runtime, *Store) {
	t.Helper()
	store, err := OpenStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRuntime(context.Background(), ServiceOptions{Store: store, Adapter: adapter, DeferWorker: true, RetryMin: 10 * time.Millisecond, RetryMax: 20 * time.Millisecond})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err = r.SetConfiguration(context.Background(), ServiceConfiguration{ServerURL: "https://scratchpad.invalid", CredentialFingerprint: "SHA256:synthetic", PublicKeyPath: "/tmp/synthetic.pub", SigningKeyPath: "/tmp/synthetic.key"}); err != nil {
		r.Close()
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r, store
}
func testRequest(id, title string) CaptureRequest {
	return CaptureRequest{RequestID: id, Record: Record{Type: "finding", Title: title, Authority: "observed", Confidence: "high", Payload: json.RawMessage(`{"marker":"SYNTHETIC_CAPTURE_SECRET"}`)}, Scope: Scope{ProjectID: "proj_synthetic"}, ClientSavedAt: "2026-10-15T10:00:00Z", Binding: Binding{ServerURL: "https://scratchpad.invalid", CredentialFingerprint: "SHA256:synthetic"}}
}
func testIdentity() AuthenticatedIdentity {
	return AuthenticatedIdentity{ServerURL: "https://scratchpad.invalid", CredentialID: "credential_synthetic", CaptureCapabilities: []string{CapabilityClientSavedAtV1}}
}

func shortPrivateTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sps-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestCaptureCommitIsEncryptedDurableAndReplaySafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "captures.sqlite")
	keys := &testKeys{}
	adapter := &testAdapter{identity: testIdentity()}
	r, _ := testRuntime(t, path, keys, adapter)
	if err := r.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := testRequest("request-1", "SYNTHETIC_TITLE_SECRET")
	request.Mirror = MirrorConsent{Enabled: true, Path: "/tmp/SYNTHETIC_MIRROR_PATH_SECRET"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	accepted, err := r.Capture(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Receipt.ReceiptID == "" || accepted.Receipt.State != ReceiptPending {
		t.Fatalf("unexpected receipt: %#v", accepted.Receipt)
	}
	repeated, err := r.Capture(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Receipt.ReceiptID != accepted.Receipt.ReceiptID {
		t.Fatalf("retry changed receipt: %q != %q", repeated.Receipt.ReceiptID, accepted.Receipt.ReceiptID)
	}
	changed := request
	changed.Record.Title = "changed title"
	if _, err = r.Capture(ctx, changed); !errors.Is(err, ErrConflictSentinel) {
		t.Fatalf("changed request should conflict, got %v", err)
	}
	changedMirror := request
	changedMirror.Mirror.Path = "/tmp/DIFFERENT_SYNTHETIC_MIRROR_PATH"
	if _, err = r.Capture(ctx, changedMirror); !errors.Is(err, ErrConflictSentinel) {
		t.Fatalf("changed mirror consent should conflict, got %v", err)
	}
	other, err := r.Capture(ctx, testRequest("request-2", "Different capture"))
	if err != nil {
		t.Fatal(err)
	}
	if other.Receipt.ReceiptID == accepted.Receipt.ReceiptID {
		t.Fatal("distinct captures shared a receipt")
	}
	r.Close()
	db, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"SYNTHETIC_CAPTURE_SECRET", "SYNTHETIC_TITLE_SECRET", "https://scratchpad.invalid", "/tmp/synthetic.key", "SYNTHETIC_MIRROR_PATH_SECRET"} {
		if strings.Contains(string(db), marker) {
			t.Fatalf("database leaked plaintext marker %q", marker)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("database permissions = %o", info.Mode().Perm())
	}
	if keys.creates != 1 {
		t.Fatalf("key provider Create called %d times", keys.creates)
	}
	denied := &testKeys{key: append([]byte(nil), keys.key...), loadErr: errors.New("key unavailable")}
	if _, err = OpenStore(path, denied); !errors.Is(err, ErrKeyUnavailableSentinel) {
		t.Fatalf("existing ciphertext must fail closed: %v", err)
	}
	if denied.creates != 0 {
		t.Fatal("missing key caused replacement key creation")
	}
	// Reopen with the original key and inspect only the public receipt contract.
	r, _ = testRuntime(t, path, keys, adapter)
	defer r.Close()
	if err = r.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, savedEnvelope, err := r.store.loadReceipt(accepted.Receipt.ReceiptID)
	if err != nil {
		t.Fatal(err)
	}
	if savedEnvelope.Receipt.Content == nil || savedEnvelope.Receipt.Content.Record.Title != "SYNTHETIC_TITLE_SECRET" || savedEnvelope.Request == nil || savedEnvelope.Request.Mirror.Path != "/tmp/SYNTHETIC_MIRROR_PATH_SECRET" {
		t.Fatalf("capture or mirror consent did not survive restart: %#v", savedEnvelope)
	}
}

func TestConcurrentStableIdentityCreatesOneDurableReceipt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	keys := &testKeys{}
	adapter := &testAdapter{identity: testIdentity()}
	r, _ := testRuntime(t, path, keys, adapter)
	defer r.Close()
	if err := r.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	req := testRequest("same-request", "Concurrent synthetic capture")
	const n = 8
	var wg sync.WaitGroup
	ids := make(chan string, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
			defer cancel()
			out, err := r.Capture(ctx, req)
			ids <- out.Receipt.ReceiptID
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	first := ""
	for id := range ids {
		if id == "" {
			t.Fatal("capture returned no receipt")
		}
		if first == "" {
			first = id
		} else if first != id {
			t.Fatal("duplicate request created multiple receipts")
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	listed, err := r.ListReceipts(context.Background(), ListReceiptsRequest{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Receipts) != 1 {
		t.Fatalf("got %d receipts, want one", len(listed.Receipts))
	}
}

func TestReceiptPaginationAndThirtyDayRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	keys := &testKeys{}
	adapter := &testAdapter{identity: testIdentity()}
	r, _ := testRuntime(t, path, keys, adapter)
	defer r.Close()
	if err := r.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		_, err := r.Capture(ctx, testRequest(fmt.Sprintf("page-%d", i), fmt.Sprintf("Synthetic page %d", i)))
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	first, err := r.ListReceipts(context.Background(), ListReceiptsRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Receipts) != 2 || first.NextCursor == "" {
		t.Fatalf("first page did not return a cursor: %#v", first)
	}
	second, err := r.ListReceipts(context.Background(), ListReceiptsRequest{Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Receipts) != 1 || second.NextCursor != "" {
		t.Fatalf("unexpected second page: %#v", second)
	}
	oldest := second.Receipts[0]
	key, env, err := r.store.loadReceipt(oldest.ReceiptID)
	if err != nil {
		t.Fatal(err)
	}
	env.Receipt.State = ReceiptDelivered
	env.Request = nil
	env.Receipt.Content = nil
	env.TombstoneUntil = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	if err = r.store.updateEnvelope(key, env, time.Time{}, 1); err != nil {
		t.Fatal(err)
	}
	if err = r.store.prune(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = r.LookupReceipt(context.Background(), oldest.ReceiptID); !errors.Is(err, ErrReceiptNotFoundSentinel) {
		t.Fatalf("expired minimal receipt remained available: %v", err)
	}
}

func TestPeriodicPruningExpiresDeliveredReceiptsAndPreservesPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	keys := &testKeys{}
	store, err := OpenStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &testAdapter{identity: testIdentity()}
	r, err := NewRuntime(context.Background(), ServiceOptions{Store: store, Adapter: adapter, PruneInterval: 10 * time.Millisecond, HealthInterval: time.Hour, RetryMin: time.Hour})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	defer r.Close()
	if err = r.SetConfiguration(context.Background(), ServiceConfiguration{ServerURL: "https://scratchpad.invalid", CredentialFingerprint: "SHA256:synthetic", PublicKeyPath: "/tmp/synthetic.pub", SigningKeyPath: "/tmp/synthetic.key"}); err != nil {
		t.Fatal(err)
	}
	if err = r.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	delivered, err := r.Capture(ctx, testRequest("periodic-expired", "Synthetic expired receipt"))
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	pending, err := r.Capture(ctx, testRequest("periodic-pending", "Synthetic pending receipt"))
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	key, env, err := r.store.loadReceipt(delivered.Receipt.ReceiptID)
	if err != nil {
		t.Fatal(err)
	}
	env.Receipt.State = ReceiptDelivered
	env.Request = nil
	env.Receipt.Content = nil
	env.TombstoneUntil = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	if err = r.store.updateEnvelope(key, env, time.Time{}, 1); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		_, _, err = r.store.loadReceipt(delivered.Receipt.ReceiptID)
		if errors.Is(err, ErrReceiptNotFoundSentinel) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("periodic worker did not prune expired delivered receipt: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	if got, err := r.LookupReceipt(context.Background(), pending.Receipt.ReceiptID); err != nil || got.State != ReceiptPending {
		t.Fatalf("periodic pruning removed a pending capture: %#v err=%v", got, err)
	}
}

func TestReceiptListingOmitsLargeCaptureContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	keys := &testKeys{}
	adapter := &testAdapter{identity: testIdentity()}
	r, _ := testRuntime(t, path, keys, adapter)
	defer r.Close()
	if err := r.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := testRequest("large-list-item", "synthetic")
	request.Record.Payload = json.RawMessage(`{"blob":"` + strings.Repeat("x", 3<<20) + `"}`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	accepted, err := r.Capture(ctx, request)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.ListReceipts(context.Background(), ListReceiptsRequest{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= MaxFrameBytes || len(page.Receipts) != 1 || page.Receipts[0].Content != nil {
		t.Fatalf("list response was not bounded: bytes=%d response=%#v", len(encoded), page)
	}
	detail, err := r.LookupReceipt(context.Background(), accepted.Receipt.ReceiptID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Content == nil || len(detail.Content.Record.Payload) < 3<<20 {
		t.Fatal("single-item lookup did not preserve queued content")
	}
}

func TestRecoverableBindingAndCredentialBlocksResumeSafely(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	keys := &testKeys{}
	var authPhase atomic.Int32
	var deliverCalls atomic.Int32
	var deliveredCredential string
	adapter := &testAdapter{identity: testIdentity()}
	adapter.authenticate = func(context.Context, Binding) (AuthenticatedIdentity, error) {
		identity := testIdentity()
		if authPhase.Load() == 1 {
			identity.CredentialID = "credential_changed"
		}
		return identity, nil
	}
	adapter.deliver = func(_ context.Context, item DeliveryItem) (DeliveryResult, error) {
		deliveredCredential = item.CredentialID
		if deliverCalls.Add(1) == 1 {
			return DeliveryResult{}, &DeliveryFailure{Code: DeliveryConnectivity, Retryable: true}
		}
		return successResult(), nil
	}
	r, store := testDeferredRuntime(t, path, keys, adapter)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	accepted, err := r.Capture(ctx, testRequest("restore-binding", "Synthetic binding restore"))
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	original, _ := r.GetConfiguration(context.Background())
	changed := original
	changed.ServerURL = "https://other.invalid"
	changed.CredentialFingerprint = "SHA256:other"
	if err = r.SetConfiguration(context.Background(), changed); err != nil {
		t.Fatal(err)
	}
	if progressed, processErr := r.processOne(context.Background()); processErr != nil || !progressed {
		t.Fatalf("changed binding was not recorded: progressed=%v err=%v", progressed, processErr)
	}
	blocked, err := r.LookupReceipt(context.Background(), accepted.Receipt.ReceiptID)
	if err != nil || blocked.State != ReceiptBlocked || blocked.DeliveryCode != DeliveryCredentialChanged {
		t.Fatalf("changed binding did not block safely: receipt=%#v err=%v", blocked, err)
	}
	if err = r.SetConfiguration(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	if progressed, processErr := r.processOne(context.Background()); processErr != nil || !progressed {
		t.Fatalf("restored binding was not reverified: progressed=%v err=%v", progressed, processErr)
	}
	if adapter.authCalls.Load() != 1 || deliverCalls.Load() != 1 || deliveredCredential != "credential_synthetic" {
		t.Fatalf("first restored attempt used wrong identity: auth=%d deliver=%d credential=%q", adapter.authCalls.Load(), deliverCalls.Load(), deliveredCredential)
	}
	authPhase.Store(1)
	if err = r.SetConfiguration(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	if progressed, processErr := r.processOne(context.Background()); processErr != nil || !progressed {
		t.Fatalf("changed credential was not checkpointed as blocked: progressed=%v err=%v", progressed, processErr)
	}
	blocked, err = r.LookupReceipt(context.Background(), accepted.Receipt.ReceiptID)
	if err != nil || blocked.State != ReceiptBlocked || blocked.DeliveryCode != DeliveryCredentialChanged {
		t.Fatalf("changed credential did not block safely: receipt=%#v err=%v", blocked, err)
	}
	if deliverCalls.Load() != 1 {
		t.Fatal("delivery was attempted under a changed credential")
	}
	authPhase.Store(0)
	if err = r.SetConfiguration(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	if progressed, processErr := r.processOne(context.Background()); processErr != nil || !progressed {
		t.Fatalf("restored credential was not reverified: progressed=%v err=%v", progressed, processErr)
	}
	delivered, err := r.LookupReceipt(context.Background(), accepted.Receipt.ReceiptID)
	if err != nil || delivered.State != ReceiptDelivered || delivered.CentralRecordID != "rec_synthetic" {
		t.Fatalf("restored identity did not deliver: receipt=%#v err=%v", delivered, err)
	}
	_, env, err := store.loadReceipt(accepted.Receipt.ReceiptID)
	if err != nil || env.CredentialID != "credential_synthetic" || deliveredCredential != "credential_synthetic" {
		t.Fatalf("checkpointed identity changed: env=%q delivered=%q err=%v", env.CredentialID, deliveredCredential, err)
	}
}

func TestSetConfigurationInvalidatesConnectionObservation(t *testing.T) {
	keys := &testKeys{}
	adapter := &testAdapter{identity: testIdentity()}
	r, _ := testDeferredRuntime(t, filepath.Join(t.TempDir(), "queue.sqlite"), keys, adapter)
	if _, err := r.VerifyConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err := r.Status(context.Background())
	if err != nil || status.Connection.State != ConnectionConnected || status.Connection.CheckedAt == "" {
		t.Fatalf("verification did not publish a checked connected state: status=%+v err=%v", status.Connection, err)
	}
	c, err := r.GetConfiguration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.SigningKeyPath = "/tmp/another-synthetic.key"
	if err = r.SetConfiguration(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	status, err = r.Status(context.Background())
	if err != nil || status.Connection.State != ConnectionUnknown || status.Connection.CheckedAt != "" {
		t.Fatalf("changed signer reference retained old observation: status=%+v err=%v", status.Connection, err)
	}
}

func TestVerifyConnectionDoesNotReportSuccessWhenAuthenticatedReadFails(t *testing.T) {
	keys := &testKeys{}
	adapter := &testAdapter{identity: testIdentity()}
	adapter.authenticate = func(context.Context, Binding) (AuthenticatedIdentity, error) {
		return AuthenticatedIdentity{}, &DeliveryFailure{Code: DeliveryAuthentication}
	}
	adapter.verifyConnection = func(context.Context, Binding) (AuthenticatedIdentity, error) {
		return AuthenticatedIdentity{}, &DeliveryFailure{Code: DeliveryServerRejected}
	}
	r, _ := testDeferredRuntime(t, filepath.Join(t.TempDir(), "queue.sqlite"), keys, adapter)
	if _, err := r.VerifyConnection(context.Background()); err == nil {
		t.Fatal("verification succeeded after the authenticated resource read failed")
	}
	status, err := r.Status(context.Background())
	if err != nil || status.Connection.State == ConnectionConnected || status.Connection.CheckedAt == "" {
		t.Fatalf("failed resource read was reported as successful verification: status=%+v err=%v", status.Connection, err)
	}
}

func TestDelayedVerificationCannotRepublishOldConfigurationStatus(t *testing.T) {
	keys := &testKeys{}
	started := make(chan struct{})
	release := make(chan struct{})
	adapter := &testAdapter{identity: testIdentity()}
	adapter.authenticate = func(context.Context, Binding) (AuthenticatedIdentity, error) {
		close(started)
		<-release
		return testIdentity(), nil
	}
	r, _ := testDeferredRuntime(t, filepath.Join(t.TempDir(), "queue.sqlite"), keys, adapter)
	result := make(chan error, 1)
	go func() {
		_, err := r.VerifyConnection(context.Background())
		result <- err
	}()
	<-started
	c, err := r.GetConfiguration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.ServerURL = "https://new-synthetic.invalid"
	c.CredentialFingerprint = "SHA256:new-synthetic"
	c.PublicKeyPath = "/tmp/new-synthetic.pub"
	c.SigningKeyPath = "/tmp/new-synthetic.key"
	if err = r.SetConfiguration(context.Background(), c); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err = <-result; !errors.Is(err, ErrConflictSentinel) {
		t.Fatalf("stale verification should report configuration conflict, got %v", err)
	}
	status, err := r.Status(context.Background())
	if err != nil || status.Connection.State != ConnectionUnknown || status.Connection.CheckedAt != "" {
		t.Fatalf("old verification republished status for the previous configuration: status=%+v err=%v", status.Connection, err)
	}
}

func TestDelayedDeliveryIsDrainedBeforeConfigurationChanges(t *testing.T) {
	keys := &testKeys{}
	entered := make(chan struct{})
	release := make(chan struct{})
	adapter := &testAdapter{identity: testIdentity()}
	adapter.deliver = func(context.Context, DeliveryItem) (DeliveryResult, error) {
		close(entered)
		<-release
		return successResult(), nil
	}
	r, _ := testDeferredRuntime(t, filepath.Join(t.TempDir(), "queue.sqlite"), keys, adapter)
	request := testRequest("config-during-delivery", "Synthetic serialized delivery")
	if _, err := r.Capture(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	processDone := make(chan error, 1)
	go func() {
		_, err := r.processOne(context.Background())
		processDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("delivery did not enter adapter")
	}
	c, err := r.GetConfiguration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.ServerURL = "https://new-synthetic.invalid"
	c.CredentialFingerprint = "SHA256:new-synthetic"
	configDone := make(chan error, 1)
	go func() { configDone <- r.SetConfiguration(context.Background(), c) }()
	select {
	case err = <-configDone:
		t.Fatalf("configuration changed before admitted delivery drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err = <-processDone; err != nil {
		t.Fatal(err)
	}
	if err = <-configDone; err != nil {
		t.Fatal(err)
	}
	status, err := r.Status(context.Background())
	if err != nil || status.Connection.State != ConnectionUnknown || status.Connection.CheckedAt != "" {
		t.Fatalf("old delivery republished connected status after configuration change: status=%+v err=%v", status.Connection, err)
	}
}

func TestQuiesceBarrierPreventsLateWorkerAdmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	adapter := &testAdapter{identity: testIdentity()}
	r, _ := testDeferredRuntime(t, path, &testKeys{}, adapter)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	accepted, err := r.Capture(ctx, testRequest("quiesce-barrier", "Synthetic quiesce barrier"))
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	r.workerAdmissionHook = func() { close(entered); <-release }
	processDone := make(chan error, 1)
	go func() {
		_, processErr := r.processOne(context.Background())
		processDone <- processErr
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not reach the admission barrier")
	}
	if err = r.Quiesce(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err = <-processDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not finish after quiesce acknowledgement")
	}
	if adapter.calls.Load() != 0 || adapter.authCalls.Load() != 0 {
		t.Fatal("worker started delivery after quiesce barrier completed")
	}
	receipt, err := r.LookupReceipt(context.Background(), accepted.Receipt.ReceiptID)
	if err != nil || receipt.State != ReceiptPending {
		t.Fatalf("queued capture changed during quiesce: %#v err=%v", receipt, err)
	}
}

func TestQuiesceWaitsForAdmittedCaptureDurableCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	r, _ := testDeferredRuntime(t, path, &testKeys{}, &testAdapter{identity: testIdentity()})
	defer r.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	r.intakeAdmissionHook = func() { close(entered); <-release }
	captureDone := make(chan error, 1)
	request := testRequest("quiesce-intake-drain", "Synthetic admitted capture")
	go func() {
		_, err := r.Capture(context.Background(), request)
		captureDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("capture did not enter the durable-commit barrier")
	}
	quiesced := make(chan error, 1)
	go func() { quiesced <- r.Quiesce(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for {
		status, err := r.Status(context.Background())
		if err == nil && status.Lifecycle == LifecycleQuiescing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("quiesce did not close intake before draining")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-quiesced:
		t.Fatalf("quiesce acknowledged before admitted local commit finished: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-quiesced:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("quiesce did not acknowledge after local commit drained")
	}
	if err := <-captureDone; err != nil {
		t.Fatalf("admitted capture failed before local durability: %v", err)
	}
	items, err := r.ListReceipts(context.Background(), ListReceiptsRequest{Limit: 10})
	if err != nil || len(items.Receipts) != 1 || items.Receipts[0].State != ReceiptPending {
		t.Fatalf("acknowledged quiesce did not preserve the admitted durable receipt: %#v err=%v", items, err)
	}
	if _, err = r.Capture(context.Background(), testRequest("after-quiesce", "Synthetic blocked capture")); !errors.Is(err, ErrServiceQuiescingSentinel) {
		t.Fatalf("new intake was accepted after quiesce acknowledgement: %v", err)
	}
}

func TestFinalStorageAndSocketDirectorySymlinksDoNotChangeTargetPermissions(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	storageLink := filepath.Join(root, "storage-link")
	if err := os.Symlink(target, storageLink); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(filepath.Join(storageLink, "queue.sqlite"), &testKeys{}); !errors.Is(err, ErrStorageFailedSentinel) {
		t.Fatalf("storage directory symlink was accepted: %v", err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("rejected storage symlink changed target permissions: %v err=%v", info.Mode().Perm(), err)
	}
	socketLink := filepath.Join(root, "socket-link")
	if err := os.Symlink(target, socketLink); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenIPC(filepath.Join(socketLink, "service.sock"), nil); !errors.Is(err, ErrStorageFailedSentinel) {
		t.Fatalf("socket directory symlink was accepted: %v", err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("rejected socket symlink changed target permissions: %v err=%v", info.Mode().Perm(), err)
	}
}

func TestLocalCommitFailureProcessHelper(t *testing.T) {
	if os.Getenv("SCRATCHPAD_LOCAL_COMMIT_HELPER") != "1" {
		return
	}
	database := os.Getenv("SCRATCHPAD_LOCAL_COMMIT_DATABASE")
	socket := os.Getenv("SCRATCHPAD_LOCAL_COMMIT_SOCKET")
	keys := &testKeys{key: []byte(strings.Repeat("k", 32))}
	store, err := OpenStore(database, keys)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRuntime(context.Background(), ServiceOptions{Store: store, Adapter: &testAdapter{identity: testIdentity()}, DeferWorker: true, ExitAfterQuiesce: true, HealthInterval: time.Hour})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	defer r.Close()
	if err = r.SetConfiguration(context.Background(), ServiceConfiguration{ServerURL: "https://scratchpad.invalid", CredentialFingerprint: "SHA256:synthetic", PublicKeyPath: "/tmp/synthetic.pub", SigningKeyPath: "/tmp/synthetic.key"}); err != nil {
		t.Fatal(err)
	}
	server, err := ListenIPC(socket, r)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.StartWorker(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = server.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRealProcessRejectsCaptureWhenSQLiteCannotCommit(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "queue.sqlite")
	keys := &testKeys{key: []byte(strings.Repeat("k", 32))}
	store, err := OpenStore(database, keys)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(shortPrivateTempDir(t), "service.sock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLocalCommitFailureProcessHelper$")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_LOCAL_COMMIT_HELPER=1", "SCRATCHPAD_LOCAL_COMMIT_DATABASE="+database, "SCRATCHPAD_LOCAL_COMMIT_SOCKET="+socket)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	client := NewClient(socket)
	client.Timeout = time.Second
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = client.Status(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
			t.Fatalf("synthetic service process did not become reachable: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = client.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	lockDB, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := lockDB.Conn(context.Background())
	if err != nil {
		_ = lockDB.Close()
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		_ = conn.Close()
		_ = lockDB.Close()
		t.Fatal(err)
	}
	_, captureErr := client.Capture(context.Background(), testRequest("process-commit-failure", "Synthetic commit failure"))
	_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
	_ = conn.Close()
	_ = lockDB.Close()
	if !errors.Is(captureErr, ErrStorageFailedSentinel) {
		t.Fatalf("capture did not fail closed when its durable commit was blocked: %v", captureErr)
	}
	checkDB, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	var captureRows int
	if err = checkDB.QueryRow("SELECT count(*) FROM capture_items WHERE state NOT IN ('settings','configuration')").Scan(&captureRows); err != nil {
		_ = checkDB.Close()
		t.Fatal(err)
	}
	_ = checkDB.Close()
	if captureRows != 0 {
		t.Fatalf("failed process capture left a successful receipt: %d", captureRows)
	}
	if err = client.Quiesce(context.Background()); err != nil {
		t.Fatalf("quiesce after local commit failure: %v", err)
	}
	if err = cmd.Wait(); err != nil {
		t.Fatalf("synthetic service process did not exit cleanly: %v", err)
	}
}

func TestQuiesceControlUsesDrainDeadlineAndKeepsSocketUntilHandlersDrain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	store, err := OpenStore(path, &testKeys{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRuntime(context.Background(), ServiceOptions{Store: store, Adapter: &testAdapter{identity: testIdentity()}, ExitAfterQuiesce: true})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	defer r.Close()
	socket := filepath.Join(shortPrivateTempDir(t), "service.sock")
	server, err := ListenIPC(socket, r)
	if err != nil {
		t.Fatal(err)
	}
	server.RequestTimeout = 20 * time.Millisecond
	server.ControlTimeout = 300 * time.Millisecond
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(context.Background()) }()
	client := NewClient(socket)
	client.Timeout = time.Second
	r.workMu.Lock()
	quiesceDone := make(chan error, 1)
	go func() { quiesceDone <- client.Quiesce(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for {
		r.mu.Lock()
		isQuiescing := r.quiescing
		r.mu.Unlock()
		if isQuiescing {
			break
		}
		if time.Now().After(deadline) {
			r.workMu.Unlock()
			t.Fatal("quiesce did not enter while waiting for its drain")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err = os.Lstat(socket); err != nil {
		r.workMu.Unlock()
		t.Fatalf("socket was unlinked while a handler remained admitted: %v", err)
	}
	select {
	case err = <-quiesceDone:
		r.workMu.Unlock()
		t.Fatalf("quiesce acknowledged before worker drain: %v", err)
	case <-time.After(60 * time.Millisecond):
	}
	r.workMu.Unlock()
	select {
	case err = <-quiesceDone:
		if err != nil {
			t.Fatalf("quiesce response missed its extended control deadline: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("quiesce did not return after releasing the drain")
	}
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("server did not finish after quiesce response")
	}
	if _, err = os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remained after all IPC handlers drained: %v", err)
	}
}

type failingResumeService struct{ Service }

func (s failingResumeService) ResumeAfterUpdate(context.Context) error {
	return ErrServiceStoppedSentinel
}

func TestIPCFailedResumeKeepsQuiescingGateClosed(t *testing.T) {
	r, _ := testRuntime(t, filepath.Join(t.TempDir(), "queue.sqlite"), &testKeys{key: []byte(strings.Repeat("k", 32))}, &testAdapter{identity: testIdentity()}, false, false)
	socket := filepath.Join(shortPrivateTempDir(t), "service.sock")
	server, err := ListenIPC(socket, failingResumeService{Service: r})
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	serveCtx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(serveCtx) }()
	defer func() {
		cancel()
		<-serveDone
		r.Close()
	}()
	client := NewClient(socket)
	client.Timeout = time.Second
	if err = client.Quiesce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = client.ResumeAfterUpdate(context.Background()); !errors.Is(err, ErrServiceStoppedSentinel) {
		t.Fatalf("failed runtime resume did not report its failure: %v", err)
	}
	if _, err = client.Status(context.Background()); !errors.Is(err, ErrServiceQuiescingSentinel) {
		t.Fatalf("failed runtime resume reopened IPC gate: %v", err)
	}
}

func TestIPCResumeAfterUpdateReopensGateAndPreservesPause(t *testing.T) {
	database := filepath.Join(t.TempDir(), "queue.sqlite")
	keys := &testKeys{key: []byte(strings.Repeat("k", 32))}
	r, _ := testRuntime(t, database, keys, &testAdapter{identity: testIdentity()}, false, false)
	socket := filepath.Join(shortPrivateTempDir(t), "service.sock")
	server, err := ListenIPC(socket, r)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	serveCtx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(serveCtx) }()
	client := NewClient(socket)
	client.Timeout = time.Second

	if err = client.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = client.Quiesce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Status(context.Background()); !errors.Is(err, ErrServiceQuiescingSentinel) {
		t.Fatalf("quiesced service accepted ordinary IPC before updater resume: %v", err)
	}
	if err = client.ResumeAfterUpdate(context.Background()); err != nil {
		t.Fatalf("IPC resume was rejected by the quiescing gate: %v", err)
	}
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != LifecyclePaused {
		t.Fatalf("resume-after-update changed the user's paused setting: %s", status.Lifecycle)
	}

	cancel()
	if err = <-serveDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("IPC server shutdown: %v", err)
	}
	r.Close()
	r2, _ := testRuntime(t, database, keys, &testAdapter{identity: testIdentity()}, false, false)
	defer r2.Close()
	status, err = r2.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != LifecyclePaused {
		t.Fatalf("paused state was not durable across service restart: %s", status.Lifecycle)
	}
}

func TestProductionQuiesceCannotResumeAfterExitCommit(t *testing.T) {
	r, _ := testRuntime(t, filepath.Join(t.TempDir(), "queue.sqlite"), &testKeys{key: []byte(strings.Repeat("k", 32))}, &testAdapter{identity: testIdentity()}, false, true)
	socket := filepath.Join(shortPrivateTempDir(t), "service.sock")
	server, err := ListenIPC(socket, r)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(context.Background()) }()
	client := NewClient(socket)
	client.Timeout = time.Second
	if err = client.Quiesce(context.Background()); err != nil {
		t.Fatalf("production quiesce handshake failed: %v", err)
	}
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("production service did not exit after quiesce acknowledgement")
	}
	if err = r.ResumeAfterUpdate(context.Background()); !errors.Is(err, ErrServiceQuiescingSentinel) {
		t.Fatalf("service claimed it could resume after exit was committed: %v", err)
	}
	status, err := r.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != LifecycleQuiescing {
		t.Fatalf("failed post-exit resume changed lifecycle: %s", status.Lifecycle)
	}
	r.Close()
}

func TestIPCVerifyPreservesTypedRemoteFailureCategories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	adapter := &testAdapter{identity: testIdentity()}
	r, _ := testDeferredRuntime(t, path, &testKeys{}, adapter)
	socket := filepath.Join(shortPrivateTempDir(t), "service.sock")
	server, err := ListenIPC(socket, r)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx) }()
	defer func() {
		cancel()
		<-serveDone
		r.Close()
	}()
	client := NewClient(socket)
	for _, code := range []DeliveryCode{DeliveryConnectivity, DeliveryAuthentication, DeliveryServerIncompatible} {
		adapter.authenticate = func(context.Context, Binding) (AuthenticatedIdentity, error) {
			return AuthenticatedIdentity{}, &DeliveryFailure{Code: code}
		}
		_, err = client.VerifyConnection(context.Background())
		var failure *DeliveryFailure
		if !errors.As(err, &failure) || failure.Code != code || errors.Is(err, ErrStorageFailedSentinel) {
			t.Fatalf("IPC changed %s into a local storage failure: %#v err=%v", code, failure, err)
		}
		if strings.TrimSpace(safeMessage(ErrorCode(code))) == "" || safeMessage(ErrorCode(code)) == safeMessage(ErrInternal) {
			t.Fatalf("remote category %s has no truthful safe message", code)
		}
	}
}

func TestIdleHealthProbeDetectsOutageRecoveryAndThrottles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	keys := &testKeys{}
	var offline atomic.Bool
	var authCalls atomic.Int32
	offline.Store(true)
	adapter := &testAdapter{identity: testIdentity()}
	adapter.authenticate = func(context.Context, Binding) (AuthenticatedIdentity, error) {
		authCalls.Add(1)
		if offline.Load() {
			return AuthenticatedIdentity{}, &DeliveryFailure{Code: DeliveryConnectivity, Retryable: true}
		}
		return testIdentity(), nil
	}
	store, err := OpenStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRuntime(context.Background(), ServiceOptions{Store: store, Adapter: adapter, HealthInterval: 500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err = r.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := ServiceConfiguration{ServerURL: "https://scratchpad.invalid", CredentialFingerprint: "SHA256:synthetic", PublicKeyPath: "/tmp/synthetic.pub", SigningKeyPath: "/tmp/synthetic.key"}
	if err = r.SetConfiguration(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := r.Status(context.Background())
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if status.Connection.State == ConnectionConnectivity {
			if status.Pending != 0 || adapter.calls.Load() != 0 {
				t.Fatal("health probe created or delivered a capture while the queue was empty")
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	status, err := r.Status(context.Background())
	if err != nil || status.Connection.State != ConnectionConnectivity {
		t.Fatalf("idle outage was not observed: connection=%+v err=%v", status.Connection, err)
	}
	firstCount := authCalls.Load()
	time.Sleep(60 * time.Millisecond)
	firstCount = authCalls.Load()
	time.Sleep(60 * time.Millisecond)
	if count := authCalls.Load(); count != firstCount {
		t.Fatalf("health probing was not throttled to its configured cadence: first=%d later=%d", firstCount, count)
	}
	offline.Store(false)
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, err = r.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status.Connection.State == ConnectionConnected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("idle health probe did not observe recovery")
}

func TestIdleConnectionEvidenceExpiresWhenProbeStalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	keys := &testKeys{}
	var authCalls atomic.Int32
	probeBlocked := make(chan struct{})
	release := make(chan struct{})
	adapter := &testAdapter{identity: testIdentity()}
	adapter.authenticate = func(context.Context, Binding) (AuthenticatedIdentity, error) {
		if authCalls.Add(1) == 1 {
			return testIdentity(), nil
		}
		select {
		case <-probeBlocked:
		default:
			close(probeBlocked)
		}
		select {
		case <-release:
		case <-time.After(time.Second):
		}
		return testIdentity(), nil
	}
	store, err := OpenStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	const interval = 30 * time.Millisecond
	r, err := NewRuntime(context.Background(), ServiceOptions{Store: store, Adapter: adapter, HealthInterval: interval})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err = r.SetConfiguration(context.Background(), ServiceConfiguration{ServerURL: "https://scratchpad.invalid", CredentialFingerprint: "SHA256:synthetic", PublicKeyPath: "/tmp/synthetic.pub", SigningKeyPath: "/tmp/synthetic.key"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := r.Status(context.Background())
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if status.Connection.State == ConnectionConnected {
			break
		}
		time.Sleep(time.Millisecond)
	}
	status, err := r.Status(context.Background())
	if err != nil || status.Connection.State != ConnectionConnected {
		t.Fatalf("initial health evidence did not connect: status=%+v err=%v", status.Connection, err)
	}
	select {
	case <-probeBlocked:
	case <-time.After(time.Second):
		t.Fatal("periodic probe did not start")
	}
	time.Sleep(2*interval + 10*time.Millisecond)
	status, err = r.Status(context.Background())
	if err != nil || status.Connection.State != ConnectionUnknown {
		t.Fatalf("stale connected evidence remained green: status=%+v err=%v", status.Connection, err)
	}
	close(release)
}

func TestQuiesceDrainsHealthProbeAndPreventsAnother(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	adapter := &testAdapter{identity: testIdentity()}
	adapter.authenticate = func(context.Context, Binding) (AuthenticatedIdentity, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return testIdentity(), nil
	}
	store, err := OpenStore(path, &testKeys{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRuntime(context.Background(), ServiceOptions{Store: store, Adapter: adapter, HealthInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err = r.SetConfiguration(context.Background(), ServiceConfiguration{ServerURL: "https://scratchpad.invalid", CredentialFingerprint: "SHA256:synthetic", PublicKeyPath: "/tmp/synthetic.pub", SigningKeyPath: "/tmp/synthetic.key"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("health probe did not enter authentication")
	}
	quiesced := make(chan error, 1)
	go func() { quiesced <- r.Quiesce(context.Background()) }()
	select {
	case err = <-quiesced:
		t.Fatalf("quiesce acknowledged before its in-flight health probe drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err = <-quiesced; err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	time.Sleep(3 * 20 * time.Millisecond)
	if after := calls.Load(); after != before {
		t.Fatalf("quiesced service started another health probe: before=%d after=%d", before, after)
	}
}

func TestDeferredWorkerDoesNotDeliverBeforeSupervisorStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	adapter := &testAdapter{identity: testIdentity()}
	r, _ := testDeferredRuntime(t, path, &testKeys{}, adapter)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	accepted, err := r.Capture(ctx, testRequest("deferred-worker", "Synthetic deferred start"))
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if adapter.authCalls.Load() != 0 {
		t.Fatal("worker authenticated before supervisor start")
	}
	if err = r.StartWorker(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		receipt, lookupErr := r.LookupReceipt(context.Background(), accepted.Receipt.ReceiptID)
		if lookupErr == nil && receipt.State == ReceiptDelivered {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("supervisor start did not release queued delivery")
}

func TestSchemaVersionInspectionIsReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	version, err := InspectSchemaVersion(path)
	if err != nil || version != 0 {
		t.Fatalf("missing database schema version: version=%d err=%v", version, err)
	}
	keys := &testKeys{}
	store, err := OpenStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	loadsBefore := keys.loads
	version, err = InspectSchemaVersion(path)
	if err != nil || version != QueueSchemaVersion || keys.loads != loadsBefore {
		t.Fatalf("schema inspection was not read-only: version=%d loads=%d err=%v", version, keys.loads, err)
	}
}

func TestStartupAndRuntimeFailClosedOnNonFirstEnvelopeCorruption(t *testing.T) {
	createQueue := func(t *testing.T, path string, keys *testKeys, adapter *testAdapter) *Runtime {
		t.Helper()
		r, _ := testRuntime(t, path, keys, adapter)
		if err := r.PauseDelivery(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"corrupt-first", "corrupt-second"} {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
			_, err := r.Capture(ctx, testRequest(id, "Synthetic corruption fixture"))
			cancel()
			if err != nil {
				t.Fatal(err)
			}
		}
		return r
	}
	corruptSecond := func(t *testing.T, db *sql.DB) {
		t.Helper()
		var id string
		if err := db.QueryRow("SELECT receipt_key FROM capture_items WHERE state='pending' ORDER BY rowid LIMIT 1 OFFSET 1").Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("UPDATE capture_items SET envelope=zeroblob(length(envelope)),created_ns=1 WHERE receipt_key=?", id); err != nil {
			t.Fatal(err)
		}
	}

	startupPath := filepath.Join(t.TempDir(), "startup-corrupt.sqlite")
	startupKeys := &testKeys{}
	startupRuntime := createQueue(t, startupPath, startupKeys, &testAdapter{identity: testIdentity()})
	startupRuntime.Close()
	db, err := sql.Open("sqlite", startupPath)
	if err != nil {
		t.Fatal(err)
	}
	corruptSecond(t, db)
	_ = db.Close()
	if store, openErr := OpenStore(startupPath, startupKeys); openErr == nil {
		_ = store.Close()
		t.Fatal("startup accepted corruption in a non-first encrypted row")
	}
	if _, openErr := OpenStore(startupPath, startupKeys); openErr == nil {
		t.Fatal("corrupt queue could reach a successful service startup/capture path")
	}

	runtimePath := filepath.Join(t.TempDir(), "runtime-corrupt.sqlite")
	adapter := &testAdapter{identity: testIdentity()}
	r := createQueue(t, runtimePath, &testKeys{}, adapter)
	r.store.mu.Lock()
	corruptSecond(t, r.store.db)
	r.store.mu.Unlock()
	if err = r.ResumeDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := r.Status(context.Background())
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if !status.StorageAvailable {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, err := r.Status(context.Background())
	if err != nil || status.StorageAvailable {
		t.Fatalf("runtime still reported healthy storage after decrypt failure: %#v err=%v", status, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	_, err = r.Capture(ctx, testRequest("after-corrupt", "Synthetic must-fail capture"))
	cancel()
	if !errors.Is(err, ErrStorageFailedSentinel) {
		t.Fatalf("capture succeeded or returned an unsafe status after corruption: %v", err)
	}
	if adapter.calls.Load() != 0 {
		t.Fatal("worker delivered another capture after storage corruption")
	}
}

func TestIPCRejectsUnknownAndTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	r, _ := testRuntime(t, path, &testKeys{}, &testAdapter{identity: testIdentity()})
	defer r.Close()
	socketDir, err := os.MkdirTemp("/tmp", "sp-ipc-strict-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketDir)
	socket := filepath.Join(socketDir, "service.sock")
	server, err := ListenIPC(socket, r)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()

	exchange := func(payload string) Response {
		t.Helper()
		conn, dialErr := net.DialTimeout("unix", socket, time.Second)
		if dialErr != nil {
			t.Fatal(dialErr)
		}
		defer conn.Close()
		if writeErr := writeFrame(conn, []byte(payload)); writeErr != nil {
			t.Fatal(writeErr)
		}
		frame, readErr := readFrame(conn)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var response Response
		if json.Unmarshal(frame, &response) != nil {
			t.Fatal("invalid response frame")
		}
		return response
	}

	unknownEnvelope := exchange(`{"version":1,"id":"unknown-envelope","op":"status","body":{},"extra":true}`)
	if unknownEnvelope.Error == nil || unknownEnvelope.Error.Code != ErrInvalidRequest {
		t.Fatalf("unknown envelope field was accepted: %#v", unknownEnvelope)
	}
	trailingEnvelope := exchange(`{"version":1,"id":"trailing-envelope","op":"status","body":{}} {}`)
	if trailingEnvelope.Error == nil || trailingEnvelope.Error.Code != ErrInvalidRequest {
		t.Fatalf("trailing envelope JSON was accepted: %#v", trailingEnvelope)
	}
	unknownBody := exchange(`{"version":1,"id":"unknown-body","op":"set_configuration","body":{"configuration":{"serverURL":"https://scratchpad.invalid","credentialFingerprint":"SHA256:synthetic","publicKeyPath":"/tmp/synthetic.pub","signingKeyPath":"/tmp/synthetic.key"},"extra":true}}`)
	if unknownBody.Error == nil || unknownBody.Error.Code != ErrConflict {
		t.Fatalf("unknown operation-body field was accepted: %#v", unknownBody)
	}
	unknownEmptyBody := exchange(`{"version":1,"id":"unknown-empty","op":"status","body":{"extra":true}}`)
	if unknownEmptyBody.Error == nil || unknownEmptyBody.Error.Code != ErrConflict {
		t.Fatalf("unknown field on empty operation body was accepted: %#v", unknownEmptyBody)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("IPC server did not stop")
	}
}

func TestRetryAndDeliveredReceiptDropCaptureContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	keys := &testKeys{}
	var calls atomic.Int32
	adapter := &testAdapter{identity: testIdentity(), deliver: func(context.Context, DeliveryItem) (DeliveryResult, error) {
		if calls.Add(1) == 1 {
			return DeliveryResult{}, &DeliveryFailure{Code: DeliveryConnectivity, Retryable: true}
		}
		return successResult(), nil
	}}
	r, _ := testRuntime(t, path, keys, adapter)
	defer r.Close()
	request := testRequest("retry-me", "Synthetic retry")
	request.ClientSavedAt = ""
	out, err := r.Capture(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		receipt, e := r.LookupReceipt(context.Background(), out.Receipt.ReceiptID)
		if e == nil && receipt.State == ReceiptDelivered {
			if receipt.Content != nil {
				t.Fatal("delivered receipt retained capture content")
			}
			if receipt.CentralRecordID != "rec_synthetic" || receipt.CentralProjectID != "proj_synthetic" {
				t.Fatalf("missing central receipt identity: %#v", receipt)
			}
			if calls.Load() < 2 {
				t.Fatalf("retry did not occur: calls=%d", calls.Load())
			}
			repeated, retryErr := r.Capture(context.Background(), request)
			if retryErr != nil || repeated.Receipt.ReceiptID != out.Receipt.ReceiptID || repeated.Receipt.State != ReceiptDelivered {
				t.Fatalf("delivered duplicate was not stable: receipt=%#v err=%v", repeated.Receipt, retryErr)
			}
			changed := request
			changed.Record.Title = "changed after delivery"
			if _, retryErr = r.Capture(context.Background(), changed); !errors.Is(retryErr, ErrConflictSentinel) {
				t.Fatalf("changed payload after delivery should conflict, got %v", retryErr)
			}
			if calls.Load() != 2 {
				t.Fatalf("delivered duplicate caused a second central call: %d", calls.Load())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("retry did not deliver before timeout")
}

func TestDeliveredRetryAfterRestartReturnsReceiptWithoutCachedServerObjects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	keys := &testKeys{}
	adapter := &testAdapter{identity: testIdentity()}
	r, _ := testDeferredRuntime(t, path, keys, adapter)
	request := testRequest("restart-delivered-retry", "Synthetic restart retry")
	if _, err := r.Capture(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if progressed, err := r.processOne(context.Background()); err != nil || !progressed {
		t.Fatalf("initial capture did not deliver: progressed=%v err=%v", progressed, err)
	}
	r.Close()

	restarted, _ := testDeferredRuntime(t, path, keys, adapter)
	defer restarted.Close()
	repeated, err := restarted.Capture(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Outcome != OutcomeDelivered || repeated.Receipt.State != ReceiptDelivered || repeated.Receipt.CentralRecordID != "rec_synthetic" || repeated.Receipt.CentralProjectID != "proj_synthetic" {
		t.Fatalf("restart retry did not return the durable delivered receipt: %#v", repeated)
	}
	if len(repeated.CentralRecord) != 0 || len(repeated.CentralProject) != 0 || len(repeated.Mirror) != 0 {
		t.Fatal("restart retry fabricated or retained central response objects")
	}
	if adapter.calls.Load() != 1 {
		t.Fatalf("delivered restart retry made an additional central call: %d", adapter.calls.Load())
	}
}

func TestDeliveredReceiptWaitsForBoundedFastResultPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	adapter := &testAdapter{identity: testIdentity()}
	r, _ := testDeferredRuntime(t, path, &testKeys{}, adapter)
	defer r.Close()
	request := testRequest("result-publication-race", "Synthetic result publication")
	if _, err := r.Capture(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	published := make(chan struct{})
	release := make(chan struct{})
	r.resultPublishHook = func() { close(published); <-release }
	resultDone := make(chan CaptureOutcome, 1)
	resultErr := make(chan error, 1)
	go func() {
		out, err := r.Capture(context.Background(), request)
		resultDone <- out
		resultErr <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		r.waitersMu.Lock()
		waiting := r.waiters[request.RequestID] != nil
		r.waitersMu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("live retry did not register for transient delivery results")
		}
		time.Sleep(time.Millisecond)
	}
	processDone := make(chan error, 1)
	go func() {
		_, err := r.processOne(context.Background())
		processDone <- err
	}()
	select {
	case <-published:
	case <-time.After(time.Second):
		t.Fatal("delivery did not reach the result publication barrier")
	}
	select {
	case <-resultDone:
		t.Fatal("delivered retry returned before the matching fast result was published")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-processDone; err != nil {
		t.Fatal(err)
	}
	result := <-resultDone
	if err := <-resultErr; err != nil {
		t.Fatal(err)
	}
	if len(result.CentralRecord) == 0 || len(result.CentralProject) == 0 {
		t.Fatal("fast delivered retry lost available central response objects")
	}
}

func TestLiveFastPathPreservesPayloadAboveResultCacheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	large := json.RawMessage(`{"synthetic":"` + strings.Repeat("x", (2<<20)+(64<<10)) + `"}`)
	adapter := &testAdapter{identity: testIdentity(), deliver: func(context.Context, DeliveryItem) (DeliveryResult, error) {
		result := successResult()
		result.CentralRecord = large
		return result, nil
	}}
	r, _ := testRuntime(t, path, &testKeys{}, adapter)
	defer r.Close()
	out, err := r.Capture(context.Background(), testRequest("large-live-response", "Synthetic large response"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome != OutcomeDelivered || len(out.CentralRecord) <= fastResultCacheBytes {
		t.Fatalf("live result lost response above cache cap: outcome=%s bytes=%d", out.Outcome, len(out.CentralRecord))
	}
	encodedOutcome, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal(Response{Version: ProtocolVersion, ID: strings.Repeat("0", 256), Result: encodedOutcome})
	if err != nil || len(frame) > MaxFrameBytes {
		t.Fatalf("live result exceeded IPC frame limit: bytes=%d err=%v", len(frame), err)
	}
}

func TestLiveFastPathSurvivesCachePressureBeforeWaiterPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	adapter := &testAdapter{identity: testIdentity()}
	r, _ := testDeferredRuntime(t, path, &testKeys{}, adapter)
	defer r.Close()
	pressure := DeliveryResult{CentralRecord: json.RawMessage(`{"synthetic":"` + strings.Repeat("p", 4096) + `"}`)}
	r.resultWaiterPublishHook = func() {
		for i := 0; i < fastResultCacheEntries+8; i++ {
			r.cacheDeliveryResultLocked(fmt.Sprintf("pressure-%d", i), pressure, time.Now())
		}
	}
	request := testRequest("waiter-cache-pressure", "Synthetic waiter pressure")
	type captureResult struct {
		out CaptureOutcome
		err error
	}
	done := make(chan captureResult, 1)
	go func() {
		out, err := r.Capture(context.Background(), request)
		done <- captureResult{out, err}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		r.waitersMu.Lock()
		waiting := r.waiters[request.RequestID] != nil
		r.waitersMu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("capture did not register its live result waiter")
		}
		time.Sleep(time.Millisecond)
	}
	for {
		receipts, err := r.ListReceipts(context.Background(), ListReceiptsRequest{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(receipts.Receipts) != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("capture was not durably accepted")
		}
		time.Sleep(time.Millisecond)
	}
	if progressed, err := r.processOne(context.Background()); err != nil || !progressed {
		t.Fatalf("delivery failed: progressed=%v err=%v", progressed, err)
	}
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.out.Outcome != OutcomeDelivered || len(result.out.CentralRecord) == 0 || !json.Valid(result.out.CentralRecord) {
			t.Fatalf("active caller lost its complete fast result under cache pressure: %#v", result.out)
		}
	case <-time.After(time.Second):
		t.Fatal("capture caller did not finish after delivery")
	}
	if len(r.results) != fastResultCacheEntries || r.resultsBytes > fastResultCacheBytes {
		t.Fatalf("shared result cache exceeded bounds: entries=%d bytes=%d", len(r.results), r.resultsBytes)
	}
}

func TestFastResultCacheIsBoundedAndExpires(t *testing.T) {
	r := &Runtime{results: map[string]cachedDeliveryResult{}}
	result := DeliveryResult{CentralRecord: json.RawMessage(`{"synthetic":"` + strings.Repeat("x", 1024) + `"}`)}
	r.resultsMu.Lock()
	for i := 0; i < fastResultCacheEntries+8; i++ {
		r.cacheDeliveryResultLocked(fmt.Sprintf("receipt-%d", i), result, time.Now())
	}
	r.resultsMu.Unlock()
	if len(r.results) != fastResultCacheEntries || r.resultsBytes > fastResultCacheBytes {
		t.Fatalf("fast result cache exceeded bounds: entries=%d bytes=%d", len(r.results), r.resultsBytes)
	}
	oldest := ""
	var oldestTime time.Time
	for id, cached := range r.results {
		if oldest == "" || cached.storedAt.Before(oldestTime) {
			oldest = id
			oldestTime = cached.storedAt
		}
	}
	r.resultsMu.Lock()
	r.results[oldest] = cachedDeliveryResult{result: result, storedAt: time.Now().Add(-fastResultCacheTTL), byteCount: deliveryResultBytes(result)}
	r.resultsMu.Unlock()
	if _, ok := r.deliveryResult(oldest); ok {
		t.Fatal("expired server response remained in fast result cache")
	}
}

func TestPauseStopExplicitStartAndDiscardControls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	keys := &testKeys{}
	adapter := &testAdapter{identity: testIdentity()}
	r, _ := testRuntime(t, path, keys, adapter)
	if err := r.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	out, err := r.Capture(ctx, testRequest("control-1", "Synthetic held"))
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if adapter.calls.Load() != 0 {
		t.Fatal("Pause did not suppress delivery")
	}
	discarded, err := r.DiscardReceipt(context.Background(), out.Receipt.ReceiptID)
	if err != nil {
		t.Fatal(err)
	}
	if discarded.State != ReceiptDiscarded || discarded.Content != nil {
		t.Fatalf("discard did not retain a minimal receipt: %#v", discarded)
	}
	if err = r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Capture(context.Background(), testRequest("control-2", "Stopped capture")); !errors.Is(err, ErrServiceStoppedSentinel) {
		t.Fatalf("stopped service accepted capture: %v", err)
	}
	r.Close()
	r, _ = testRuntime(t, path, keys, adapter)
	if !r.InitiallyStopped() {
		t.Fatal("ordinary process restart cleared Stop")
	}
	r.Close()
	r, _ = testRuntime(t, path, keys, adapter, true)
	defer r.Close()
	if r.InitiallyStopped() {
		t.Fatal("explicit Start did not clear Stop")
	}
	status, err := r.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != LifecyclePaused {
		t.Fatalf("explicit Start lost persisted Pause: %s", status.Lifecycle)
	}
}

func TestUncertainDeliveryFreezesProjectSelectionAndLegacyServerIsBlocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	keys := &testKeys{}
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	adapter := &testAdapter{identity: testIdentity(), deliver: func(context.Context, DeliveryItem) (DeliveryResult, error) {
		entered <- struct{}{}
		<-release
		return successResult(), nil
	}}
	r, _ := testRuntime(t, path, keys, adapter)
	defer r.Close()
	out, err := r.Capture(context.Background(), testRequest("uncertain", "Synthetic uncertain"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("delivery did not start")
	}
	if _, err = r.SelectProject(context.Background(), SelectProjectRequest{ReceiptID: out.Receipt.ReceiptID, ProjectID: "proj_other"}); !errors.Is(err, ErrConflictSentinel) {
		t.Fatalf("selection changed after possible commit: %v", err)
	}
	close(release)
	legacy := &testAdapter{identity: AuthenticatedIdentity{ServerURL: "https://scratchpad.invalid", CredentialID: "credential_synthetic"}}
	legacyRuntime, _ := testRuntime(t, filepath.Join(t.TempDir(), "legacy.sqlite"), &testKeys{}, legacy)
	defer legacyRuntime.Close()
	legacyReq := testRequest("legacy", "Synthetic legacy server")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	_, err = legacyRuntime.Capture(ctx, legacyReq)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if legacy.calls.Load() != 0 {
		t.Fatal("server lacking clientSavedAt capability received capture")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, e := legacyRuntime.Status(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if status.Connection.State == ConnectionServerIncompatible {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, err := legacyRuntime.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Connection.State != ConnectionServerIncompatible {
		t.Fatalf("legacy server was not rejected: %#v", status.Connection)
	}
}

func TestPrivateIPCStatusAndPause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	keys := &testKeys{}
	adapter := &testAdapter{identity: testIdentity()}
	r, _ := testRuntime(t, path, keys, adapter, false, true)
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
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != LifecycleRunning || !status.KeyAvailable {
		t.Fatalf("unexpected service status: %#v", status)
	}
	if err = client.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err = client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != LifecyclePaused {
		t.Fatalf("pause not reflected over IPC: %s", status.Lifecycle)
	}
	if err = client.Quiesce(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("IPC server did not stop")
	}
	r.Close()
	store, err := OpenStore(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	after, err := NewRuntime(context.Background(), ServiceOptions{Store: store, Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	status, err = after.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != LifecyclePaused {
		t.Fatalf("update quiesce changed persistent Pause state: %s", status.Lifecycle)
	}
}

func TestIPCServerCleanupDoesNotUnlinkReplacementSocket(t *testing.T) {
	path := filepath.Join(shortPrivateTempDir(t), "service.sock")
	store, err := OpenStore(filepath.Join(t.TempDir(), "queue.sqlite"), &testKeys{})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(context.Background(), ServiceOptions{Store: store, Adapter: &testAdapter{}, DeferWorker: true})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	defer runtime.Close()
	server, err := ListenIPC(path, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err = server.listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	replacement.SetUnlinkOnClose(false)
	defer replacement.Close()
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(server.socketInfo, before) {
		t.Fatal("replacement socket unexpectedly reused the original inode")
	}
	if err = server.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("old IPC server removed a replacement socket: before=%v after=%v err=%v", before, after, err)
	}
}

func TestDefaultSocketPathIgnoresEnvironmentSpecificTempDirectory(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "environment-specific-tmp"))
	t.Setenv("XDG_RUNTIME_DIR", "")
	want := filepath.Join("/tmp", "scratchpad-"+strconv.Itoa(os.Getuid()), "service.sock")
	if got := DefaultSocketPath(); got != want {
		t.Fatalf("default socket path depends on process TMPDIR: got %q want %q", got, want)
	}
}
