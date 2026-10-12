package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

func (a *AI) lexicalFallback(w http.ResponseWriter, r *http.Request, scope jobScope, query, intent string, limit, budget int, cause error) {
	var protocol *protocolError
	if !errors.As(cause, &protocol) || !contains([]string{"AI_UNAVAILABLE", "AI_DISABLED", "SECRET_UNAVAILABLE", "AI_CONFIGURATION_CHANGED", "RAG_UNAVAILABLE", "RAG_INDEX_INCOMPLETE"}, protocol.code) {
		a.auth.fail(w, cause)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
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
	ids, err := allowedJobs(ctx, tx, scope)
	if err != nil {
		a.auth.fail(w, err)
		return
	}
	coverageStatus := "unavailable"
	if protocol.code != "RAG_UNAVAILABLE" {
		coverage, err := a.ragCoverage(ctx, tx, scope)
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		coverageStatus = coverage.Status
	}
	semantic := map[string]any{"available": false, "indexCoverage": coverageStatus, "reason": protocol.code}
	terms := lexicalTerms.FindAllString(query, 30)
	results := []map[string]any{}
	omitted := 0
	if len(terms) == 0 {
		JSON(w, 200, map[string]any{"results": results, "resultsOmitted": omitted, "semantic": semantic})
		return
	}
	args := []any{strings.Join(terms, " ")}
	places := make([]string, len(ids))
	for i, id := range ids {
		places[i] = fmt.Sprintf("$%d", i+2)
		args = append(args, id)
	}
	filter := ""
	if intent == "current" {
		filter = ` AND COALESCE(m.data->'archived','false'::jsonb)<>'true'::jsonb AND COALESCE(m.data->>'legacyStatus','') NOT IN ('superseded','superseded_or_partial') AND NOT EXISTS(SELECT 1 FROM entities l WHERE l.kind='relationship' AND l.data->>'status'='accepted' AND l.data->>'type'='replaces' AND l.data->>'toRecordId'=r.id)`
	}
	statement := `SELECT r.data,m.data,ts_rank_cd(s.document,plainto_tsquery('simple',$1)),EXISTS(SELECT 1 FROM entities l WHERE l.kind='relationship' AND l.data->>'status'='accepted' AND l.data->>'type'='replaces' AND l.data->>'toRecordId'=r.id),EXISTS(SELECT 1 FROM entities l WHERE l.kind='relationship' AND l.data->>'status'='accepted' AND l.data->>'type'='partially_replaces' AND l.data->>'toRecordId'=r.id),COALESCE((SELECT jsonb_agg(edge.data) FROM(SELECT link.data FROM entities link JOIN entities f ON f.kind='record' AND f.id=link.data->>'fromRecordId' JOIN entities t ON t.kind='record' AND t.id=link.data->>'toRecordId' WHERE link.kind='relationship' AND link.data->>'status'='accepted' AND (f.id=r.id OR t.id=r.id) AND f.data->>'projectId' IN (` + strings.Join(places, ",") + `) AND t.data->>'projectId' IN (` + strings.Join(places, ",") + `) ORDER BY CASE link.data->>'type' WHEN 'replaces' THEN 0 WHEN 'partially_replaces' THEN 1 WHEN 'contradicts' THEN 2 ELSE 3 END,link.created_at,link.id LIMIT 10) edge),'[]'::jsonb) FROM record_search s JOIN entities r ON r.kind='record' AND r.id=s.record_id LEFT JOIN entities m ON m.kind='metadata' AND m.id=r.id WHERE s.document @@ plainto_tsquery('simple',$1) AND r.data->>'projectId' IN (` + strings.Join(places, ",") + `) AND COALESCE(r.data->>'authority','')<>'derived'` + filter + ` ORDER BY ts_rank_cd(s.document,plainto_tsquery('simple',$1)) DESC,r.id LIMIT 100`
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		a.auth.fail(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var raw, meta, edges []byte
		var score float64
		var full, partial bool
		if err = rows.Scan(&raw, &meta, &score, &full, &partial, &edges); err != nil {
			a.auth.fail(w, err)
			return
		}
		var record, metadata map[string]any
		var links []map[string]any
		if json.Unmarshal(edges, &links) != nil {
			a.auth.fail(w, aiUnavailable())
			return
		}
		if json.Unmarshal(raw, &record) != nil || (meta != nil && json.Unmarshal(meta, &metadata) != nil) {
			a.auth.fail(w, aiUnavailable())
			return
		}
		id := stringValue(record, "id")
		if !portableID(id) {
			continue
		}
		if len(results) >= limit {
			omitted++
			continue
		}
		hash, err := aiHash(record)
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		units := utf16.Encode([]rune(stringValue(record, "content")))
		bestStart, bestEnd, bestMatches := 0, 0, -1
		for start := 0; start < len(units) || bestMatches < 0; {
			end := min(start+6000, len(units))
			if end < len(units) && units[end-1] >= 0xd800 && units[end-1] <= 0xdbff {
				end--
			}
			text := strings.ToLower(string(utf16.Decode(units[start:end])))
			matches := 0
			for _, term := range terms {
				if strings.Contains(text, strings.ToLower(term)) {
					matches++
				}
			}
			if matches > bestMatches {
				bestStart, bestEnd, bestMatches = start, end, matches
			}
			start = end
			if start == len(units) {
				break
			}
		}
		text := string(utf16.Decode(units[bestStart:bestEnd]))
		lower := strings.ToLower(text)
		match := -1
		for _, term := range terms {
			at := strings.Index(lower, strings.ToLower(term))
			if at >= 0 && (match < 0 || at < match) {
				match = at
			}
		}
		excerptStart := bestStart
		if match >= 0 {
			runes := []rune(text)
			prefix := string(runes[:utf8.RuneCountInString(lower[:match])])
			excerptStart = bestStart + max(0, len(utf16.Encode([]rune(prefix)))-160)
			if excerptStart > bestStart && units[excerptStart] >= 0xdc00 && units[excerptStart] <= 0xdfff {
				excerptStart--
			}
		}
		excerpt := ragExcerpt(string(utf16.Decode(units[excerptStart:bestEnd])), 1000)
		identity, err := aiHash(map[string]any{"recordId": id, "contentHash": hash, "processingVersion": ragProcessingVersion, "start": bestStart, "end": bestEnd})
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		applicability := lifecycle(record, metadata, nil)
		if !truthy(metadata["archived"]) {
			if full {
				applicability = "superseded"
			} else if partial {
				applicability = "partially_superseded"
			}
		}
		if applicability == "active" {
			applicability = "current"
		}
		if applicability == "current" && truthy(objectValue(record, "provenance")["import"]) {
			applicability = "unverified"
		}
		location := map[string]any{"field": "content", "unit": "utf16", "start": excerptStart, "end": excerptStart + len(utf16.Encode([]rune(excerpt)))}
		citations := []map[string]any{}
		for _, link := range links {
			citation := map[string]any{"id": link["id"], "type": link["type"], "fromRecordId": link["fromRecordId"], "toRecordId": link["toRecordId"]}
			if note := stringValue(link, "note"); note != "" {
				citation["note"] = ragExcerpt(note, 128)
			}
			citations = append(citations, citation)
		}
		item := map[string]any{"id": "passage_" + identity, "recordId": id, "projectId": record["projectId"], "text": excerpt, "textTruncated": excerpt != text, "score": score, "sourceLocation": location, "sourceUrl": "/api/v1/records/" + id, "authority": record["authority"], "confidence": record["confidence"], "applicability": applicability, "contentHash": hash, "processingVersion": ragProcessingVersion, "retrieval": "lexical", "relationships": citations, "relationshipsOmitted": 0, "relationshipPreviewLimitReached": len(links) == 10}
		for {
			candidate := append(append([]map[string]any{}, results...), item)
			encoded, err := json.Marshal(map[string]any{"results": candidate, "resultsOmitted": omitted, "semantic": semantic})
			if err != nil {
				a.auth.fail(w, err)
				return
			}
			if len(encoded)+1 <= budget-32 {
				results = candidate
				break
			}
			length := len(utf16.Encode([]rune(excerpt)))
			if len(results) > 0 {
				omitted++
				break
			}
			if len(citations) > 0 {
				citations = citations[:len(citations)-1]
				item["relationships"], item["relationshipsOmitted"] = citations, len(links)-len(citations)
				continue
			}
			if length <= 32 {
				omitted++
				break
			}
			excerpt = ragExcerpt(excerpt, max(32, length/2))
			item["text"], item["textTruncated"] = excerpt, true
			location["end"] = excerptStart + len(utf16.Encode([]rune(excerpt)))
		}
	}
	if err = rows.Err(); err != nil {
		a.auth.fail(w, err)
		return
	}
	JSON(w, 200, map[string]any{"results": results, "resultsOmitted": omitted, "semantic": semantic})
}
