package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"time"
	"unicode/utf16"
)

const ragProcessingVersion = "passage-v1"

func ragFingerprint(cfg AIConfig) (string, error) {
	if cfg.EmbeddingDimensions > 16000 {
		return "", &protocolError{"VALIDATION_FAILED", "Passage RAG supports at most 16000 embedding dimensions.", 400}
	}
	return aiHash(map[string]any{"provider": cfg.BaseURL, "embeddingModel": cfg.EmbeddingModel, "dimensions": cfg.EmbeddingDimensions, "processingVersion": ragProcessingVersion})
}
func (a *AI) ragAvailable(ctx context.Context, q entityReader) error {
	if !a.rag || a.auth.Store.Engine != "postgres" {
		return &protocolError{"RAG_UNAVAILABLE", "Enable PostgreSQL passage RAG with application-schema pgvector.", 409}
	}
	var installed bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_extension WHERE extname='vector' AND extnamespace=(SELECT oid FROM pg_catalog.pg_namespace WHERE nspname=current_schema()))`).Scan(&installed); err != nil {
		return err
	}
	if !installed {
		return &protocolError{"RAG_UNAVAILABLE", "Install pgvector in the application schema before indexing passages.", 409}
	}
	return nil
}
func (a *AI) ragSource(ctx context.Context, conn *sql.Conn, scope jobScope, id string) (map[string]any, error) {
	ids, err := allowedJobs(ctx, conn, scope)
	if err != nil {
		return nil, err
	}
	record, err := curationEntity(ctx, conn, "record", id)
	if err != nil {
		return nil, err
	}
	if !contains(ids, stringValue(record, "projectId")) || record["authority"] == "derived" {
		return nil, &protocolError{"AI_NOT_ALLOWED", "The selected source is not eligible for passage RAG.", 403}
	}
	return record, nil
}
func (a *AI) RAGIndex() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if _, err := apiActor(ctx, a.auth.Store.DB, r, a.auth.Origin); err != nil {
			a.auth.fail(w, err)
			return
		}
		var body map[string]any
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		if decoder.Decode(&body) != nil || body == nil || decoder.Decode(new(any)) != io.EOF {
			a.auth.fail(w, aiInvalid())
			return
		}
		scope, err := parseJobScope(body)
		id := stringValue(body, "recordId")
		if err != nil || !portableID(id) {
			a.auth.fail(w, aiInvalid())
			return
		}
		rebuild := false
		if value, exists := body["rebuild"]; exists {
			var ok bool
			rebuild, ok = value.(bool)
			if !ok {
				a.auth.fail(w, aiInvalid())
				return
			}
		}
		var job map[string]any
		err = a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
			actor, err := apiActor(ctx, conn, r, a.auth.Origin)
			if err != nil {
				return err
			}
			if err = a.ragAvailable(ctx, conn); err != nil {
				return err
			}
			if err = a.auth.Store.RAGSchema(ctx, conn); err != nil {
				return err
			}
			record, err := a.ragSource(ctx, conn, scope, id)
			if err != nil {
				return err
			}
			row, err := readSettings(ctx, conn, "ai_settings")
			if err != nil {
				return err
			}
			cfg, err := aiConfig(row)
			if err != nil {
				return err
			}
			fingerprint, err := ragFingerprint(cfg)
			if err != nil {
				return err
			}
			hash, err := aiHash(record)
			if err != nil {
				return err
			}
			identity, err := aiHash([]string{id, hash, fingerprint})
			if err != nil {
				return err
			}
			jobID := "job_rag_" + identity
			old, err := optional(ctx, conn, "ai_job", jobID)
			if err != nil {
				return err
			}
			if old != nil {
				if err := json.Unmarshal(old, &job); err != nil {
					return err
				}
				if !rebuild {
					return nil
				}
				expected, err := expectedVersion(body, r)
				if err != nil {
					return err
				}
				if job["version"] != expected {
					return &versionConflict{job["version"]}
				}
				if !contains([]string{"completed", "failed"}, stringValue(job, "status")) {
					return &protocolError{"CONFLICT", "An active indexing job cannot be rebuilt.", 409}
				}
				previous, err := copyEntity(job)
				if err != nil {
					return err
				}
				job["status"], job["attempts"], job["runAfter"] = "queued", 0, timestamp(time.Now())
				for _, field := range []string{"lastError", "completedAt", "leaseToken", "leaseUntil", "passageCount", "sourceCount"} {
					job[field] = nil
				}
				if err = update(ctx, conn, "ai_job", job); err != nil {
					return err
				}
				return apiAudit(ctx, conn, "ai.job.retried", "ai_job", jobID, actor, previous, job)
			}
			if rebuild {
				return &protocolError{"CONFLICT", "No previous indexing job exists to rebuild.", 409}
			}
			job = map[string]any{"type": "rag_index", "scope": map[string]any{"projectIds": []string{stringValue(record, "projectId")}, "crossProject": scope.CrossProject}, "recordId": id, "contentHash": hash, "fingerprint": fingerprint, "processingVersion": ragProcessingVersion, "status": "queued", "attempts": 0, "runAfter": timestamp(time.Now())}
			if err = insert(ctx, conn, "ai_job", jobID, job); err != nil {
				return err
			}
			return apiAudit(ctx, conn, "ai.job.queued", "ai_job", jobID, actor, nil, job)
		})
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		JSON(w, 202, map[string]any{"job": job})
	}
}

type ragPassage struct {
	ID         string
	Start, End int
	Text       string
	Vector     []float64
}

func ragVector(values []float64) (string, error) {
	scale := 0.0
	for _, v := range values {
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return "", aiUnavailable()
		}
		scale = math.Max(scale, math.Abs(v))
	}
	if scale == 0 {
		return "", aiUnavailable()
	}
	vector := make([]float32, len(values))
	for i, v := range values {
		vector[i] = float32(v / scale)
	}
	raw, err := json.Marshal(vector)
	return string(raw), err
}
func (a *AI) executeRAGIndex(ctx context.Context, job map[string]any, snapshot providerSnapshot) error {
	snapshot.passage = true
	scope, err := aiScope(job)
	if err != nil {
		return err
	}
	var record map[string]any
	err = a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		if _, err := a.workerCurrent(ctx, conn, job, snapshot, false); err != nil {
			return err
		}
		var err error
		record, err = a.ragSource(ctx, conn, scope, stringValue(job, "recordId"))
		return err
	})
	if err != nil {
		return err
	}
	hash, err := aiHash(record)
	if err != nil {
		return err
	}
	if hash != job["contentHash"] {
		return aiChanged()
	}
	units := utf16.Encode([]rune(stringValue(record, "content")))
	passages := []ragPassage{}
	for start := 0; start < len(units) || len(passages) == 0; {
		end := min(start+6000, len(units))
		if end < len(units) && units[end-1] >= 0xd800 && units[end-1] <= 0xdbff {
			end--
		}
		text := string(utf16.Decode(units[start:end]))
		identity, err := aiHash(map[string]any{"recordId": record["id"], "contentHash": hash, "processingVersion": ragProcessingVersion, "start": start, "end": end})
		if err != nil {
			return err
		}
		vectors, err := a.embedInputs(ctx, nil, snapshot, []string{stringValue(record, "title") + "\n" + text}, func(conn *sql.Conn) error { _, err := a.workerCurrent(ctx, conn, job, snapshot, true); return err })
		if err != nil {
			return err
		}
		passages = append(passages, ragPassage{"passage_" + identity, start, end, text, vectors[0]})
		start = end
		if start == len(units) {
			break
		}
	}
	return a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		current, err := a.workerCurrent(ctx, conn, job, snapshot, false)
		if err != nil {
			return err
		}
		source, err := a.ragSource(ctx, conn, scope, stringValue(job, "recordId"))
		if err != nil {
			return err
		}
		fresh, err := aiHash(source)
		if err != nil {
			return err
		}
		if fresh != hash {
			return aiChanged()
		}
		if _, err = conn.ExecContext(ctx, `DELETE FROM rag_passages WHERE record_id=$1 AND fingerprint=$2`, record["id"], job["fingerprint"]); err != nil {
			return err
		}
		for _, passage := range passages {
			vector, err := ragVector(passage.Vector)
			if err != nil {
				return err
			}
			if _, err = conn.ExecContext(ctx, `INSERT INTO rag_passages(id,fingerprint,record_id,project_id,content_hash,processing_version,source_start,source_end,text,dimensions,embedding) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::vector)`, passage.ID, job["fingerprint"], record["id"], record["projectId"], hash, ragProcessingVersion, passage.Start, passage.End, passage.Text, snapshot.config.EmbeddingDimensions, vector); err != nil {
				return err
			}
		}
		current["status"], current["passageCount"], current["sourceCount"], current["completedAt"] = "completed", len(passages), 1, timestamp(time.Now())
		current["leaseToken"], current["leaseUntil"], current["lastError"] = nil, nil, nil
		if err = update(ctx, conn, "ai_job", current); err != nil {
			return err
		}
		return apiAudit(ctx, conn, "ai.job.completed", "ai_job", stringValue(job, "id"), aiWorkerActor, nil, current)
	})
}
