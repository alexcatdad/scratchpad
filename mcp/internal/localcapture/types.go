// Package localcapture implements Scratchpad's durable local capture service
// and its private per-user IPC contract.
package localcapture

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	ProtocolVersion           = 1
	QueueSchemaVersion        = 1
	MaxFrameBytes             = 4 << 20
	FastConfirmWait           = time.Second
	ReceiptRetention          = 30 * 24 * time.Hour
	CapabilityClientSavedAtV1 = "clientSavedAt-v1"
	// PersistentlyStoppedStartupExitCode is the service process exit status
	// when ordinary startup observes the owner's saved Stop state.
	PersistentlyStoppedStartupExitCode = 3
)

// Operation is one bounded request supported by the local service protocol.
type Operation string

const (
	OpCapture           Operation = "capture"
	OpLookupReceipt     Operation = "lookup_receipt"
	OpListReceipts      Operation = "list_receipts"
	OpDiscardReceipt    Operation = "discard_receipt"
	OpSelectProject     Operation = "select_project"
	OpStatus            Operation = "status"
	OpPauseDelivery     Operation = "pause_delivery"
	OpResumeDelivery    Operation = "resume_delivery"
	OpStop              Operation = "stop"
	OpQuiesce           Operation = "quiesce"
	OpResumeAfterUpdate Operation = "resume_after_update"
	OpGetConfiguration  Operation = "get_configuration"
	OpSetConfiguration  Operation = "set_configuration"
	OpVerifyConnection  Operation = "verify_connection"
	OpListProjects      Operation = "list_projects"
)

// Request and Response are the versioned, length-prefixed JSON envelopes.
type Request struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Op      Operation       `json:"op"`
	Body    json.RawMessage `json:"body"`
}

type Response struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *WireError      `json:"error,omitempty"`
}

type ErrorCode string

const (
	ErrInvalidRequest    ErrorCode = "INVALID_REQUEST"
	ErrProtocolVersion   ErrorCode = "PROTOCOL_VERSION"
	ErrUnknownOperation  ErrorCode = "UNKNOWN_OPERATION"
	ErrUnauthorizedPeer  ErrorCode = "UNAUTHORIZED_PEER"
	ErrServiceStopped    ErrorCode = "SERVICE_STOPPED"
	ErrServiceQuiescing  ErrorCode = "SERVICE_QUIESCING"
	ErrKeyUnavailable    ErrorCode = "KEY_UNAVAILABLE"
	ErrKeyUnlockRequired ErrorCode = "KEY_UNLOCK_REQUIRED"
	ErrStorageFailed     ErrorCode = "STORAGE_FAILED"
	ErrReceiptNotFound   ErrorCode = "RECEIPT_NOT_FOUND"
	ErrDiscardNotAllowed ErrorCode = "DISCARD_NOT_ALLOWED"
	ErrProjectSelection  ErrorCode = "PROJECT_SELECTION_REQUIRED"
	ErrConflict          ErrorCode = "CONFLICT"
	ErrInternal          ErrorCode = "INTERNAL"
)

// WireError contains only a stable safe message; it never embeds upstream
// responses, capture content, paths, credentials or implementation details.
type WireError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

var (
	ErrServiceStoppedSentinel    = errors.New("service stopped")
	ErrServiceQuiescingSentinel  = errors.New("service quiescing")
	ErrKeyUnavailableSentinel    = errors.New("queue encryption key unavailable")
	ErrKeyUnlockRequiredSentinel = errors.New("queue encryption key unlock required")
	ErrStorageFailedSentinel     = errors.New("local capture storage failed")
	ErrReceiptNotFoundSentinel   = errors.New("receipt not found")
	ErrDiscardNotAllowedSentinel = errors.New("receipt cannot be discarded")
	ErrProjectSelectionSentinel  = errors.New("explicit project selection required")
	ErrConflictSentinel          = errors.New("capture request identity conflicts with prior content")
	ErrUpgradePendingSentinel    = errors.New("application upgrade is still in progress")
	ErrProtocolVersionSentinel   = errors.New("unsupported local service protocol version")
	ErrUnauthorizedPeerSentinel  = errors.New("local service peer is not authorized")
	ErrUnknownOperationSentinel  = errors.New("unknown local service operation")
	ErrInternalSentinel          = errors.New("local service operation failed")
)

