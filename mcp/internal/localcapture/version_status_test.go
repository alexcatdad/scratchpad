package localcapture

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStatusReportsServiceAndProbedServerVersions(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "queue.sqlite"), &testKeys{})
	if err != nil {
		t.Fatal(err)
	}
	identity := testIdentity()
	identity.ServerVersion, identity.ServerCommit = "2026.10.31", "abc1234"
	adapter := &testAdapter{identity: identity}
	r, err := NewRuntime(context.Background(), ServiceOptions{Store: store, Adapter: adapter, DeferWorker: true, RetryMin: 10 * time.Millisecond, RetryMax: 20 * time.Millisecond, Version: "2026.10.3", Build: "12"})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	if err = r.SetConfiguration(context.Background(), ServiceConfiguration{ServerURL: "https://scratchpad.invalid", CredentialFingerprint: "SHA256:synthetic", PublicKeyPath: "/tmp/synthetic.pub", SigningKeyPath: "/tmp/synthetic.key"}); err != nil {
		t.Fatal(err)
	}

	before, err := r.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before.ServiceVersion != "2026.10.3" || before.ServiceBuild != "12" || before.ServerVersion != "" {
		t.Fatalf("unexpected status before probe: %#v", before)
	}

	r.probeConnection(context.Background())
	after, err := r.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Connection.State != ConnectionConnected || after.ServerVersion != "2026.10.31" || after.ServerCommit != "abc1234" {
		t.Fatalf("probe did not record the server build: %#v", after)
	}
}
