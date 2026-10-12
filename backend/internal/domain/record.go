package domain

import "encoding/json"

// Record is the validated capture being assembled by the central application.
// Payload and context remain flexible; imported immutable JSON is never rebuilt
// through this type. A context pointer preserves an explicitly supplied {}.
type Record struct {
	ID               string          `json:"id,omitempty"`
	Version          float64         `json:"version,omitempty"`
	CreatedAt        string          `json:"createdAt,omitempty"`
	ProjectID        string          `json:"projectId,omitempty"`
	Type             string          `json:"type"`
	Title            string          `json:"title"`
	Authority        string          `json:"authority"`
	Confidence       string          `json:"confidence"`
	ConfidenceReason string          `json:"confidenceReason,omitempty"`
	HappenedAt       string          `json:"happenedAt,omitempty"`
	Payload          map[string]any  `json:"payload"`
	Actor            map[string]any  `json:"actor,omitempty"`
	GitContext       *map[string]any `json:"gitContext,omitempty"`
	Content          string          `json:"content,omitempty"`
	PayloadVersion   int             `json:"payloadVersion,omitempty"`
	RecordedAt       string          `json:"recordedAt,omitempty"`
}

// Fields adapts a newly assembled capture to the existing persistence writer,
// which supplies its identifier, creation instant and initial entity version.
func (record *Record) Fields() (map[string]any, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	err = json.Unmarshal(data, &fields)
	return fields, err
}
