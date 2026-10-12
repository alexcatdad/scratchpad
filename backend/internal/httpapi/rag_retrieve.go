package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf16"
)

func ragLimit(body map[string]any, field string, fallback, lowest, highest int) (int, error) {
	value, exists := body[field]
	if !exists {
		return fallback, nil
	}
	n, ok := value.(float64)
	if !ok || n < float64(lowest) || n > float64(highest) || n != math.Trunc(n) {
		return 0, aiInvalid()
	}
	return int(n), nil
}
func ragExcerpt(text string, limit int) string {
	units := utf16.Encode([]rune(text))
	if len(units) <= limit {
		return text
	}
	if units[limit-1] >= 0xd800 && units[limit-1] <= 0xdbff {
		limit--
	}
	return string(utf16.Decode(units[:limit]))
}
func (a *AI) RAGRetrieve() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := apiActor(r.Context(), a.auth.Store.DB, r, a.auth.Origin); err != nil {
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
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		query, ok := body["query"].(string)
		query = strings.TrimSpace(query)
		if !ok || query == "" || len(utf16.Encode([]rune(query))) > 4000 {
			a.auth.fail(w, aiInvalid())
			return
		}
		limit, err := ragLimit(body, "limit", 20, 1, 100)
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		budget, err := ragLimit(body, "maxChars", 12000, 4096, 64000)
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		intent := "current"
		if value, exists := body["intent"]; exists {
			intent, ok = value.(string)
			if !ok || !contains([]string{"current", "history"}, intent) {
				a.auth.fail(w, aiInvalid())
				return
			}
		}
		if err = a.ragAvailable(r.Context(), a.auth.Store.DB); err != nil {
			a.auth.fail(w, err)
			return
		}
		snapshot, err := a.provider(r.Context())
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		snapshot.passage = true
		fingerprint, err := ragFingerprint(snapshot.config)
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(snapshot.config.RequestTimeoutSeconds+10)*time.Second)
		defer cancel()
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Duration(snapshot.config.RequestTimeoutSeconds+10) * time.Second))
		vectors, err := a.embedInputs(ctx, r, snapshot, []string{query}, func(conn *sql.Conn) error {
			if err := a.ragAvailable(ctx, conn); err != nil {
				return err
			}
			_, err := allowedJobs(ctx, conn, scope)
			return err
		})
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		vector, err := ragVector(vectors[0])
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		results := []map[string]any{}
		coverageStatus := "incomplete"
		omitted := 0
		err = a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
			if err := a.providerCurrent(ctx, conn, r, snapshot); err != nil {
				return err
			}
			if err := a.ragAvailable(ctx, conn); err != nil {
				return err
			}
			coverage, err := a.ragCoverage(ctx, conn, scope)
			if err != nil {
				return err
			}
			coverageStatus = coverage.Status
			ids, err := allowedJobs(ctx, conn, scope)
			if err != nil {
				return err
			}
			args := []any{fingerprint, vector, snapshot.config.EmbeddingDimensions}
			placeholders := make([]string, len(ids))
			for i, id := range ids {
				placeholders[i] = fmt.Sprintf("$%d", i+4)
				args = append(args, id)
			}
			selected := strings.Join(placeholders, ",")
			filter := ""
			if intent == "current" {
				filter = ` AND COALESCE(m.data->'archived','false'::jsonb) <> 'true'::jsonb AND COALESCE(m.data->>'legacyStatus','') NOT IN ('superseded','superseded_or_partial') AND NOT EXISTS(SELECT 1 FROM entities l WHERE l.kind='relationship' AND l.data->>'status'='accepted' AND l.data->>'type'='replaces' AND l.data->>'toRecordId'=r.id)`
			}
			statement := `WITH eligible AS MATERIALIZED(SELECT p.*,r.data AS record,m.data AS metadata FROM rag_passages p JOIN entities r ON r.kind='record' AND r.id=p.record_id AND r.data->>'projectId'=p.project_id LEFT JOIN entities m ON m.kind='metadata' AND m.id=r.id WHERE p.fingerprint=$1 AND p.dimensions=$3 AND p.project_id IN (` + selected + `)` + filter + `) SELECT p.id,p.record_id,p.project_id,p.content_hash,p.processing_version,p.source_start,p.source_end,p.text,p.record,p.metadata,1-(p.embedding <=> $2::vector) AS score,EXISTS(SELECT 1 FROM entities replacement WHERE replacement.kind='relationship' AND replacement.data->>'status'='accepted' AND replacement.data->>'type'='replaces' AND replacement.data->>'toRecordId'=p.record_id),EXISTS(SELECT 1 FROM entities replacement WHERE replacement.kind='relationship' AND replacement.data->>'status'='accepted' AND replacement.data->>'type'='partially_replaces' AND replacement.data->>'toRecordId'=p.record_id),COALESCE((SELECT jsonb_agg(l.data) FROM (SELECT link.data FROM entities link JOIN entities f ON f.kind='record' AND f.id=link.data->>'fromRecordId' JOIN entities t ON t.kind='record' AND t.id=link.data->>'toRecordId' WHERE link.kind='relationship' AND link.data->>'status'='accepted' AND (f.id=p.record_id OR t.id=p.record_id) AND f.data->>'projectId' IN (` + selected + `) AND t.data->>'projectId' IN (` + selected + `) ORDER BY CASE link.data->>'type' WHEN 'replaces' THEN 0 WHEN 'partially_replaces' THEN 1 WHEN 'contradicts' THEN 2 ELSE 3 END,link.created_at,link.id LIMIT 10) l),'[]'::jsonb) FROM eligible p ORDER BY score DESC,p.id LIMIT 100`
			rows, err := conn.QueryContext(ctx, statement, args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id, recordID, projectID, hash, processing, text string
				var start, end int
				var score float64
				var fullReplacement, partialReplacement bool
				var raw, meta, edges []byte
				if err = rows.Scan(&id, &recordID, &projectID, &hash, &processing, &start, &end, &text, &raw, &meta, &score, &fullReplacement, &partialReplacement, &edges); err != nil {
					return err
				}
				var record, metadata map[string]any
				var links []map[string]any
				if json.Unmarshal(raw, &record) != nil || json.Unmarshal(edges, &links) != nil {
					return aiUnavailable()
				}
				if meta != nil && json.Unmarshal(meta, &metadata) != nil {
					return aiUnavailable()
				}
				currentHash, err := aiHash(record)
				if err != nil {
					return err
				}
				units := utf16.Encode([]rune(stringValue(record, "content")))
				if currentHash != hash || processing != ragProcessingVersion || !portableID(recordID) || start < 0 || end < start || end > len(units) || string(utf16.Decode(units[start:end])) != text || record["authority"] == "derived" || math.IsNaN(score) || math.IsInf(score, 0) {
					continue
				}
				if score < snapshot.config.SimilarityThreshold {
					continue
				}
				if len(results) >= limit {
					omitted++
					continue
				}
				applicability := lifecycle(record, metadata, links)
				// Governing lifecycle is independent of scoped, bounded edge previews.
				if !truthy(metadata["archived"]) {
					if fullReplacement {
						applicability = "superseded"
					} else if partialReplacement {
						applicability = "partially_superseded"
					}
				}
				if applicability == "active" {
					applicability = "current"
				}
				if applicability == "current" && truthy(objectValue(record, "provenance")["import"]) {
					applicability = "unverified"
				}
				excerpt := ragExcerpt(text, 1000)
				location := map[string]any{"field": "content", "unit": "utf16", "start": start, "end": start + len(utf16.Encode([]rune(excerpt)))}
				citations := []map[string]any{}
				for _, link := range links {
					citation := map[string]any{"id": link["id"], "type": link["type"], "fromRecordId": link["fromRecordId"], "toRecordId": link["toRecordId"]}
					if note := stringValue(link, "note"); note != "" {
						citation["note"] = ragExcerpt(note, 128)
					}
					citations = append(citations, citation)
				}
				item := map[string]any{"id": id, "recordId": recordID, "projectId": projectID, "text": excerpt, "textTruncated": excerpt != text, "score": score, "sourceLocation": location, "sourceUrl": "/api/v1/records/" + recordID, "authority": record["authority"], "confidence": record["confidence"], "applicability": applicability, "contentHash": hash, "processingVersion": processing, "relationships": citations, "relationshipsOmitted": 0, "relationshipPreviewLimitReached": len(links) == 10}
				for {
					candidate := append(append([]map[string]any{}, results...), item)
					encoded, err := json.Marshal(map[string]any{"results": candidate, "resultsOmitted": omitted, "semantic": map[string]any{"available": true, "indexCoverage": "not_assessed"}})
					if err != nil {
						return err
					}
					// Reserve space for the final omitted-candidate count.
					if len(encoded)+1 <= budget-32 {
						results = candidate
						break
					}
					if len(results) > 0 {
						omitted++
						break
					}
					if len(citations) > 0 {
						citations = citations[:len(citations)-1]
						item["relationships"], item["relationshipsOmitted"] = citations, len(links)-len(citations)
						continue
					}
					length := len(utf16.Encode([]rune(excerpt)))
					if length <= 32 {
						omitted++
						break
					}
					excerpt = ragExcerpt(excerpt, max(32, length/2))
					item["text"], item["textTruncated"] = excerpt, true
					location["end"] = start + len(utf16.Encode([]rune(excerpt)))
				}
			}
			return rows.Err()
		})
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		JSON(w, 200, map[string]any{"results": results, "resultsOmitted": omitted, "semantic": map[string]any{"available": true, "indexCoverage": coverageStatus}})
	}
}
