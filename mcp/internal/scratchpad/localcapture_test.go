package scratchpad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
)

// fixtureCaptureService keeps legacy MCP contract tests focused on the HTTP
// adapter while still exercising the MCP-to-local-service boundary. End-to-end
// service durability and IPC are covered in mcp/integration.
type fixtureCaptureService struct {
	localcapture.Service
	client   *Client
	receipts map[string]localcapture.Receipt
}

func attachFixtureCaptureService(c *Client) {
	c.CaptureService = &fixtureCaptureService{client: c, receipts: map[string]localcapture.Receipt{}}
}

func (s *fixtureCaptureService) GetConfiguration(context.Context) (localcapture.ServiceConfiguration, error) {
	return localcapture.ServiceConfiguration{
		ServerURL: s.client.Config.URL, CredentialFingerprint: "SHA256:synthetic",
		PublicKeyPath: s.client.Config.PublicKeyPath, SigningKeyPath: s.client.Config.SigningKeyPath,
	}, nil
}

func (s *fixtureCaptureService) Capture(ctx context.Context, request localcapture.CaptureRequest) (localcapture.CaptureOutcome, error) {
	if request.ClientSavedAt == "" {
		request.ClientSavedAt = "2026-10-15T00:00:00Z"
	}
	projectID := request.Scope.ProjectID
	if projectID == "" {
		var git GitContext
		if len(request.GitContext) > 0 {
			if err := json.Unmarshal(request.GitContext, &git); err != nil {
				return localcapture.CaptureOutcome{}, err
			}
		}
		if git.RepositoryIdentity == "" {
			if request.GitContext == nil {
				return localcapture.CaptureOutcome{}, &APIError{Code: "PROJECT_IDENTITY_REQUIRED", Message: "explicit project required"}
			}
			return localcapture.CaptureOutcome{}, &APIError{Code: "PROJECT_IDENTITY_AMBIGUOUS", Message: "explicit project required"}
		}
		project, err := s.client.ResolveGitProject(ctx, request.GitContext)
		if err != nil {
			return localcapture.CaptureOutcome{}, fmt.Errorf("fixture project resolution (explicit scope %q): %w", request.Scope.ProjectID, err)
		}
		projectID, _ = project["id"].(string)
	}
	var payload any
	if err := json.Unmarshal(request.Record.Payload, &payload); err != nil {
		return localcapture.CaptureOutcome{}, err
	}
	record := map[string]any{
		"type": request.Record.Type, "title": request.Record.Title,
		"authority": request.Record.Authority, "confidence": request.Record.Confidence,
		"payload": payload,
	}
	if request.Record.ConfidenceReason != "" {
		record["confidenceReason"] = request.Record.ConfidenceReason
	}
	if request.Record.HappenedAt != "" {
		record["happenedAt"] = request.Record.HappenedAt
	}
	result, err := s.client.CaptureCentral(ctx, projectID, record, request.GitContext, request.RequestID, request.ClientSavedAt)
	if err != nil {
		return localcapture.CaptureOutcome{}, err
	}
	receipt := localcapture.Receipt{ReceiptID: "receipt-" + request.RequestID, State: localcapture.ReceiptDelivered, AcceptedAt: request.ClientSavedAt, ClientSavedAt: request.ClientSavedAt, CentralRecordID: stringField(result.Record, "id"), CentralProjectID: projectID}
	s.receipts[receipt.ReceiptID] = receipt
	centralRecord, _ := json.Marshal(result.Record)
	centralProject, _ := json.Marshal(result.Project)
	mirror, _ := json.Marshal(result.Mirror)
	return localcapture.CaptureOutcome{Outcome: localcapture.OutcomeDelivered, Receipt: receipt, CentralRecord: centralRecord, CentralProject: centralProject, Mirror: mirror}, nil
}

func (s *fixtureCaptureService) LookupReceipt(_ context.Context, id string) (localcapture.Receipt, error) {
	receipt, ok := s.receipts[id]
	if !ok {
		return localcapture.Receipt{}, localcapture.ErrReceiptNotFoundSentinel
	}
	return receipt, nil
}

func stringField(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return text
}

var _ localcapture.Service = (*fixtureCaptureService)(nil)