// Binding pins queued delivery to its original server and enrolled SSH key.
// Fingerprints are public-key identities, never key material or session tokens.
type Binding struct {
	ServerURL             string `json:"serverURL"`
	CredentialFingerprint string `json:"credentialFingerprint"`
	PublicKeyPath         string `json:"publicKeyPath,omitempty"`
	SigningKeyPath        string `json:"signingKeyPath,omitempty"`
	SSHAgentSocket        string `json:"sshAgentSocket,omitempty"`
	CredentialID          string `json:"credentialId,omitempty"`
}

// ServiceConfiguration contains only non-secret account routing and public
// key identity. It is stored encrypted because the server URL can identify a
// private installation. Authentication material remains owned by MCP.
type ServiceConfiguration struct {
	ServerURL             string `json:"serverURL"`
	CredentialFingerprint string `json:"credentialFingerprint"`
	PublicKeyPath         string `json:"publicKeyPath"`
	SigningKeyPath        string `json:"signingKeyPath"`
	SSHAgentSocket        string `json:"sshAgentSocket,omitempty"`
}

type SetConfigurationRequest struct {
	Configuration ServiceConfiguration `json:"configuration"`
}

type VerifyConnectionResponse struct {
	ServerURL           string   `json:"serverURL"`
	CredentialID        string   `json:"credentialId"`
	CaptureCapabilities []string `json:"captureCapabilities,omitempty"`
	VerifiedAt          string   `json:"verifiedAt"`
}

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ListProjectsResponse struct {
	Projects []Project `json:"projects"`
}

type Scope struct {
	WorkingDirectory string `json:"workingDirectory,omitempty"`
	ProjectID        string `json:"projectId,omitempty"`
}

type Record struct {
	Type             string          `json:"type"`
	Title            string          `json:"title"`
	Authority        string          `json:"authority"`
	Confidence       string          `json:"confidence"`
	ConfidenceReason string          `json:"confidenceReason,omitempty"`
	HappenedAt       string          `json:"happenedAt,omitempty"`
	Payload          json.RawMessage `json:"payload"`
}

// CaptureRequest is immutable after acceptance. GitContext is the one-time
// per-call snapshot taken by the MCP adapter; the service never infers scope
// from its own working directory.
type CaptureRequest struct {
	RequestID              string          `json:"requestId"`
	Record                 Record          `json:"record"`
	Scope                  Scope           `json:"scope"`
	GitContext             json.RawMessage `json:"gitContext,omitempty"`
	Mirror                 MirrorConsent   `json:"mirror,omitempty"`
	ClientSavedAt          string          `json:"clientSavedAt,omitempty"`
	Binding                Binding         `json:"binding"`
	ClientSavedAtGenerated bool            `json:"-"`
}

// MirrorConsent is the originating MCP client's explicit per-capture choice.
// It is encrypted with the queued request and never inferred from daemon env.
type MirrorConsent struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path,omitempty"`
}

type Outcome string

const (
	OutcomeSavedPending Outcome = "saved_pending"
	OutcomeDelivered    Outcome = "delivered"
)

type ReceiptState string

const (
	ReceiptPending        ReceiptState = "pending"
	ReceiptDelivered      ReceiptState = "delivered"
	ReceiptBlocked        ReceiptState = "blocked"
	ReceiptRejected       ReceiptState = "rejected"
	ReceiptNeedsSelection ReceiptState = "needs_selection"
	ReceiptDiscarded      ReceiptState = "discarded"
)

type DeliveryCode string

