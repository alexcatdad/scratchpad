// Package delivery adapts Scratchpad's existing SSH-authenticated MCP client
// to the durable local capture service without coupling the service to MCP.
package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
	"github.com/alexcatdad/scratchpad/mcp/internal/scratchpad"
)

type Adapter struct {
}

func supportsCaptureTimestamp(values []string) bool {
	for _, value := range values {
		if value == localcapture.CapabilityClientSavedAtV1 {
			return true
		}
	}
	return false
}

func (a *Adapter) authenticatedClient(ctx context.Context, binding localcapture.Binding) (*scratchpad.Client, string, []string, error) {
	client, err := a.client(binding)
	if err != nil {
		return nil, "", nil, err
	}
	credentialID, capabilities, err := client.AuthenticateIdentity(ctx)
	if err != nil {
		return nil, "", nil, classify(err)
	}
	if !supportsCaptureTimestamp(capabilities) {
		return nil, "", nil, localcaptureFailure(localcapture.DeliveryServerIncompatible, false)
	}
	if binding.CredentialID != "" && binding.CredentialID != credentialID {
		return nil, "", nil, localcaptureFailure(localcapture.DeliveryCredentialChanged, false)
	}
	return client, credentialID, capabilities, nil
}

func NewAdapter() *Adapter { return &Adapter{} }

func publicKeyFingerprint(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", localcaptureFailure(localcapture.DeliveryAuthentication, false)
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return "", localcaptureFailure(localcapture.DeliveryAuthentication, false)
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return "", localcaptureFailure(localcapture.DeliveryAuthentication, false)
	}
	digest := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:]), nil
}

func (a *Adapter) client(binding localcapture.Binding) (*scratchpad.Client, error) {
	fingerprint, err := publicKeyFingerprint(binding.PublicKeyPath)
	if err != nil || fingerprint != binding.CredentialFingerprint {
		return nil, localcaptureFailure(localcapture.DeliveryCredentialChanged, false)
	}
	client, err := scratchpad.NewClient(scratchpad.Config{
		URL: binding.ServerURL, PublicKeyPath: binding.PublicKeyPath,
		SigningKeyPath: binding.SigningKeyPath,
	})
	if err != nil {
		return nil, localcaptureFailure(localcapture.DeliveryServerIncompatible, false)
	}
	return client, nil
}

func (a *Adapter) Authenticate(ctx context.Context, binding localcapture.Binding) (localcapture.AuthenticatedIdentity, error) {
	_, credentialID, capabilities, err := a.authenticatedClient(ctx, binding)
	if err != nil {
		return localcapture.AuthenticatedIdentity{}, err
	}
	return localcapture.AuthenticatedIdentity{ServerURL: binding.ServerURL, CredentialID: credentialID, CaptureCapabilities: capabilities}, nil
}

func (a *Adapter) VerifyConnection(ctx context.Context, binding localcapture.Binding) (localcapture.AuthenticatedIdentity, error) {
	client, credentialID, capabilities, err := a.authenticatedClient(ctx, binding)
	if err != nil {
		return localcapture.AuthenticatedIdentity{}, err
	}
	if _, err = client.ListCentralProjects(ctx); err != nil {
		return localcapture.AuthenticatedIdentity{}, classify(err)
	}
	return localcapture.AuthenticatedIdentity{ServerURL: binding.ServerURL, CredentialID: credentialID, CaptureCapabilities: capabilities}, nil
}

func (a *Adapter) ResolveProject(ctx context.Context, binding localcapture.Binding, credentialID string, gitContext json.RawMessage) (string, error) {
	client, current, _, err := a.authenticatedClient(ctx, binding)
	if err != nil {
		return "", err
	}
	if current != credentialID {
		return "", localcaptureFailure(localcapture.DeliveryCredentialChanged, false)
	}
	project, err := client.ResolveGitProject(ctx, gitContext)
	if err != nil {
		return "", classify(err)
	}
	id, _ := project["id"].(string)
	if id == "" {
		return "", localcaptureFailure(localcapture.DeliveryProjectAmbiguous, false)
	}
	return id, nil
}

