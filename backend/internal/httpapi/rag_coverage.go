package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf16"
)

type ragCoverage struct {
	Status       string `json:"status"`
	Fingerprint  string `json:"fingerprint"`
	Eligible     int    `json:"eligibleSources"`
	Indexed      int    `json:"indexedSources"`
	Queued       int    `json:"queuedSources"`
	Running      int    `json:"runningSources"`
	Failed       int    `json:"failedSources"`
	Unindexed    int    `json:"unindexedSources"`
	Incompatible int    `json:"incompatibleSources"`
	Passages     int    `json:"passageCount"`
}

func (a *AI) RAGCoverage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if _, err := apiActor(ctx, a.auth.Store.DB, r, a.auth.Origin); err != nil {
			a.auth.fail(w, err)
			return
		}
		if r.URL.Query().Has("projectId") && r.URL.Query().Has("projectIds") {
			a.auth.fail(w, aiInvalid())
			return
		}
		if err := a.prepareRAG(ctx); err != nil {
			a.auth.fail(w, err)
			return
		}
		tx, err := a.auth.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		defer tx.Rollback()
		if _, err = apiActor(ctx, tx, r, a.auth.Origin); err != nil {
			a.auth.fail(w, err)
			return
		}
		if err = a.ragAvailable(ctx, tx); err != nil {
			a.auth.fail(w, err)
			return
		}
		params := r.URL.Query()
		scope := jobScope{ProjectID: params.Get("projectId")}
		if values := params["projectIds"]; len(values) > 0 {
			scope.ProjectIDs = values
		}
		if value, exists := params["crossProject"]; exists {
			if len(value) != 1 || !contains([]string{"true", "false"}, value[0]) {
				a.auth.fail(w, aiInvalid())
				return
			}
			scope.CrossProject = value[0] == "true"
		}
		if len(params["projectId"]) > 1 || len(scope.ProjectIDs) > 100 {
			a.auth.fail(w, aiInvalid())
			return
		}
		coverage, err := a.ragCoverage(ctx, tx, scope)
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		JSON(w, 200, coverage)
	}
}
func (a *AI) ragCoverage(ctx context.Context, q scopeReader, scope jobScope) (ragCoverage, error) {
	result := ragCoverage{Status: "complete"}
	ids, err := allowedJobs(ctx, q, scope)
	if err != nil {
		return result, err
	}
	row, err := readSettings(ctx, q, "ai_settings")
	if err != nil {
		return result, err
	}
	cfg, err := aiConfig(row)
	if err != nil {
		return result, err
	}
	result.Fingerprint, err = ragFingerprint(cfg)
	if err != nil {
		return result, err
	}
	args := []any{}
	places := make([]string, len(ids))
	for i, id := range ids {
		places[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, id)
	}
	statement := `SELECT r.data,COALESCE((SELECT jsonb_agg(j.data) FROM entities j WHERE j.kind='ai_job' AND j.data->>'type'='rag_index' AND j.data->>'recordId'=r.id),'[]'::jsonb),COALESCE((SELECT jsonb_agg(jsonb_build_object('fingerprint',p.fingerprint,'contentHash',p.content_hash,'processingVersion',p.processing_version,'dimensions',p.dimensions,'id',p.id,'start',p.source_start,'end',p.source_end,'text',p.text,'vectorUsable',vector_dims(p.embedding)=p.dimensions AND vector_norm(p.embedding)>0)) FROM rag_passages p WHERE p.record_id=r.id AND p.project_id=r.data->>'projectId'),'[]'::jsonb) FROM entities r WHERE r.kind='record' AND r.data->>'projectId' IN (` + strings.Join(places, ",") + `) AND COALESCE(r.data->>'authority','')<>'derived'`
	rows, err := q.QueryContext(ctx, statement, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw, jobsRaw, passagesRaw []byte
		if err = rows.Scan(&raw, &jobsRaw, &passagesRaw); err != nil {
			return result, err
		}
		var record map[string]any
		var jobs, passages []map[string]any
		if json.Unmarshal(raw, &record) != nil || json.Unmarshal(jobsRaw, &jobs) != nil || json.Unmarshal(passagesRaw, &passages) != nil {
			return result, aiUnavailable()
		}
		hash, err := aiHash(record)
		if err != nil {
			return result, err
		}
		result.Eligible++
		var current map[string]any
		incompatible := false
		for _, job := range jobs {
			if job["fingerprint"] == result.Fingerprint && job["contentHash"] == hash && job["processingVersion"] == ragProcessingVersion {
				current = job
			} else {
				incompatible = true
			}
		}
		// A completed count alone cannot establish faithful source coverage.
		units := utf16.Encode([]rune(stringValue(record, "content")))
		expected := map[string]struct {
			start, end int
			text       string
		}{}
		for start := 0; start < len(units) || len(expected) == 0; {
			end := min(start+6000, len(units))
			if end < len(units) && units[end-1] >= 0xd800 && units[end-1] <= 0xdbff {
				end--
			}
			identity, err := aiHash(map[string]any{"recordId": record["id"], "contentHash": hash, "processingVersion": ragProcessingVersion, "start": start, "end": end})
			if err != nil {
				return result, err
			}
			expected["passage_"+identity] = struct {
				start, end int
				text       string
			}{start, end, string(utf16.Decode(units[start:end]))}
			start = end
			if start == len(units) {
				break
			}
		}
		compatiblePassages := 0
		generationPassages := 0
		for _, passage := range passages {
			if passage["fingerprint"] == result.Fingerprint {
				generationPassages++
			}
			if passage["fingerprint"] == result.Fingerprint && passage["contentHash"] == hash && passage["processingVersion"] == ragProcessingVersion && passage["dimensions"] == float64(cfg.EmbeddingDimensions) {
				wanted, exists := expected[stringValue(passage, "id")]
				if exists && passage["start"] == float64(wanted.start) && passage["end"] == float64(wanted.end) && passage["text"] == wanted.text && passage["vectorUsable"] == true {
					compatiblePassages++
				}
			} else {
				incompatible = true
			}
		}
		result.Passages += compatiblePassages
		if incompatible {
			result.Incompatible++
		}
		if current != nil && current["status"] == "completed" && generationPassages == len(expected) && compatiblePassages == len(expected) && current["passageCount"] == float64(compatiblePassages) {
			result.Indexed++
			continue
		}
		switch current["status"] {
		case "queued":
			result.Queued++
		case "running":
			result.Running++
		case "failed":
			result.Failed++
		default:
			result.Unindexed++
		}
	}
	if result.Indexed != result.Eligible {
		result.Status = "incomplete"
	}
	return result, rows.Err()
}