const (
	DeliveryConnectivity       DeliveryCode = "connectivity"
	DeliveryAuthentication     DeliveryCode = "authentication"
	DeliveryCredentialChanged  DeliveryCode = "credential_changed"
	DeliveryServerIncompatible DeliveryCode = "server_incompatible"
	DeliveryProjectAmbiguous   DeliveryCode = "project_ambiguous"
	DeliveryServerRejected     DeliveryCode = "server_rejected"
	DeliveryConflict           DeliveryCode = "conflict"
)

type ReceiptContent struct {
	RequestID     string          `json:"requestId"`
	Record        Record          `json:"record"`
	Scope         Scope           `json:"scope"`
	GitContext    json.RawMessage `json:"gitContext,omitempty"`
	ClientSavedAt string          `json:"clientSavedAt"`
}

type Receipt struct {
	ReceiptID        string          `json:"receiptId"`
	State            ReceiptState    `json:"state"`
	AcceptedAt       string          `json:"acceptedAt"`
	ClientSavedAt    string          `json:"clientSavedAt,omitempty"`
	CentralRecordID  string          `json:"centralRecordId,omitempty"`
	CentralProjectID string          `json:"centralProjectId,omitempty"`
	ServerRecordedAt string          `json:"serverRecordedAt,omitempty"`
	DeliveryCode     DeliveryCode    `json:"deliveryCode,omitempty"`
	NextAttemptAt    string          `json:"nextAttemptAt,omitempty"`
	Content          *ReceiptContent `json:"content,omitempty"`
}

type CaptureResponse struct {
	Outcome        Outcome         `json:"outcome"`
	Receipt        Receipt         `json:"receipt"`
	CentralRecord  json.RawMessage `json:"centralRecord,omitempty"`
	CentralProject json.RawMessage `json:"centralProject,omitempty"`
	Mirror         json.RawMessage `json:"mirror,omitempty"`
}

type ListReceiptsRequest struct {
	Limit  int          `json:"limit,omitempty"`
	Cursor string       `json:"cursor,omitempty"`
	State  ReceiptState `json:"state,omitempty"`
}