func (a *Adapter) ListProjects(ctx context.Context, binding localcapture.Binding) ([]localcapture.Project, error) {
	client, _, _, err := a.authenticatedClient(ctx, binding)
	if err != nil {
		return nil, err
	}
	projects, err := client.ListCentralProjects(ctx)
	if err != nil {
		return nil, classify(err)
	}
	result := make([]localcapture.Project, 0, len(projects))
	for _, project := range projects {
		id, _ := project["id"].(string)
		name, _ := project["name"].(string)
		if id != "" {
			result = append(result, localcapture.Project{ID: id, Name: name})
		}
	}
	return result, nil
}

func (a *Adapter) Deliver(ctx context.Context, item localcapture.DeliveryItem) (localcapture.DeliveryResult, error) {
	client, current, _, err := a.authenticatedClient(ctx, item.Binding)
	if err != nil {
		return localcapture.DeliveryResult{}, err
	}
	if current != item.CredentialID {
		return localcapture.DeliveryResult{}, localcaptureFailure(localcapture.DeliveryCredentialChanged, false)
	}
	client.Config.WorkingDirectory = item.Scope.WorkingDirectory
	client.Config.Mirror = item.Mirror.Enabled
	client.Config.MirrorPath = item.Mirror.Path
	var payload any
	decoder := json.NewDecoder(strings.NewReader(string(item.Record.Payload)))
	decoder.UseNumber()
	if err = decoder.Decode(&payload); err != nil {
		return localcapture.DeliveryResult{}, localcaptureFailure(localcapture.DeliveryServerIncompatible, false)
	}
	record := map[string]any{
		"type": item.Record.Type, "title": item.Record.Title,
		"authority": item.Record.Authority, "confidence": item.Record.Confidence,
		"payload": payload,
	}
	if item.Record.ConfidenceReason != "" {
		record["confidenceReason"] = item.Record.ConfidenceReason
	}
	if item.Record.HappenedAt != "" {
		record["happenedAt"] = item.Record.HappenedAt
	}
	result, err := client.CaptureCentral(ctx, item.Scope.ProjectID, record, item.GitContext, item.RequestID, item.ClientSavedAt)
	if err != nil {
		if strings.Contains(err.Error(), "did not preserve clientSavedAt") {
			return localcapture.DeliveryResult{}, localcaptureFailure(localcapture.DeliveryServerIncompatible, false)
		}
		return localcapture.DeliveryResult{}, classify(err)
	}
	recordID, _ := result.Record["id"].(string)
	projectID, _ := result.Project["id"].(string)
	recordedAt, _ := result.Record["recordedAt"].(string)
	centralRecord, _ := json.Marshal(result.Record)
	centralProject, _ := json.Marshal(result.Project)
	mirror, _ := json.Marshal(result.Mirror)
	return localcapture.DeliveryResult{
		RecordID: recordID, ProjectID: projectID, ServerRecordedAt: recordedAt,
		CentralRecord: centralRecord, CentralProject: centralProject, Mirror: mirror,
	}, nil
}

func classify(err error) error {
	var api *scratchpad.APIError
	if !errors.As(err, &api) {
		return localcaptureFailure(localcapture.DeliveryConnectivity, true)
	}
	switch api.Code {
	case "SERVER_INCOMPATIBLE":
		return localcaptureFailure(localcapture.DeliveryServerIncompatible, false)
	case "AUTH_REQUIRED", "AUTH_INVALID", "AUTH_CHALLENGE_EXPIRED", "AUTH_CHALLENGE_USED":
		return localcaptureFailure(localcapture.DeliveryAuthentication, true)
	case "PROJECT_IDENTITY_REQUIRED":
		return localcaptureFailure(localcapture.DeliveryProjectAmbiguous, false)
	case "CONFLICT":
		return localcaptureFailure(localcapture.DeliveryConflict, false)
	case "VALIDATION_FAILED", "VALIDATION_ERROR", "PROJECT_NOT_FOUND", "RECORD_TYPE_DISABLED", "FORBIDDEN":
		return localcaptureFailure(localcapture.DeliveryServerRejected, false)
	default:
		return localcaptureFailure(localcapture.DeliveryConnectivity, true)
	}
}

func localcaptureFailure(code localcapture.DeliveryCode, retryable bool) error {
	return &localcapture.DeliveryFailure{Code: code, Retryable: retryable}
}

var _ localcapture.DeliveryAdapter = (*Adapter)(nil)