func TestCaptureFailsClosedWhenLocalServiceUnavailable(t *testing.T) {
	posts := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/records" {
			posts++
		}
		http.Error(w, "unexpected direct fallback", http.StatusInternalServerError)
	}))
	defer api.Close()
	client, err := NewClient(Config{URL: api.URL, WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	client.CaptureService = localcapture.NewClient(filepath.Join(t.TempDir(), "missing.sock"))
	_, _, err = client.capture(context.Background(), "decision", Common{
		Scope: Scope{ProjectID: "project-synthetic"}, RequestID: "offline-no-service",
		Title: "Synthetic offline capture", Authority: "observed", Confidence: "high",
	}, DecisionPayload{Decision: "Remain durable"})
	if err == nil {
		t.Fatal("capture succeeded without the local service")
	}
	if posts != 0 {
		t.Fatalf("capture fell back to direct HTTP and posted %d records", posts)
	}
}

type conflictCaptureService struct {
	localcapture.Service
	configuration localcapture.ServiceConfiguration
}

func (s conflictCaptureService) GetConfiguration(context.Context) (localcapture.ServiceConfiguration, error) {
	return s.configuration, nil
}

func (conflictCaptureService) Capture(context.Context, localcapture.CaptureRequest) (localcapture.CaptureOutcome, error) {
	return localcapture.CaptureOutcome{}, localcapture.ErrConflictSentinel
}

func TestCapturePreservesConflictCodeFromDurableService(t *testing.T) {
	directory := t.TempDir()
	client, err := NewClient(Config{URL: "http://localhost:3124", WorkingDirectory: directory})
	if err != nil {
		t.Fatal(err)
	}
	configuration := localcapture.ServiceConfiguration{
		ServerURL: client.Config.URL, CredentialFingerprint: "SHA256:synthetic",
	}
	client.CaptureService = conflictCaptureService{configuration: configuration}
	_, _, err = client.capture(context.Background(), "finding", Common{
		Scope: Scope{ProjectID: "proj_conflict"}, RequestID: "stable-request",
		Title: "Synthetic changed retry", Authority: "observed", Confidence: "high",
	}, FindingPayload{Finding: "Changed content must conflict."})
	if err == nil || !strings.Contains(err.Error(), "CONFLICT") || !errors.Is(err, localcapture.ErrConflictSentinel) {
		t.Fatalf("expected an established conflict code while preserving its safe cause, got %v", err)
	}
}

type deliveredReceiptOnlyService struct {
	localcapture.Service
	configuration localcapture.ServiceConfiguration
}

func (s deliveredReceiptOnlyService) GetConfiguration(context.Context) (localcapture.ServiceConfiguration, error) {
	return s.configuration, nil
}

func (deliveredReceiptOnlyService) Capture(_ context.Context, request localcapture.CaptureRequest) (localcapture.CaptureOutcome, error) {
	return localcapture.CaptureOutcome{
		Outcome: localcapture.OutcomeDelivered,
		Receipt: localcapture.Receipt{
			ReceiptID: "receipt-after-restart", State: localcapture.ReceiptDelivered,
			CentralRecordID: "rec_after_restart", CentralProjectID: "proj_after_restart",
		},
	}, nil
}

func TestDeliveredReceiptAfterRestartDoesNotInventFullServerObjects(t *testing.T) {
	directory := t.TempDir()
	client, err := NewClient(Config{
		URL: "http://localhost:3123", WorkingDirectory: directory,
		PublicKeyPath: filepath.Join(directory, "synthetic.pub"), SigningKeyPath: filepath.Join(directory, "synthetic.key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration := localcapture.ServiceConfiguration{
		ServerURL: client.Config.URL, CredentialFingerprint: "SHA256:synthetic",
		PublicKeyPath: client.Config.PublicKeyPath, SigningKeyPath: client.Config.SigningKeyPath,
	}
	client.CaptureService = deliveredReceiptOnlyService{configuration: configuration}
	_, output, err := client.capture(context.Background(), "finding", Common{
		Scope: Scope{WorkingDirectory: directory, ProjectID: "proj_after_restart"}, RequestID: "same-delivered-request",
		Title: "Synthetic post-restart replay", Authority: "observed", Confidence: "high",
	}, FindingPayload{Finding: "Durable receipt survives service restart."})
	if err != nil {
		t.Fatal(err)
	}
	if output.Outcome != string(localcapture.OutcomeDelivered) || output.CentralRecordID != "rec_after_restart" || output.CentralProjectID != "proj_after_restart" || output.Record != nil || output.Project != nil {
		t.Fatalf("delivered receipt should retain central references without inventing full server objects: %+v", output)
	}
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	var structured any
	if err := json.Unmarshal(raw, &structured); err != nil {
		t.Fatal(err)
	}
	if err := captureResolved.Validate(structured); err != nil {
		t.Fatalf("receipt-only delivered output violates the MCP schema: %v", err)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	if len(values["record"]) != 0 || len(values["project"]) != 0 || len(values["centralRecordId"]) == 0 || len(values["centralProjectId"]) == 0 {
		t.Fatalf("receipt-only response has fabricated or missing identities: %s", raw)
	}
}

// This boundary fixture records whether mismatched credentials reach intake.
type signingReferenceService struct {
	localcapture.Service
	configuration localcapture.ServiceConfiguration
	outcome       localcapture.CaptureOutcome
	calls         int
}

func (s *signingReferenceService) GetConfiguration(context.Context) (localcapture.ServiceConfiguration, error) {
	return s.configuration, nil
}
func (s *signingReferenceService) Capture(context.Context, localcapture.CaptureRequest) (localcapture.CaptureOutcome, error) {
	s.calls++
	return s.outcome, nil
}

func TestCaptureSigningKeyFallback(t *testing.T) {
	for _, tc := range []struct {
		name, signing, servicePublic, serviceSigning string
		pending, reject                              bool
	}{
		{name: "omitted online", servicePublic: "owner.pub", serviceSigning: "owner.pub"},
		{name: "omitted offline", servicePublic: "owner.pub", serviceSigning: "owner.pub", pending: true},
		{name: "explicit signing reference", signing: "owner.key", servicePublic: "owner.pub", serviceSigning: "owner.key"},
		{name: "different signing reference", signing: "other.key", servicePublic: "owner.pub", serviceSigning: "owner.pub", reject: true},
		{name: "different public reference", servicePublic: "other.pub", serviceSigning: "owner.pub", reject: true},
		{name: "omitted does not match different signing reference", servicePublic: "owner.pub", serviceSigning: "other.key", reject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			signing := ""
			if tc.signing != "" {
				signing = filepath.Join(dir, tc.signing)
			}
			c, err := NewClient(Config{URL: "http://localhost:3123", WorkingDirectory: dir, PublicKeyPath: filepath.Join(dir, "owner.pub"), SigningKeyPath: signing})
			if err != nil {
				t.Fatal(err)
			}
			outcome := localcapture.CaptureOutcome{Outcome: localcapture.OutcomeDelivered, Receipt: localcapture.Receipt{ReceiptID: "receipt-synthetic", State: localcapture.ReceiptDelivered, CentralRecordID: "rec_synthetic", CentralProjectID: "proj_synthetic"}}
			if tc.pending {
				outcome.Outcome = localcapture.OutcomeSavedPending
				outcome.Receipt.State = localcapture.ReceiptPending
				outcome.Receipt.CentralRecordID = ""
				outcome.Receipt.CentralProjectID = ""
			}
			s := &signingReferenceService{configuration: localcapture.ServiceConfiguration{ServerURL: c.Config.URL, CredentialFingerprint: "SHA256:synthetic", PublicKeyPath: filepath.Join(dir, tc.servicePublic), SigningKeyPath: filepath.Join(dir, tc.serviceSigning)}, outcome: outcome}
			c.CaptureService = s
			_, got, err := c.capture(context.Background(), "finding", Common{Scope: Scope{ProjectID: "proj_synthetic"}, RequestID: "synthetic-signing-fallback", Title: "Synthetic signing fallback", Authority: "observed", Confidence: "high"}, FindingPayload{Finding: "Synthetic boundary regression"})
			if tc.reject {
				if err == nil || !strings.Contains(err.Error(), "key references") || s.calls != 0 {
					t.Fatalf("mismatch must fail before intake: calls=%d err=%v", s.calls, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.calls != 1 || got.Outcome != string(outcome.Outcome) || got.Receipt["receiptId"] != "receipt-synthetic" || got.CentralRecordID != outcome.Receipt.CentralRecordID {
				t.Fatalf("receipt outcome changed: %+v", got)
			}
		})
	}
}
