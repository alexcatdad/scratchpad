package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

func hasInlineAnswer(record map[string]any) bool {
	if answered, projected := record["contextInlineAnswer"].(bool); projected {
		return answered
	}
	answer, ok := objectValue(record, "payload")["answer"].(string)
	return ok && strings.TrimSpace(answer) != ""
}

// Selection reads chronology and lifecycle inputs before fetching full bodies.
// In particular, long omitted findings never need decoding into Go objects.
func contextInputs(ctx context.Context, tx *sql.Tx, engine string, id any) ([]map[string]any, map[string]map[string]any, []map[string]any, error) {
	from, to := "json_extract(l.data,'$.fromRecordId')", "json_extract(l.data,'$.toRecordId')"
	if engine == "postgres" {
		from, to = "l.data->>'fromRecordId'", "l.data->>'toRecordId'"
	}
	rows, err := tx.QueryContext(ctx, "SELECT h.data,m.data FROM context_headers h LEFT JOIN entities m ON m.kind='metadata' AND m.id=h.record_id WHERE h.project_id=$1", id)
	if err != nil {
		return nil, nil, nil, err
	}
	records := []map[string]any{}
	metadata := map[string]map[string]any{}
	for rows.Next() {
		var raw, meta []byte
		if err = rows.Scan(&raw, &meta); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		var record map[string]any
		if err = json.Unmarshal(raw, &record); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		records = append(records, record)
		if meta != nil {
			var value map[string]any
			if err = json.Unmarshal(meta, &value); err != nil {
				rows.Close()
				return nil, nil, nil, err
			}
			metadata[stringValue(record, "id")] = value
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, nil, err
	}
	rows, err = tx.QueryContext(ctx, "SELECT l.data FROM entities l WHERE l.kind='relationship' AND EXISTS(SELECT 1 FROM context_headers h WHERE h.project_id=$1 AND (h.record_id="+from+" OR h.record_id="+to+")) ORDER BY l.created_at,l.id", id)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	links := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, nil, nil, err
		}
		var link map[string]any
		if err = json.Unmarshal(raw, &link); err != nil {
			return nil, nil, nil, err
		}
		links = append(links, link)
	}
	return records, metadata, links, rows.Err()
}

// Records are selected by immutable IDs in the same read transaction as their
// headers and curation. Reusing each map preserves section references and the
// contextual annotations while restoring the exact capture fields.
func hydrateContextRecords(ctx context.Context, tx *sql.Tx, records []map[string]any) error {
	byID := map[string]map[string]any{}
	for _, record := range records {
		byID[stringValue(record, "id")] = record
	}
	if len(byID) == 0 {
		return nil
	}
	args := []any{}
	placeholders := []string{}
	for id := range byID {
		args = append(args, id)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,data FROM entities WHERE kind='record' AND id IN ("+strings.Join(placeholders, ",")+")", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			return err
		}
		var capture map[string]any
		if err = json.Unmarshal(raw, &capture); err != nil {
			return err
		}
		record := byID[id]
		annotations := map[string]any{}
		for _, name := range []string{"metadata", "applicability", "replacedBy", "historicalStatus"} {
			if value, exists := record[name]; exists {
				annotations[name] = value
			}
		}
		clear(record)
		for name, value := range capture {
			record[name] = value
		}
		for name, value := range annotations {
			record[name] = value
		}
	}
	return rows.Err()
}
func answeredQuestions(records, links []map[string]any) map[string]bool {
	byID := map[string]map[string]any{}
	for _, record := range records {
		byID[stringValue(record, "id")] = record
	}
	answered := map[string]bool{}
	for _, link := range links {
		if link["type"] != "answers" || link["status"] != "accepted" {
			continue
		}
		answer := byID[stringValue(link, "fromRecordId")]
		if answer == nil || answer["applicability"] != "current" {
			continue
		}
		if answer["type"] == "qa" {
			if !hasInlineAnswer(answer) {
				continue
			}
		} else if answer["contextContentNonempty"] != true && strings.TrimSpace(stringValue(answer, "content")) == "" {
			raw, _ := json.Marshal(answer["payload"])
			text, err := renderCapture(stringValue(answer, "type"), raw)
			if err != nil || strings.TrimSpace(text) == "" {
				continue
			}
		}
		answered[stringValue(link, "toRecordId")] = true
	}
	return answered
}
