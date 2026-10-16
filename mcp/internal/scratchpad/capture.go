package scratchpad

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Alternative struct {
	Name           string `json:"name"`
	ReasonRejected string `json:"reasonRejected,omitempty"`
}
type DecisionPayload struct {
	Decision     string   `json:"decision"`
	Rationale    string   `json:"rationale,omitempty"`
	Alternatives []string `json:"alternatives,omitempty"`
	Consequences []string `json:"consequences,omitempty"`
}
type DecisionInput struct {
	Common
	DecisionPayload
}
type ADRPayload struct {
	Context      string        `json:"context,omitempty"`
	Decision     string        `json:"decision"`
	Rationale    string        `json:"rationale,omitempty"`
	Alternatives []Alternative `json:"alternatives,omitempty"`
	Consequences []string      `json:"consequences,omitempty"`
}
type ADRInput struct {
	Common
	ADRPayload
}
type BusinessDecisionPayload struct {
	Decision        string `json:"decision"`
	Rationale       string `json:"rationale,omitempty"`
	RequestedBy     string `json:"requestedBy,omitempty"`
	BusinessContext string `json:"businessContext,omitempty"`
	ExpectedOutcome string `json:"expectedOutcome,omitempty"`
}
type BusinessDecisionInput struct {
	Common
	BusinessDecisionPayload
}
type FindingPayload struct {
	Finding     string   `json:"finding"`
	Environment string   `json:"environment,omitempty"`
	Limitations []string `json:"limitations,omitempty"`
}
type FindingInput struct {
	Common
	FindingPayload
}
type QAPayload struct {
	Question    string   `json:"question"`
	Answer      *string  `json:"answer,omitempty" jsonschema:"Optional answer; omit it to preserve an unresolved question, then capture and link an answer separately"`
	Limitations []string `json:"limitations,omitempty"`
}
type QAInput struct {
	Common
	QAPayload
}
type FailurePayload struct {
	Expected   string `json:"expected,omitempty"`
	Observed   string `json:"observed"`
	Cause      string `json:"cause,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	Lesson     string `json:"lesson,omitempty"`
}
type FailureInput struct {
	Common
	FailurePayload
}
type ConstraintPayload struct {
	Constraint string `json:"constraint"`
	Reason     string `json:"reason,omitempty"`
	Scope      string `json:"scope,omitempty"`
}
type ConstraintInput struct {
	Common
	ConstraintPayload
}
type ProjectStatePayload struct {
	State         string `json:"state"`
	Reason        string `json:"reason,omitempty"`
	PreviousState string `json:"previousState,omitempty"`
	FollowUp      string `json:"followUp,omitempty"`
}
type ProjectStateInput struct {
	Common
	ProjectStatePayload
}

const captureResultGuidance = "; outcome and receipt report durable intake/delivery. After service restart, record and project may be omitted while the receipt retains central IDs; use get_record for the full central record"

func registerCaptures(s *mcp.Server, c *Client) {
	addCaptureTool(s, &mcp.Tool{Name: "record_decision", Description: "Capture an immutable decision with provenance; reuse requestId on retries" + captureResultGuidance}, func(ctx context.Context, _ *mcp.CallToolRequest, in DecisionInput) (*mcp.CallToolResult, CaptureResult, error) {
		return c.capture(ctx, "decision", in.Common, in.DecisionPayload)
	})
	addCaptureTool(s, &mcp.Tool{Name: "record_adr", Description: "Capture an immutable adr with provenance; reuse requestId on retries" + captureResultGuidance}, func(ctx context.Context, _ *mcp.CallToolRequest, in ADRInput) (*mcp.CallToolResult, CaptureResult, error) {
		return c.capture(ctx, "adr", in.Common, in.ADRPayload)
	})
	addCaptureTool(s, &mcp.Tool{Name: "record_business_decision", Description: "Capture an immutable business_decision with provenance; reuse requestId on retries" + captureResultGuidance}, func(ctx context.Context, _ *mcp.CallToolRequest, in BusinessDecisionInput) (*mcp.CallToolResult, CaptureResult, error) {
		return c.capture(ctx, "business_decision", in.Common, in.BusinessDecisionPayload)
	})
	addCaptureTool(s, &mcp.Tool{Name: "record_finding", Description: "Capture an immutable finding with provenance; reuse requestId on retries" + captureResultGuidance}, func(ctx context.Context, _ *mcp.CallToolRequest, in FindingInput) (*mcp.CallToolResult, CaptureResult, error) {
		return c.capture(ctx, "finding", in.Common, in.FindingPayload)
	})
	addCaptureTool(s, &mcp.Tool{Name: "record_qa", Description: "Capture an immutable qa with provenance; reuse requestId on retries" + captureResultGuidance}, func(ctx context.Context, request *mcp.CallToolRequest, in QAInput) (*mcp.CallToolResult, CaptureResult, error) {
		if in.Answer == nil && nullArgument(request, "answer") {
			return nil, CaptureResult{}, &APIError{Code: "VALIDATION_FAILED", Message: "A supplied answer must be a nonempty string."}
		}
		return c.capture(ctx, "qa", in.Common, in.QAPayload)
	})
	addCaptureTool(s, &mcp.Tool{Name: "record_failure", Description: "Capture an immutable failure with provenance; reuse requestId on retries" + captureResultGuidance}, func(ctx context.Context, _ *mcp.CallToolRequest, in FailureInput) (*mcp.CallToolResult, CaptureResult, error) {
		return c.capture(ctx, "failure", in.Common, in.FailurePayload)
	})
	addCaptureTool(s, &mcp.Tool{Name: "record_constraint", Description: "Capture an immutable constraint with provenance; reuse requestId on retries" + captureResultGuidance}, func(ctx context.Context, _ *mcp.CallToolRequest, in ConstraintInput) (*mcp.CallToolResult, CaptureResult, error) {
		return c.capture(ctx, "constraint", in.Common, in.ConstraintPayload)
	})
	addCaptureTool(s, &mcp.Tool{Name: "record_project_state", Description: "Capture an immutable project_state with provenance; reuse requestId on retries" + captureResultGuidance}, func(ctx context.Context, _ *mcp.CallToolRequest, in ProjectStateInput) (*mcp.CallToolResult, CaptureResult, error) {
		return c.capture(ctx, "project_state", in.Common, in.ProjectStatePayload)
	})
}
