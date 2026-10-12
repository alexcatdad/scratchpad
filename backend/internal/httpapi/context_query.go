package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

func hasInlineAnswer(record map[string]any) bool {
	answer, ok := objectValue(record, "payload")["answer"].(string)
	return ok && strings.TrimSpace(answer) != ""
}

// Context loads only this project's sources and edges; raw captures remain unchanged.
func contextInputs(ctx context.Context, tx *sql.Tx, engine string, id any) ([]map[string]any, map[string]map[string]any, []map[string]any, error) {
	project := "json_extract(r.data,'$.projectId')"
	from, to := "json_extract(l.data,'$.fromRecordId')", "json_extract(l.data,'$.toRecordId')"
	if engine == "postgres" {
		project = "r.data->>'projectId'"
		from, to = "l.data->>'fromRecordId'", "l.data->>'toRecordId'"
	}
	rows, err := tx.QueryContext(ctx, "SELECT r.data,m.data FROM entities r LEFT JOIN entities m ON m.kind='metadata' AND m.id=r.id WHERE r.kind='record' AND "+project+"=$1", id)
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
	rows, err = tx.QueryContext(ctx, "SELECT l.data FROM entities l WHERE l.kind='relationship' AND EXISTS(SELECT 1 FROM entities r WHERE r.kind='record' AND "+project+"=$1 AND (r.id="+from+" OR r.id="+to+")) ORDER BY l.created_at,l.id", id)
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
		} else if strings.TrimSpace(stringValue(answer, "content")) == "" {
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
