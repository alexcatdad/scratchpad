package localcapture

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The sample credential is assembled at run time so that no credential-shaped
// literal is committed to the repository.
var syntheticToken = "ghp_" + strings.Repeat("a1B2", 9)

func redactionRequest(id string) CaptureRequest {
	request := testRequest(id, "Deployed with "+syntheticToken)
	request.Record.ConfidenceReason = "export DEPLOY_PASSWORD=Hunter2hunter2 worked"
	request.Record.Payload = json.RawMessage(`{"finding":"token ` + syntheticToken + ` was pasted","exact":1.2500,"apiKey":"k3y-Value-99"}`)
	request.GitContext = json.RawMessage(`{"remote":"https://deploy:Hunter2pw@git.invalid/owner/repo","commit":"7817b82ccdbd237f960de07a8d4e13d61fd96a6a"}`)
	return request
}

func TestCaptureRedactsCredentialsBeforeStorageAndDelivery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "captures.sqlite")
	delivered := make(chan DeliveryItem, 1)
	adapter := &testAdapter{identity: testIdentity(), deliver: func(_ context.Context, item DeliveryItem) (DeliveryResult, error) {
		delivered <- item
		return successResult(), nil
	}}
	r, store := testRuntime(t, path, &testKeys{}, adapter)
	defer r.Close()
	if err := r.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	accepted, err := r.Capture(ctx, redactionRequest("redact-1"))
	if err != nil {
		t.Fatal(err)
	}

	want := []Redaction{{Kind: "assigned-secret", Count: 2}, {Kind: "github-token", Count: 2}, {Kind: "url-credential", Count: 1}}
	if len(accepted.Receipt.Redactions) != len(want) {
		t.Fatalf("redactions = %#v, want %#v", accepted.Receipt.Redactions, want)
	}
	for i := range want {
		if accepted.Receipt.Redactions[i] != want[i] {
			t.Fatalf("redactions = %#v, want %#v", accepted.Receipt.Redactions, want)
		}
	}

	content := accepted.Receipt.Content
	if content == nil {
		t.Fatal("pending receipt has no content")
	}
	if content.Record.Title != "Deployed with secret://redacted/github-token" {
		t.Fatalf("title = %q", content.Record.Title)
	}
	if content.Record.ConfidenceReason != "export DEPLOY_PASSWORD=secret://redacted/assigned-secret worked" {
		t.Fatalf("confidence reason = %q", content.Record.ConfidenceReason)
	}
	wantPayload := `{"finding":"token secret://redacted/github-token was pasted","exact":1.2500,"apiKey":"secret://redacted/assigned-secret"}`
	if string(content.Record.Payload) != wantPayload {
		t.Fatalf("payload = %s", content.Record.Payload)
	}
	wantGit := `{"remote":"https://deploy:secret://redacted/url-credential@git.invalid/owner/repo","commit":"7817b82ccdbd237f960de07a8d4e13d61fd96a6a"}`
	if string(content.GitContext) != wantGit {
		t.Fatalf("git context = %s", content.GitContext)
	}

	_, env, err := store.findByReceipt(accepted.Receipt.ReceiptID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{syntheticToken, "Hunter2hunter2", "Hunter2pw", "k3y-Value-99"} {
		if strings.Contains(string(stored), secret) {
			t.Fatalf("stored envelope still contains %q", secret)
		}
	}

	// A retry carries the caller's original, unredacted input. It must match
	// the stored capture and must not be reported as changed content.
	repeated, err := r.Capture(ctx, redactionRequest("redact-1"))
	if err != nil {
		t.Fatalf("retry of the original input failed: %v", err)
	}
	if repeated.Receipt.ReceiptID != accepted.Receipt.ReceiptID {
		t.Fatalf("retry changed receipt: %q != %q", repeated.Receipt.ReceiptID, accepted.Receipt.ReceiptID)
	}

	if err = r.ResumeDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case item := <-delivered:
		sent, marshalErr := json.Marshal(struct {
			Record     Record
			GitContext json.RawMessage
		}{item.Record, item.GitContext})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		for _, secret := range []string{syntheticToken, "Hunter2hunter2", "Hunter2pw", "k3y-Value-99"} {
			if strings.Contains(string(sent), secret) {
				t.Fatalf("delivery still contains %q", secret)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("capture was not delivered")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		receipt, lookupErr := r.LookupReceipt(context.Background(), accepted.Receipt.ReceiptID)
		if lookupErr == nil && receipt.State == ReceiptDelivered {
			if len(receipt.Redactions) != len(want) {
				t.Fatalf("delivered receipt lost its redaction report: %#v", receipt.Redactions)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("receipt did not reach the delivered state")
}

func TestCaptureWithoutCredentialsIsUnchangedAndReportsNoRedaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "captures.sqlite")
	r, _ := testRuntime(t, path, &testKeys{}, &testAdapter{identity: testIdentity()})
	defer r.Close()
	if err := r.PauseDelivery(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	request := testRequest("plain-1", "Keep PostgreSQL on the homelab")
	request.Record.Payload = json.RawMessage(`{ "decision" : "commit 7817b82ccdbd237f960de07a8d4e13d61fd96a6a", "n": 1.0 }`)
	accepted, err := r.Capture(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Receipt.Redactions != nil {
		t.Fatalf("unexpected redactions: %#v", accepted.Receipt.Redactions)
	}
	if got := string(accepted.Receipt.Content.Record.Payload); got != string(request.Record.Payload) {
		t.Fatalf("payload changed: %s", got)
	}
	encoded, err := json.Marshal(accepted.Receipt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "redactions") {
		t.Fatalf("receipt without redactions carries the field: %s", encoded)
	}
}
