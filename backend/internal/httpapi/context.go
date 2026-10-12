package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func (s *SSH) CompactProjectContext() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		if _, err := apiActor(ctx, s.Store.DB, r, s.Origin); err != nil {
			s.fail(w, err)
			return
		}
		params := r.URL.Query()
		if values, exists := params["version"]; exists && (len(values) != 1 || values[0] != "2") {
			s.fail(w, validationProject())
			return
		}
		params.Set("version", "2")
		r.URL.RawQuery = params.Encode()
		s.ProjectContext()(w, r)
	}
}

func (s *SSH) ProjectContext() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		options := &sql.TxOptions{ReadOnly: true}
		if s.Store.Engine == "postgres" {
			options.Isolation = sql.LevelRepeatableRead
		}
		tx, err := s.Store.DB.BeginTx(ctx, options)
		if err != nil {
			s.fail(w, err)
			return
		}
		defer tx.Rollback()
		if _, err = apiActor(ctx, tx, r, s.Origin); err != nil {
			s.fail(w, err)
			return
		}
		project, err := projectEntity(ctx, tx, r.PathValue("id"))
		if err != nil {
			s.fail(w, err)
			return
		}
		var result map[string]any
		version := r.URL.Query().Get("version")
		if values := r.URL.Query()["version"]; len(values) > 1 {
			s.fail(w, validationProject())
			return
		}
		switch version {
		case "", "1":
			result, err = projectContext(ctx, tx, project, s.Store.Engine)
		case "2":
			result, err = compactContext(ctx, tx, project, s.Store.Engine, r.URL.Query())
		default:
			err = validationProject()
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		JSON(w, 200, result)
	}
}
func contextInstant(record map[string]any) string {
	for _, field := range []string{"happenedAt", "recordedAt", "createdAt"} {
		if value, exists := record[field]; exists && value != nil {
			return stringValue(record, field)
		}
	}
	return ""
}
func contextSelect(records []map[string]any, limit int, predicate func(map[string]any) bool) []map[string]any {
	selected := []map[string]any{}
	for _, record := range records {
		if predicate(record) {
			selected = append(selected, record)
			if len(selected) == limit {
				break
			}
		}
	}
	return selected
}
func projectContextRecords(ctx context.Context, tx *sql.Tx, project map[string]any, engine string) ([]map[string]any, []map[string]any, error) {
	all, byID, links, err := contextInputs(ctx, tx, engine, project["id"])
	if err != nil {
		return nil, nil, err
	}
	incoming := map[string][]map[string]any{}
	for _, link := range links {
		if link["status"] == "accepted" && (link["type"] == "replaces" || link["type"] == "partially_replaces") {
			target := stringValue(link, "toRecordId")
			incoming[target] = append(incoming[target], link)
		}
	}
	records := []map[string]any{}
	for _, record := range all {
		if record["projectId"] != project["id"] {
			continue
		}
		meta := byID[stringValue(record, "id")]
		if meta != nil {
			record["metadata"] = meta
		}
		legacy := meta["legacyStatus"]
		replacements := incoming[stringValue(record, "id")]
		applicability := "current"
		full := false
		for _, link := range replacements {
			if link["type"] == "replaces" {
				full = true
			}
		}
		switch {
		case truthy(meta["archived"]):
			applicability = "archived"
		case full:
			applicability = "superseded"
		case len(replacements) > 0:
			applicability = "partially_superseded"
		case legacy == "superseded":
			applicability = "historically_superseded"
		case legacy == "superseded_or_partial":
			applicability = "requires_review"
		case truthy(objectValue(record, "provenance")["import"]):
			applicability = "unverified"
		}
		replacedBy := []map[string]any{}
		for _, link := range replacements {
			reference := map[string]any{"recordId": link["fromRecordId"], "relationshipId": link["id"], "type": link["type"]}
			if note, exists := link["note"]; exists {
				reference["note"] = note
			}
			replacedBy = append(replacedBy, reference)
		}
		record["applicability"], record["replacedBy"], record["historicalStatus"] = applicability, replacedBy, legacy
		records = append(records, record)
	}
	ordering := collate.New(language.English)
	sort.Slice(records, func(i, j int) bool {
		left, right := records[i], records[j]
		if comparison, err := compareInstants(contextInstant(left), contextInstant(right)); err == nil && comparison != 0 {
			return comparison > 0
		}
		if comparison, err := compareInstants(stringValue(left, "createdAt"), stringValue(right, "createdAt")); err == nil && comparison != 0 {
			return comparison > 0
		}
		return ordering.CompareString(stringValue(left, "id"), stringValue(right, "id")) > 0
	})
	return records, links, nil
}
func projectContext(ctx context.Context, tx *sql.Tx, project map[string]any, engine string) (map[string]any, error) {
	records, links, err := projectContextRecords(ctx, tx, project, engine)
	if err != nil {
		return nil, err
	}
	available := contextSelect(records, len(records)+1, func(record map[string]any) bool {
		return record["applicability"] == "current" || record["applicability"] == "unverified" || record["applicability"] == "partially_superseded"
	})
	answered := answeredQuestions(records, links)
	types := func(kinds ...string) []map[string]any {
		return contextSelect(available, 20, func(record map[string]any) bool { return containsString(kinds, record["type"]) })
	}
	states := types("project_state")
	var current any
	for _, record := range records {
		if record["type"] == "project_state" && record["applicability"] == "current" {
			current = record
			break
		}
	}
	applicable := contextSelect(records, 30, func(record map[string]any) bool { return record["applicability"] == "current" })
	currentCount := 0
	for _, record := range records {
		if record["applicability"] == "current" {
			currentCount++
		}
	}
	recordIDs := map[string]bool{}
	for _, record := range records {
		recordIDs[stringValue(record, "id")] = true
	}
	relevant := []map[string]any{}
	for _, link := range links {
		if recordIDs[stringValue(link, "fromRecordId")] || recordIDs[stringValue(link, "toRecordId")] {
			relevant = append(relevant, link)
			if len(relevant) == 100 {
				break
			}
		}
	}
	return map[string]any{
		"project":         project,
		"currentState":    current,
		"state":           states,
		"stateHistory":    contextSelect(records, 20, func(record map[string]any) bool { return record["type"] == "project_state" }),
		"recentDecisions": types("decision", "adr", "business_decision"),
		"constraints":     types("constraint"),
		"openFindings":    types("finding"),
		"failures":        types("failure"),
		"openQuestions": contextSelect(available, 20, func(record map[string]any) bool {
			return record["type"] == "qa" && !hasInlineAnswer(record) && !answered[stringValue(record, "id")]
		}),
		"applicableRecords":   applicable,
		"partiallySuperseded": contextSelect(records, 20, func(record map[string]any) bool { return record["applicability"] == "partially_superseded" }),
		"historicalRecords": contextSelect(records, 20, func(record map[string]any) bool {
			return containsString([]string{"archived", "superseded", "historically_superseded"}, record["applicability"])
		}),
		"requiresReview": contextSelect(records, 20, func(record map[string]any) bool {
			return containsString([]string{"unverified", "requires_review"}, record["applicability"])
		}),
		"relationships": relevant,
		"counts":        map[string]any{"total": len(records), "current": currentCount},
		"notice":        "Deterministic context reflects recorded provenance and accepted relationships. Imported lifecycle statements remain historical evidence until reviewed.",
	}, nil
}