type ListReceiptsResponse struct {
	Receipts   []Receipt `json:"receipts"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

type ReceiptRequest struct {
	ReceiptID string `json:"receiptId"`
}

type SelectProjectRequest struct {
	ReceiptID string `json:"receiptId"`
	ProjectID string `json:"projectId"`
}

type Lifecycle string

const (
	LifecycleRunning   Lifecycle = "running"
	LifecyclePaused    Lifecycle = "paused"
	LifecycleStopped   Lifecycle = "stopped"
	LifecycleQuiescing Lifecycle = "quiescing"
)

type DeliverySummary struct {
	Code      DeliveryCode `json:"code"`
	At        string       `json:"at"`
	Retryable bool         `json:"retryable"`
}

type ServiceStatus struct {
	Lifecycle          Lifecycle        `json:"lifecycle"`
	Pending            int              `json:"pending"`
	Blocked            int              `json:"blocked"`
	Rejected           int              `json:"rejected"`
	NeedsSelection     int              `json:"needsSelection"`
	KeyAvailable       bool             `json:"keyAvailable"`
	StorageAvailable   bool             `json:"storageAvailable"`
	LastDelivery       *DeliverySummary `json:"lastDelivery,omitempty"`
	Connection         ConnectionStatus `json:"connection"`
	QueueSchemaVersion int              `json:"queueSchemaVersion"`
}

type ConnectionState string

const (
	ConnectionUnknown            ConnectionState = "unknown"
	ConnectionConnected          ConnectionState = "connected"
	ConnectionConnectivity       ConnectionState = "connectivity"
	ConnectionAuthentication     ConnectionState = "authentication"
	ConnectionCredentialChanged  ConnectionState = "credential_changed"
	ConnectionServerIncompatible ConnectionState = "server_incompatible"
)

type ConnectionStatus struct {
	State     ConnectionState `json:"state"`
	CheckedAt string          `json:"checkedAt,omitempty"`
}

type CaptureOutcome struct {
	Outcome        Outcome         `json:"outcome"`
	Receipt        Receipt         `json:"receipt"`
	CentralRecord  json.RawMessage `json:"centralRecord,omitempty"`
	CentralProject json.RawMessage `json:"centralProject,omitempty"`
	Mirror         json.RawMessage `json:"mirror,omitempty"`
}

// KeyProvider separates Keychain-backed access from queue storage. Create is
// permitted only for a genuinely new database; an existing encrypted store
// with no key must fail closed. Create must be idempotent: if the OS key store
// already contains the identifier (for example, after an interrupted first
// launch), it returns that exact key and never replaces it.
type KeyProvider interface {
	Load(context.Context) ([]byte, error)
	Create(context.Context) ([]byte, error)
}

// DeliveryAdapter reuses the MCP's established authentication and HTTP rules.
// Implementations must replay the exact original capture under its original
// server/credential binding and Idempotency-Key.
type DeliveryAdapter interface {
	// Authenticate must perform the existing MCP challenge/proof flow against
	// the configured server and report its stable credential ID.
	Authenticate(context.Context, Binding) (AuthenticatedIdentity, error)
	// VerifyConnection authenticates and performs a harmless authenticated
	// resource read before explicit setup verification succeeds. Periodic health
	// probes use Authenticate only.
	VerifyConnection(context.Context, Binding) (AuthenticatedIdentity, error)
	// ResolveProject and ListProjects use the same authenticated MCP client.
	ResolveProject(context.Context, Binding, string, json.RawMessage) (string, error)
	ListProjects(context.Context, Binding) ([]Project, error)
	Deliver(context.Context, DeliveryItem) (DeliveryResult, error)
}

type AuthenticatedIdentity struct {
	ServerURL           string
	CredentialID        string
	CaptureCapabilities []string
}

type DeliveryItem struct {
	ReceiptID     string
	RequestID     string
	AcceptedAt    string
	ClientSavedAt string
	Record        Record
	Scope         Scope
	GitContext    json.RawMessage
	Binding       Binding
	CredentialID  string
	Mirror        MirrorConsent
}

type VerifyConnectionRequest struct {
	Configuration *ServiceConfiguration `json:"configuration,omitempty"`
}

type ListProjectsRequest struct {
	Binding Binding `json:"binding"`
}

type ConfigurationResponse struct {
	Configuration ServiceConfiguration `json:"configuration"`
}

type DeliveryResult struct {
	CentralRecord    json.RawMessage
	CentralProject   json.RawMessage
	Mirror           json.RawMessage
	RecordID         string
	ProjectID        string
	ServerRecordedAt string
}

// DeliveryFailure carries only the classification needed for queue policy;
// display strings are selected by the service and never copied from upstream.
type DeliveryFailure struct {
	Code       DeliveryCode
	Retryable  bool
	RetryAfter time.Duration
}

func (e *DeliveryFailure) Error() string { return string(e.Code) }

// Service is the public Go API implemented by Server and Client. Stop persists
// a state which only the external supervisor may reverse by restarting service.
type Service interface {
	Capture(context.Context, CaptureRequest) (CaptureOutcome, error)
	LookupReceipt(context.Context, string) (Receipt, error)
	ListReceipts(context.Context, ListReceiptsRequest) (ListReceiptsResponse, error)
	DiscardReceipt(context.Context, string) (Receipt, error)
	SelectProject(context.Context, SelectProjectRequest) (Receipt, error)
	Status(context.Context) (ServiceStatus, error)
	PauseDelivery(context.Context) error
	ResumeDelivery(context.Context) error
	Stop(context.Context) error
	Quiesce(context.Context) error
	ResumeAfterUpdate(context.Context) error
	GetConfiguration(context.Context) (ServiceConfiguration, error)
	SetConfiguration(context.Context, ServiceConfiguration) error
	VerifyConnection(context.Context) (VerifyConnectionResponse, error)
	ListProjects(context.Context) (ListProjectsResponse, error)
}
