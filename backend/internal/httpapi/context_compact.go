package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"strconv"
	"unicode/utf16"
)

func compactLimit(params url.Values, name string, fallback, minValue, maxValue int) (int, error) {
	values, exists := params[name]
	if !exists {
		return fallback, nil
	}
	if len(values) != 1 {
		return 0, validationProject()
	}
	value, err := strconv.Atoi(values[0])
	if err != nil || value < minValue || value > maxValue {
		return 0, validationProject()
	}
	return value, nil
}
func compactText(value string, limit int) (string, bool) {
	units := utf16.Encode([]rune(value))
	if len(units) <= limit {
		return value, false
	}
	end := limit
	if end > 0 && units[end-1] >= 0xd800 && units[end-1] <= 0xdbff {
		end--
	}
	return string(utf16.Decode(units[:end])), true
}
func compactAvailable(record map[string]any) bool {
	return contains([]string{"current", "unverified", "partially_superseded"}, stringValue(record, "applicability"))
}
func compactContext(ctx context.Context, tx *sql.Tx, project map[string]any, engine string, params url.Values) (map[string]any, error) {
	limit, err := compactLimit(params, "limit", 20, 1, 100)
	if err != nil {
		return nil, err
	}
	budget, err := compactLimit(params, "maxChars", 12000, 4096, 64000)
	if err != nil {
		return nil, err
	}
	previewLimit, err := compactLimit(params, "previewChars", 320, 32, 2000)
	if err != nil {
		return nil, err
	}
	records, links, err := projectContextRecords(ctx, tx, project, engine)
	if err != nil {
		return nil, err
	}
	answered := answeredQuestions(records, links)
	currentCount, openCount := 0, 0
	currentID := ""
	for _, record := range records {
		if record["applicability"] == "current" {
			currentCount++
			if currentID == "" && record["type"] == "project_state" {
				currentID = stringValue(record, "id")
			}
		}
		if compactAvailable(record) && record["type"] == "qa" && !hasInlineAnswer(record) && !answered[stringValue(record, "id")] {
			openCount++
		}
	}
	ordered := []map[string]any{}
	if currentID != "" {
		for _, record := range records {
			if record["id"] == currentID {
				ordered = append(ordered, record)
				break
			}
		}
	}
	for _, record := range records {
		if record["id"] != currentID && compactAvailable(record) {
			ordered = append(ordered, record)
		}
	}
	for _, record := range records {
		if !compactAvailable(record) {
			ordered = append(ordered, record)
		}
	}
	previews := []map[string]any{}
	selected := ordered[:min(limit, len(ordered))]
	if err := hydrateContextRecords(ctx, tx, selected); err != nil {
		return nil, err
	}
	for _, record := range selected {
		title, titleCut := compactText(stringValue(record, "title"), 120)
		content := stringValue(record, "content")
		if content == "" {
			raw, _ := json.Marshal(record["payload"])
			if rendered, err := renderCapture(stringValue(record, "type"), raw); err == nil {
				content = rendered
			}
		}
		preview, cut := compactText(content, previewLimit)
		authority, confidence := stringValue(record, "authority"), stringValue(record, "confidence")
		if authority == "" {
			authority = "unknown"
		}
		if confidence == "" {
			confidence = "unknown"
		}
		item := map[string]any{"id": record["id"], "type": record["type"], "title": title, "preview": preview, "previewTruncated": cut, "titleTruncated": titleCut, "authority": authority, "confidence": confidence, "applicability": record["applicability"], "recordedAt": record["recordedAt"], "sourceDetailsAvailable": true}
		if record["type"] == "project_state" {
			for _, field := range []string{"state", "reason", "followUp"} {
				if value, ok := objectValue(record, "payload")[field].(string); ok {
					short, truncated := compactText(value, 160)
					item[field] = short
					if truncated {
						item["previewTruncated"] = true
					}
				}
			}
		}
		if record["type"] == "qa" {
			status := "unanswered"
			if hasInlineAnswer(record) || answered[stringValue(record, "id")] {
				status = "answered"
			}
			item["questionStatus"] = status
		}
		previews = append(previews, item)
	}
	edges := []map[string]any{}
	for _, link := range links {
		if len(edges) == 100 {
			break
		}
		edge := map[string]any{}
		for _, field := range []string{"id", "fromRecordId", "toRecordId", "type", "status", "authority"} {
			if value, exists := link[field]; exists {
				edge[field] = value
			}
		}
		edges = append(edges, edge)
	}
	name, nameCut := compactText(stringValue(project, "name"), 200)
	result := map[string]any{"version": 2, "project": map[string]any{"id": project["id"], "name": name, "nameTruncated": nameCut, "kind": project["kind"]}, "records": previews, "relationships": edges, "counts": map[string]any{"total": len(records), "current": currentCount, "openQuestions": openCount}, "limits": map[string]any{"records": limit, "maxChars": budget, "previewChars": previewLimit}, "notice": "Previews retain capture authority and lifecycle. Fetch source IDs for full evidence; omitted context is not absent evidence."}
	limited := false
	for {
		sections := map[string]any{"currentState": nil}
		for _, key := range []string{"stateHistory", "decisions", "constraints", "findings", "failures", "openQuestions", "history", "requiresReview"} {
			sections[key] = []string{}
		}
		cuts := 0
		for _, item := range previews {
			id := stringValue(item, "id")
			if item["previewTruncated"] == true {
				cuts++
			}
			if id == currentID {
				sections["currentState"] = id
			}
			key := ""
			switch stringValue(item, "type") {
			case "project_state":
				if id != currentID {
					key = "stateHistory"
				}
			case "decision", "adr", "business_decision":
				key = "decisions"
			case "constraint":
				key = "constraints"
			case "finding":
				key = "findings"
			case "failure":
				key = "failures"
			case "qa":
				if item["questionStatus"] == "unanswered" {
					key = "openQuestions"
				}
			}
			if !compactAvailable(item) {
				key = "history"
			}
			if key != "" {
				sections[key] = append(sections[key].([]string), id)
			}
			if contains([]string{"unverified", "requires_review"}, stringValue(item, "applicability")) {
				sections["requiresReview"] = append(sections["requiresReview"].([]string), id)
			}
		}
		result["records"], result["relationships"], result["sections"] = previews, edges, sections
		result["truncation"] = map[string]any{"recordsOmitted": len(records) - len(previews), "relationshipsOmitted": len(links) - len(edges), "previewsTruncated": cuts, "budgetLimited": limited}
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		if len(encoded)+1 <= budget {
			return result, nil
		}
		limited = true
		if len(edges) > 0 {
			edges = edges[:len(edges)-1]
			continue
		}
		if len(previews) > 1 {
			previews = previews[:len(previews)-1]
			continue
		}
		changed := false
		for _, item := range previews {
			for _, field := range []string{"title", "preview", "state", "reason", "followUp"} {
				if value, ok := item[field].(string); ok && value != "" {
					short, _ := compactText(value, len(utf16.Encode([]rune(value)))/2)
					item[field] = short
					item["previewTruncated"] = true
					changed = true
				}
			}
		}
		if value := result["project"].(map[string]any)["name"].(string); value != "" {
			short, _ := compactText(value, len(utf16.Encode([]rune(value)))/2)
			result["project"].(map[string]any)["name"] = short
			result["project"].(map[string]any)["nameTruncated"] = true
			changed = true
		}
		if !changed {
			return nil, &protocolError{"CONTEXT_BUDGET_TOO_SMALL", "Context references exceed this response budget.", 400}
		}
	}
}
