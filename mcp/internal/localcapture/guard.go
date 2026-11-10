package localcapture

import (
	"encoding/json"

	"github.com/alexcatdad/scratchpad/mcp/internal/secretguard"
)

// redactCapture replaces credential-shaped values in the free-text parts of a
// capture: the title, the confidence reason and every string in the payload
// and the Git context. The adapter already normalizes the Git remote; the
// context is still scanned in case another client sends a remote URL with
// embedded credentials. Identity fields, scope and timestamps are left alone.
func redactCapture(req *CaptureRequest) ([]Redaction, error) {
	var scan secretguard.Scan
	req.Record.Title = scan.Text(req.Record.Title)
	req.Record.ConfidenceReason = scan.Text(req.Record.ConfidenceReason)
	payload, err := scan.JSON(req.Record.Payload)
	if err != nil {
		return nil, err
	}
	req.Record.Payload = payload
	// A Git context that is not valid JSON is passed through for the server to
	// reject, as before; only the payload is required to be valid at intake.
	if len(req.GitContext) > 0 && json.Valid(req.GitContext) {
		gitContext, err := scan.JSON(req.GitContext)
		if err != nil {
			return nil, err
		}
		req.GitContext = gitContext
	}
	findings := scan.Findings()
	if findings == nil {
		return nil, nil
	}
	redactions := make([]Redaction, 0, len(findings))
	for _, finding := range findings {
		redactions = append(redactions, Redaction{Kind: finding.Kind, Count: finding.Count})
	}
	return redactions, nil
}
