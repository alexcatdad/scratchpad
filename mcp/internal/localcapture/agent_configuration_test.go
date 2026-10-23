package localcapture

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentSocketPersistsAndResolvesForOriginalQueuedIdentity(t *testing.T) {
	adapter := &testAdapter{identity: testIdentity()}
	r, store := testDeferredRuntime(t, filepath.Join(t.TempDir(), "queue.sqlite"), &testKeys{}, adapter)
	cfg, err := r.GetConfiguration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg.SSHAgentSocket = "/tmp/first-fixture-agent.sock"
	if err := r.SetConfiguration(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	saved, err := store.configuration()
	if err != nil || saved.SSHAgentSocket != cfg.SSHAgentSocket {
		t.Fatalf("agent socket persistence: %+v %v", saved, err)
	}
	accepted, err := r.Capture(context.Background(), testRequest("agent-fixture", "Fixture"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.SSHAgentSocket = "/tmp/current-fixture-agent.sock"
	if err := r.SetConfiguration(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	var authBinding, deliveredBinding Binding
	adapter.authenticate = func(_ context.Context, b Binding) (AuthenticatedIdentity, error) {
		authBinding = b
		return testIdentity(), nil
	}
	adapter.deliver = func(_ context.Context, item DeliveryItem) (DeliveryResult, error) {
		deliveredBinding = item.Binding
		return successResult(), nil
	}
	if progressed, err := r.processOne(context.Background()); err != nil || !progressed {
		t.Fatalf("delivery: progressed=%v err=%v", progressed, err)
	}
	receipt, err := r.LookupReceipt(context.Background(), accepted.Receipt.ReceiptID)
	if err != nil || receipt.State != ReceiptDelivered {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	for _, b := range []Binding{authBinding, deliveredBinding} {
		if b.SSHAgentSocket != cfg.SSHAgentSocket || b.ServerURL != cfg.ServerURL || b.CredentialFingerprint != cfg.CredentialFingerprint {
			t.Fatalf("delivery used wrong signer or changed identity: %+v", b)
		}
	}
	adapter.verifyConnection = func(_ context.Context, b Binding) (AuthenticatedIdentity, error) {
		if b.SSHAgentSocket != cfg.SSHAgentSocket {
			t.Errorf("verification lost configured agent: %+v", b)
		}
		return testIdentity(), nil
	}
	if _, err := r.VerifyConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConfigurationRejectsInvalidAgentSocket(t *testing.T) {
	cfg := ServiceConfiguration{ServerURL: "https://scratchpad.invalid", CredentialFingerprint: "SHA256:fixture", PublicKeyPath: "/tmp/fixture.pub", SigningKeyPath: "/tmp/fixture.pub"}
	if err := validateConfiguration(cfg); err != nil {
		t.Fatal("legacy configuration rejected", err)
	}
	for _, socket := range []string{"relative.sock", "/tmp/agent\n.sock", "/tmp/agent\r.sock", "/tmp/agent\x00.sock", "/" + strings.Repeat("x", 4096)} {
		cfg.SSHAgentSocket = socket
		if err := validateConfiguration(cfg); err == nil {
			t.Fatalf("accepted invalid agent socket %q", socket)
		}
	}
}

func TestIPCVerificationAllowsSignerUnlockWithinBoundedContext(t *testing.T) {
	adapter := &testAdapter{identity: testIdentity()}
	adapter.verifyConnection = func(ctx context.Context, _ Binding) (AuthenticatedIdentity, error) {
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining < 89*time.Second || remaining > 90*time.Second {
			t.Errorf("verification did not receive bounded unlock time: %v %v", remaining, ok)
		}
		time.Sleep(100 * time.Millisecond)
		return testIdentity(), nil
	}
	r, _ := testDeferredRuntime(t, filepath.Join(t.TempDir(), "queue.sqlite"), &testKeys{}, adapter)
	socket := filepath.Join(shortPrivateTempDir(t), "service.sock")
	server, err := ListenIPC(socket, r)
	if err != nil {
		t.Fatal(err)
	}
	server.RequestTimeout = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	defer func() { cancel(); <-done }()
	client := NewClient(socket)
	client.Timeout = 50 * time.Millisecond
	if _, err := client.VerifyConnection(context.Background()); err != nil {
		t.Fatalf("explicit signer verification inherited ordinary IPC timeout: %v", err)
	}
	if client.Timeout != 50*time.Millisecond {
		t.Fatal("verification changed ordinary request timeout")
	}
}

func TestCandidateVerificationPreservesWorkingConfigurationUntilSuccess(t *testing.T) {
	adapter := &testAdapter{identity: testIdentity()}
	r, _ := testDeferredRuntime(t, filepath.Join(t.TempDir(), "queue.sqlite"), &testKeys{}, adapter)
	original, err := r.GetConfiguration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.VerifyConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
	proposed := original
	proposed.SSHAgentSocket = "/tmp/new-fixture-agent.sock"
	adapter.verifyConnection = func(context.Context, Binding) (AuthenticatedIdentity, error) {
		return AuthenticatedIdentity{}, &DeliveryFailure{Code: DeliveryAuthentication}
	}
	payload, _ := json.Marshal(VerifyConnectionRequest{Configuration: &proposed})
	server := &IPCServer{Service: r}
	if _, err := server.dispatch(Request{Op: OpVerifyConnection, Body: payload}); err == nil {
		t.Fatal("failed candidate was accepted")
	}
	current, err := r.GetConfiguration(context.Background())
	if err != nil || current != original {
		t.Fatalf("failed candidate replaced working configuration: %+v %v", current, err)
	}
	status, err := r.Status(context.Background())
	if err != nil || status.Connection.State != ConnectionConnected {
		t.Fatalf("failed candidate changed working connection observation: %+v %v", status.Connection, err)
	}
	adapter.verifyConnection = func(_ context.Context, b Binding) (AuthenticatedIdentity, error) {
		if b.SSHAgentSocket != proposed.SSHAgentSocket {
			t.Errorf("lost candidate socket: %+v", b)
		}
		return testIdentity(), nil
	}
	if _, err := server.dispatch(Request{Op: OpVerifyConnection, Body: payload}); err != nil {
		t.Fatal(err)
	}
	current, err = r.GetConfiguration(context.Background())
	if err != nil || current != proposed {
		t.Fatalf("verified candidate was not committed: %+v %v", current, err)
	}
	status, err = r.Status(context.Background())
	if err != nil || status.Connection.State != ConnectionConnected {
		t.Fatalf("verified configuration was not connected: %+v %v", status.Connection, err)
	}
}

func TestStaleCandidateVerificationDoesNotOverwriteConcurrentConfiguration(t *testing.T) {
	adapter := &testAdapter{identity: testIdentity()}
	r, _ := testDeferredRuntime(t, filepath.Join(t.TempDir(), "queue.sqlite"), &testKeys{}, adapter)
	original, err := r.GetConfiguration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate, concurrent := original, original
	candidate.SSHAgentSocket = "/tmp/candidate-fixture.sock"
	concurrent.SSHAgentSocket = "/tmp/concurrent-fixture.sock"
	adapter.verifyConnection = func(context.Context, Binding) (AuthenticatedIdentity, error) {
		if err := r.SetConfiguration(context.Background(), concurrent); err != nil {
			t.Fatal(err)
		}
		return testIdentity(), nil
	}
	if _, err := r.VerifyConfiguration(context.Background(), candidate); !errors.Is(err, ErrConflictSentinel) {
		t.Fatalf("stale candidate did not conflict: %v", err)
	}
	current, err := r.GetConfiguration(context.Background())
	if err != nil || current != concurrent {
		t.Fatalf("stale candidate overwrote current configuration: %+v %v", current, err)
	}
}
