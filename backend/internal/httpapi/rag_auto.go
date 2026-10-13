package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// All source-generation creation shares the caller's existing transaction.
func enqueueRAGRecord(ctx context.Context, conn *sql.Conn, record map[string]any, cfg AIConfig, cross bool, actor map[string]any) (map[string]any, bool, error) {
	fingerprint, err := ragFingerprint(cfg)
	if err != nil {
		return nil, false, err
	}
	// PostgreSQL JSONB normalizes numeric spelling; generation identity follows
	// the stored source rather than the incoming capture representation.
	record, err = ragRecord(ctx, conn, stringValue(record, "id"))
	if err != nil {
		return nil, false, err
	}
	hash, err := aiHash(record)
	if err != nil {
		return nil, false, err
	}
	identity, err := aiHash([]string{stringValue(record, "id"), hash, fingerprint})
	if err != nil {
		return nil, false, err
	}
	id := "job_rag_" + identity
	old, err := optional(ctx, conn, "ai_job", id)
	if err != nil {
		return nil, false, err
	}
	var job map[string]any
	if old != nil {
		err = json.Unmarshal(old, &job)
		return job, false, err
	}
	job = map[string]any{"type": "rag_index", "scope": map[string]any{"projectIds": []string{stringValue(record, "projectId")}, "crossProject": cross}, "recordId": record["id"], "contentHash": hash, "fingerprint": fingerprint, "processingVersion": ragProcessingVersion, "status": "queued", "attempts": 0, "runAfter": timestamp(time.Now())}
	if err = insert(ctx, conn, "ai_job", id, job); err != nil {
		return nil, false, err
	}
	if err = apiAudit(ctx, conn, "ai.job.queued", "ai_job", id, actor, nil, job); err != nil {
		return nil, false, err
	}
	return job, true, nil
}

func (a *AI) queueCapturedRAG(ctx context.Context, conn *sql.Conn, project, record, actor map[string]any) error {
	if !a.rag || a.auth.Store.Engine != "postgres" || objectValue(project, "settings")["aiProcessing"] != true || record["authority"] == "derived" {
		return nil
	}
	row, err := readSettings(ctx, conn, "ai_settings")
	if err != nil {
		return err
	}
	cfg, err := aiConfig(row)
	if err != nil {
		return err
	}
	if !ragDimensionsAvailable(cfg) {
		return nil
	}
	_, _, err = enqueueRAGRecord(ctx, conn, record, cfg, false, actor)
	return err
}

// A bounded anti-join reconciles old/imported history, new consent and embedding
// generations. Persisted source jobs make each batch restart-safe and idempotent.
func (a *AI) backfillRAG(ctx context.Context) error {
	if !a.rag || a.auth.Store.Engine != "postgres" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	row, err := readSettings(ctx, a.auth.Store.DB, "ai_settings")
	if err != nil {
		return err
	}
	cfg, err := aiConfig(row)
	if err != nil {
		return err
	}
	if !ragDimensionsAvailable(cfg) {
		return nil
	}
	fingerprint, err := ragFingerprint(cfg)
	if err != nil {
		return err
	}
	rows, err := a.auth.Store.DB.QueryContext(ctx, `SELECT r.data FROM entities r JOIN entities p ON p.kind='project' AND p.id=r.data->>'projectId' WHERE r.kind='record' AND p.data#>>'{settings,aiProcessing}'='true' AND COALESCE(r.data->>'authority','')<>'derived' AND NOT EXISTS(SELECT 1 FROM entities j WHERE j.kind='ai_job' AND j.data->>'type'='rag_index' AND j.data->>'recordId'=r.id AND j.data->>'fingerprint'=$1) ORDER BY r.created_at,r.id LIMIT 16`, fingerprint)
	if err != nil {
		return err
	}
	records := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		var record map[string]any
		if err = DecodeJSONNumbers(raw, &record); err != nil {
			rows.Close()
			return err
		}
		records = append(records, record)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	return a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		current, err := readSettings(ctx, conn, "ai_settings")
		if err != nil {
			return err
		}
		currentConfig, err := aiConfig(current)
		if err != nil {
			return err
		}
		if !ragDimensionsAvailable(currentConfig) {
			return nil
		}
		currentFingerprint, err := ragFingerprint(currentConfig)
		if err != nil {
			return err
		}
		if currentFingerprint != fingerprint {
			return nil
		}
		for _, record := range records {
			project, err := projectEntity(ctx, conn, stringValue(record, "projectId"))
			if err != nil {
				return err
			}
			if objectValue(project, "settings")["aiProcessing"] != true {
				continue
			}
			if _, _, err = enqueueRAGRecord(ctx, conn, record, currentConfig, false, aiWorkerActor); err != nil {
				return err
			}
		}
		return nil
	})
}
