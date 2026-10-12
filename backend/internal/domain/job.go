package domain

// JobScope is the admitted project set persisted with each durable operation.
type JobScope struct {
	ProjectID    string   `json:"projectId,omitempty"`
	ProjectIDs   []string `json:"projectIds,omitempty"`
	CrossProject bool     `json:"crossProject"`
}

// Job is an execution snapshot; original persisted extension fields survive
// lease updates and retries because these snapshots never replace entity JSON.
type Job struct {
	Entity
	Type, Status                     string
	Attempts                         float64
	LeaseToken, LeaseUntil, RunAfter string
	Fingerprint, ProcessingVersion   string
}

func JobFrom(fields map[string]any) Job {
	text := func(key string) string { value, _ := fields[key].(string); return value }
	attempts, _ := fields["attempts"].(float64)
	return Job{Entity: EntityFrom(fields), Type: text("type"), Status: text("status"), Attempts: attempts, LeaseToken: text("leaseToken"), LeaseUntil: text("leaseUntil"), RunAfter: text("runAfter"), Fingerprint: text("fingerprint"), ProcessingVersion: text("processingVersion")}
}
