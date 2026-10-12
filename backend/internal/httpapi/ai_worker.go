package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf16"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

var aiWorkerActor = map[string]any{"kind": "system", "displayName": "Optional AI worker"}

func aiHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	canonical, err := legacyCanonical(raw, collate.New(language.English))
	if err != nil {
		return "", err
	}
	return legacyDigest(canonical), nil
}
func aiFingerprint(cfg AIConfig) (string, error) {
	return aiHash(map[string]any{"baseUrl": cfg.BaseURL, "model": cfg.Model, "embeddingModel": cfg.EmbeddingModel, "dimensions": cfg.EmbeddingDimensions})
}
func aiScope(job map[string]any) (jobScope, error) {
	raw, err := json.Marshal(job["scope"])
	if err != nil {
		return jobScope{}, aiInvalid()
	}
	var scope jobScope
	if json.Unmarshal(raw, &scope) != nil {
		return scope, aiInvalid()
	}
	return scope, nil
}

// Select only this scope's immutable sources. Provider waits never hold a connection.
func (a *AI) aiRecords(ctx context.Context, conn *sql.Conn, scope jobScope) ([]map[string]any, error) {
	return a.aiScopedEntities(ctx, conn, scope, "record")
}
func (a *AI) aiScopedEntities(ctx context.Context, conn *sql.Conn, scope jobScope, kind string) ([]map[string]any, error) {
	ids, err := allowedJobs(ctx, conn, scope)
	if err != nil {
		return nil, err
	}
	placeholders := make([]string, len(ids))
	args := []any{kind}
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+2)
		args = append(args, id)
	}
	project := "json_extract(data,'$.projectId')"
	if a.auth.Store.Engine == "postgres" {
		project = "data->>'projectId'"
	}
	rows, err := conn.QueryContext(ctx, "SELECT data FROM entities WHERE kind=$1 AND "+project+" IN ("+strings.Join(placeholders, ",")+") ORDER BY created_at,id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var record map[string]any
		if err = json.Unmarshal(raw, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}
func (a *AI) claimAIJob(ctx context.Context, snapshot providerSnapshot) (map[string]any, error) {
	var job map[string]any
	err := a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		if err := a.providerCurrent(ctx, conn, nil, snapshot); err != nil {
			return err
		}
		status, runAfter, leaseUntil, typ := "json_extract(data,'$.status')", "json_extract(data,'$.runAfter')", "json_extract(data,'$.leaseUntil')", "json_extract(data,'$.type')"
		if a.auth.Store.Engine == "postgres" {
			status, runAfter, leaseUntil, typ = "data->>'status'", "data->>'runAfter'", "data->>'leaseUntil'", "data->>'type'"
		}
		var raw []byte
		err := conn.QueryRowContext(ctx, "SELECT data FROM entities WHERE kind='ai_job' AND "+typ+" IN ('embed','analyze','export') AND (("+status+"='queued' AND "+runAfter+"<=$1) OR ("+status+"='running' AND "+leaseUntil+"<=$1)) ORDER BY created_at,id LIMIT 1", timestamp(time.Now())).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &job); err != nil {
			return err
		}
		attempts, _ := job["attempts"].(float64)
		if attempts >= 3 {
			job["status"], job["lastError"], job["completedAt"] = "failed", "Retry limit reached after an interrupted worker.", timestamp(time.Now())
			job["leaseToken"], job["leaseUntil"] = nil, nil
			if err = update(ctx, conn, "ai_job", job); err != nil {
				return err
			}
			if err = apiAudit(ctx, conn, "ai.job.failed", "ai_job", stringValue(job, "id"), aiWorkerActor, nil, job); err != nil {
				return err
			}
			job = nil
			return nil
		}
		token, err := entityID("lease")
		if err != nil {
			return err
		}
		job["status"], job["attempts"], job["leaseToken"], job["leaseUntil"], job["startedAt"] = "running", attempts+1, token, timestamp(time.Now().Add(time.Duration(snapshot.config.RequestTimeoutSeconds+30)*time.Second)), timestamp(time.Now())
		return update(ctx, conn, "ai_job", job)
	})
	return job, err
}
func (a *AI) workerCurrent(ctx context.Context, conn *sql.Conn, job map[string]any, snapshot providerSnapshot, renew bool) (map[string]any, error) {
	if err := a.providerCurrent(ctx, conn, nil, snapshot); err != nil {
		return nil, err
	}
	if err := a.jobProvider(ctx, conn); err != nil {
		return nil, err
	}
	current, err := entityMap(ctx, conn, "ai_job", stringValue(job, "id"))
	if err != nil {
		return nil, err
	}
	expiry, err := time.Parse(time.RFC3339Nano, stringValue(current, "leaseUntil"))
	if err != nil || current["status"] != "running" || current["leaseToken"] != job["leaseToken"] || !expiry.After(time.Now()) {
		return nil, &protocolError{"AI_LEASE_EXPIRED", "AI worker lease expired.", 503}
	}
	scope, err := aiScope(current)
	if err != nil {
		return nil, err
	}
	if _, err = allowedJobs(ctx, conn, scope); err != nil {
		return nil, err
	}
	if renew {
		current["leaseUntil"] = timestamp(time.Now().Add(time.Duration(snapshot.config.RequestTimeoutSeconds+30) * time.Second))
		if err = update(ctx, conn, "ai_job", current); err != nil {
			return nil, err
		}
	}
	return current, nil
}
func decodeEmbeddings(data []byte, count, dimensions int) ([][]float64, error) {
	var response struct {
		Data []struct {
			Index     *int       `json:"index"`
			Embedding []*float64 `json:"embedding"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &response) != nil || len(response.Data) != count {
		return nil, aiUnavailable()
	}
	vectors := make([][]float64, count)
	for _, item := range response.Data {
		if item.Index == nil || *item.Index < 0 || *item.Index >= count || vectors[*item.Index] != nil || len(item.Embedding) != dimensions {
			return nil, aiUnavailable()
		}
		vector := make([]float64, dimensions)
		nonzero := false
		for i, v := range item.Embedding {
			if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
				return nil, aiUnavailable()
			}
			vector[i] = *v
			nonzero = nonzero || *v != 0
		}
		if !nonzero {
			return nil, aiUnavailable()
		}
		vectors[*item.Index] = vector
	}
	return vectors, nil
}
func (a *AI) embedInputs(ctx context.Context, r *http.Request, snapshot providerSnapshot, input []string, guard func(*sql.Conn) error) ([][]float64, error) {
	data, err := a.postProviderGuarded(ctx, r, snapshot, "embeddings", map[string]any{"model": snapshot.config.EmbeddingModel, "input": input}, guard)
	if err != nil {
		return nil, err
	}
	return decodeEmbeddings(data, len(input), snapshot.config.EmbeddingDimensions)
}

func (a *AI) executeEmbedding(ctx context.Context, job map[string]any, snapshot providerSnapshot) error {
	scope, err := aiScope(job)
	if err != nil {
		return err
	}
	var records []map[string]any
	err = a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		if _, err := a.workerCurrent(ctx, conn, job, snapshot, false); err != nil {
			return err
		}
		var err error
		records, err = a.aiRecords(ctx, conn, scope)
		return err
	})
	if err != nil {
		return err
	}
	signature, err := aiFingerprint(snapshot.config)
	if err != nil {
		return err
	}
	embeddings := []map[string]any{}
	for _, record := range records {
		units := utf16.Encode([]rune(stringValue(record, "content")))
		chunks := []string{}
		for start := 0; start < len(units); {
			end := min(start+6000, len(units))
			if end < len(units) && units[end-1] >= 0xd800 && units[end-1] <= 0xdbff {
				end--
			}
			chunks = append(chunks, string(utf16.Decode(units[start:end])))
			start = end
		}
		if len(chunks) == 0 {
			chunks = append(chunks, "")
		}
		pieces := len(chunks)
		vector := make([]float64, snapshot.config.EmbeddingDimensions)
		for _, text := range chunks {
			result, err := a.embedInputs(ctx, nil, snapshot, []string{stringValue(record, "title") + "\n" + text}, func(conn *sql.Conn) error { _, err := a.workerCurrent(ctx, conn, job, snapshot, true); return err })
			if err != nil {
				return err
			}
			for dimension, value := range result[0] {
				vector[dimension] += value / float64(pieces)
			}
		}
		nonzero := false
		for _, v := range vector {
			if math.IsInf(v, 0) || math.IsNaN(v) {
				return aiUnavailable()
			}
			nonzero = nonzero || v != 0
		}
		if !nonzero {
			return aiUnavailable()
		}
		hash, err := aiHash(record)
		if err != nil {
			return err
		}
		embeddings = append(embeddings, map[string]any{"recordId": record["id"], "projectId": record["projectId"], "vector": vector, "chunkCount": pieces, "model": snapshot.config.EmbeddingModel, "dimensions": snapshot.config.EmbeddingDimensions, "fingerprint": signature, "contentHash": hash, "generator": map[string]any{"provider": snapshot.config.BaseURL, "model": snapshot.config.EmbeddingModel, "version": signature}, "generatedAt": timestamp(time.Now())})
	}
	return a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		current, err := a.workerCurrent(ctx, conn, job, snapshot, false)
		if err != nil {
			return err
		}
		for _, item := range embeddings {
			source, err := entityMap(ctx, conn, "record", stringValue(item, "recordId"))
			if err != nil {
				return err
			}
			hash, err := aiHash(source)
			if err != nil {
				return err
			}
			if hash != item["contentHash"] {
				return aiChanged()
			}
			id := stringValue(item, "recordId") + "_" + signature
			old, err := optional(ctx, conn, "ai_embedding", id)
			if err != nil {
				return err
			}
			if old == nil {
				err = insert(ctx, conn, "ai_embedding", id, item)
			} else {
				var existing map[string]any
				if err = json.Unmarshal(old, &existing); err != nil {
					return err
				}
				for key, value := range item {
					existing[key] = value
				}
				err = update(ctx, conn, "ai_embedding", existing)
			}
			if err != nil {
				return err
			}
		}
		current["status"], current["artifactIds"], current["sourceCount"], current["warnings"], current["completedAt"] = "completed", []string{}, len(records), []string{}, timestamp(time.Now())
		current["leaseToken"], current["leaseUntil"], current["lastError"] = nil, nil, nil
		if err = update(ctx, conn, "ai_job", current); err != nil {
			return err
		}
		return apiAudit(ctx, conn, "ai.job.completed", "ai_job", stringValue(job, "id"), aiWorkerActor, nil, current)
	})
}
func (a *AI) failAIJob(ctx context.Context, job map[string]any, cause error) error {
	return a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		current, err := entityMap(ctx, conn, "ai_job", stringValue(job, "id"))
		if err != nil {
			return err
		}
		if current["leaseToken"] != job["leaseToken"] || current["status"] != "running" {
			return nil
		}
		attempts, _ := current["attempts"].(float64)
		terminal := attempts >= 3
		message := "AI processing failed."
		var protocol *protocolError
		if errors.As(cause, &protocol) {
			message = protocol.message
			terminal = terminal || contains([]string{"AI_NOT_ALLOWED", "AI_DISABLED", "AI_CONFIGURATION_CHANGED", "AI_SCOPE_TOO_LARGE"}, protocol.code)
		}
		current["status"] = "queued"
		action := "ai.job.retry_scheduled"
		if terminal {
			current["status"] = "failed"
			action = "ai.job.failed"
		}
		current["leaseToken"], current["leaseUntil"], current["lastError"] = nil, nil, message
		current["runAfter"] = timestamp(time.Now().Add(time.Duration(1<<int(attempts)) * time.Second))
		if err = update(ctx, conn, "ai_job", current); err != nil {
			return err
		}
		return apiAudit(ctx, conn, action, "ai_job", stringValue(job, "id"), aiWorkerActor, nil, current)
	})
}

// Each server processes one job at a time; persisted leases coordinate
// competing instances. Scheduling remains a separate parity slice.
func (a *AI) Run(ctx context.Context) {
	tick := func() {
		snapshot, err := a.provider(ctx)
		if err != nil {
			return
		}
		job, err := a.claimAIJob(ctx, snapshot)
		if err != nil || job == nil {
			return
		}
		if job["type"] == "embed" {
			err = a.executeEmbedding(ctx, job, snapshot)
		} else {
			err = a.executeCompletion(ctx, job, snapshot)
		}
		if err != nil && ctx.Err() == nil {
			failureCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_ = a.failAIJob(failureCtx, job, err)
		}
	}
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			tick()
		}
	}
}
