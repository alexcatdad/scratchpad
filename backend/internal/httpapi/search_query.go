package httpapi

import (
	"bytes"
	"container/heap"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func searchText(engine, value, path string) string {
	if engine == "postgres" {
		path = "'{" + strings.ReplaceAll(path, ".", ",") + "}'"
		return "CASE WHEN jsonb_typeof(" + value + "#>" + path + ")='string' THEN " + value + "#>>" + path + " END"
	}
	path = "'$." + path + "'"
	return "CASE WHEN json_valid(" + value + ") THEN CASE WHEN json_type(" + value + "," + path + ")='text' THEN json_extract(" + value + "," + path + ") END END"
}

func searchArchived(engine string) string {
	if engine == "postgres" {
		return `COALESCE(m.data->'archived' NOT IN ('null'::jsonb,'false'::jsonb,'0'::jsonb,'""'::jsonb),false)`
	}
	return `CASE json_type(m.data,'$.archived') WHEN 'null' THEN 0 WHEN 'false' THEN 0 WHEN 'true' THEN 1 WHEN 'integer' THEN json_extract(m.data,'$.archived')<>0 WHEN 'real' THEN json_extract(m.data,'$.archived')<>0 WHEN 'text' THEN json_extract(m.data,'$.archived')<>'' WHEN 'array' THEN 1 WHEN 'object' THEN 1 ELSE 0 END`
}

func searchNativeAdmission(engine string, params url.Values) (string, []any) {
	where, args := []string{}, []any{}
	argument := func(value any) string { args = append(args, value); return fmt.Sprintf("$%d", len(args)) }
	join := ""
	needSource := false
	for _, field := range []string{"authority", "confidence", "branch", "gitPath", "source"} {
		needSource = needSource || params.Get(field) != ""
	}
	if needSource {
		join += " JOIN entities r ON r.kind='record' AND r.id=h.record_id"
	}
	if params.Get("tag") != "" || params.Get("status") != "" || params.Get("archived") != "" {
		join += " LEFT JOIN entities m ON m.kind='metadata' AND m.id=h.record_id"
	}
	for _, filter := range []struct{ parameter, column string }{{"projectId", "h.project_id"}, {"projectIds", "h.project_id"}, {"type", searchText(engine, "h.data", "type")}, {"types", searchText(engine, "h.data", "type")}} {
		values := params[filter.parameter]
		if filter.parameter == "projectId" || filter.parameter == "type" {
			values = nil
			if value := params.Get(filter.parameter); value != "" {
				values = []string{value}
			}
		}
		if len(values) == 0 {
			continue
		}
		places := []string{}
		for _, value := range values {
			places = append(places, argument(value))
		}
		where = append(where, "("+filter.column+") IN ("+strings.Join(places, ",")+")")
	}
	for _, filter := range []struct{ parameter, path string }{{"authority", "authority"}, {"confidence", "confidence"}, {"branch", "gitContext.branch"}, {"gitPath", "gitContext.rootPathHint"}} {
		if value := params.Get(filter.parameter); value != "" {
			where = append(where, "("+searchText(engine, "r.data", filter.path)+")="+argument(value))
		}
	}
	if value := params.Get("source"); value != "" {
		place := argument(value)
		parts := []string{}
		for _, path := range []string{"provenance.import.sourceName", "actor.client", "actor.displayName"} {
			parts = append(parts, "("+searchText(engine, "r.data", path)+")="+place)
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	if value := params.Get("tag"); value != "" {
		place := argument(value)
		predicate := `EXISTS(SELECT 1 FROM json_each(CASE WHEN json_type(m.data,'$.tags')='array' THEN json_extract(m.data,'$.tags') ELSE '[]' END) tag WHERE tag.type='text' AND tag.value=` + place + `)`
		if engine == "postgres" {
			predicate = `EXISTS(SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(m.data->'tags')='array' THEN m.data->'tags' ELSE '[]'::jsonb END) tag(value) WHERE jsonb_typeof(tag.value)='string' AND tag.value#>>'{}'=` + place + `)`
		}
		where = append(where, predicate)
	}
	archived := searchArchived(engine)
	if value := params.Get("archived"); value != "" {
		expected := "false"
		if value == "true" {
			expected = "true"
		}
		where = append(where, "("+archived+")="+expected)
	}
	incoming := func(kind string) string {
		return `EXISTS(SELECT 1 FROM entities l WHERE l.kind='relationship' AND (` + searchText(engine, "l.data", "status") + `)='accepted' AND (` + searchText(engine, "l.data", "type") + `)='` + kind + `' AND (` + searchText(engine, "l.data", "toRecordId") + `)=h.record_id)`
	}
	if value := params.Get("status"); value != "" {
		legacy := `CASE WHEN json_type(m.data,'$.legacyStatus') IS NULL OR json_type(m.data,'$.legacyStatus')='null' THEN 'active' ELSE ` + searchText(engine, "m.data", "legacyStatus") + ` END`
		if engine == "postgres" {
			legacy = `CASE WHEN m.data->'legacyStatus' IS NULL OR m.data->'legacyStatus'='null'::jsonb THEN 'active' ELSE ` + searchText(engine, "m.data", "legacyStatus") + ` END`
		}
		status := `CASE WHEN ` + archived + ` THEN 'archived' WHEN ` + incoming("replaces") + ` THEN 'superseded' WHEN ` + incoming("partially_replaces") + ` THEN 'partially_superseded' ELSE ` + legacy + ` END`
		where = append(where, "("+status+")="+argument(value))
	}
	if relation, related := params.Get("relationshipType"), params.Get("relatedTo"); relation != "" || related != "" {
		predicates := []string{`l.kind='relationship'`, `COALESCE((` + searchText(engine, "l.data", "status") + `) <> 'rejected',true)`, `((` + searchText(engine, "l.data", "fromRecordId") + `)=h.record_id OR (` + searchText(engine, "l.data", "toRecordId") + `)=h.record_id)`}
		if relation != "" {
			predicates = append(predicates, "("+searchText(engine, "l.data", "type")+")="+argument(relation))
		}
		if related != "" {
			place := argument(related)
			predicates = append(predicates, "(("+searchText(engine, "l.data", "fromRecordId")+")="+place+" OR ("+searchText(engine, "l.data", "toRecordId")+")="+place+")")
		}
		where = append(where, "EXISTS(SELECT 1 FROM entities l WHERE "+strings.Join(predicates, " AND ")+")")
	}
	if query := params.Get("q"); query != "" {
		terms := lexicalTerms.FindAllString(query, 30)
		if len(terms) == 0 {
			where = append(where, "false")
		} else {
			join += " JOIN record_search ON record_search.record_id=h.record_id"
			parts := []string{}
			for _, term := range terms {
				parts = append(parts, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
			}
			value := strings.Join(parts, " AND ")
			if engine == "postgres" {
				value = strings.Join(terms, " ")
			}
			place := argument(value)
			predicate := "record_search MATCH " + place
			if engine == "postgres" {
				predicate = "record_search.document @@ plainto_tsquery('simple'," + place + ")"
			}
			where = append(where, predicate)
		}
	}
	date := searchText(engine, "h.data", "createdAt")
	instant := `CASE WHEN json_type(h.data,'$.happenedAt') IS NOT NULL AND json_type(h.data,'$.happenedAt')<>'null' THEN ` + searchText(engine, "h.data", "happenedAt") + ` ELSE ` + searchText(engine, "h.data", "recordedAt") + ` END`
	if engine == "postgres" {
		instant = `CASE WHEN h.data->'happenedAt' IS NOT NULL AND h.data->'happenedAt'<>'null'::jsonb THEN ` + searchText(engine, "h.data", "happenedAt") + ` ELSE ` + searchText(engine, "h.data", "recordedAt") + ` END`
	}
	statement := `WITH admitted AS(SELECT DISTINCT h.record_id,COALESCE((` + date + `),'') AS created_at,COALESCE((` + instant + `),'') AS instant FROM context_headers h` + join
	if len(where) > 0 {
		statement += " WHERE " + strings.Join(where, " AND ")
	}
	statement += `), ordered AS(SELECT record_id,created_at,instant,` + searchCanonicalUTC(engine, "created_at") + ` AS sort_key,` + searchCanonicalUTC(engine, "instant") + ` AS instant_key FROM admitted) SELECT record_id,created_at,instant,sort_key FROM ordered WHERE `
	return statement, args
}

type searchCandidate struct {
	id, date string
	seconds  int64
	fraction string
	valid    bool
}
type searchPageHeap struct {
	values   []searchCandidate
	ordering *collate.Collator
}

func (p searchPageHeap) Len() int           { return len(p.values) }
func (p searchPageHeap) Less(i, j int) bool { return p.compare(p.values[i], p.values[j]) < 0 }
func (p searchPageHeap) Swap(i, j int)      { p.values[i], p.values[j] = p.values[j], p.values[i] }
func (p *searchPageHeap) Push(value any)    { p.values = append(p.values, value.(searchCandidate)) }
func (p *searchPageHeap) Pop() any {
	last := p.values[len(p.values)-1]
	p.values = p.values[:len(p.values)-1]
	return last
}
func newSearchCandidate(id, date string) searchCandidate {
	seconds, fraction, err := timestampParts(date)
	return searchCandidate{id: id, date: date, seconds: seconds, fraction: fraction, valid: err == nil}
}
func (p searchPageHeap) compare(a, b searchCandidate) int {
	if a.valid != b.valid {
		if a.valid {
			return 1
		}
		return -1
	}
	if a.valid {
		if a.seconds < b.seconds {
			return -1
		}
		if a.seconds > b.seconds {
			return 1
		}
		width := max(len(a.fraction), len(b.fraction))
		if order := strings.Compare(a.fraction+strings.Repeat("0", width-len(a.fraction)), b.fraction+strings.Repeat("0", width-len(b.fraction))); order != 0 {
			return order
		}
	} else if order := p.ordering.CompareString(a.date, b.date); order != 0 {
		return order
	}
	return p.ordering.CompareString(a.id, b.id)
}
func searchChronologicalKey(date string) string {
	// Historical RFC3339 offsets (and other Go-parseable noncanonical forms)
	// retain the existing comparator semantics through the streaming fallback.
	if !isoTimestampPattern.MatchString(date) {
		return ""
	}
	seconds, fraction, err := timestampParts(date)
	if err != nil {
		return ""
	}
	at := time.UnixMilli(seconds).UTC()
	if at.Year() < 0 || at.Year() > 9999 {
		return ""
	}
	if fraction == "" {
		fraction = strings.TrimRight(fmt.Sprintf("%03d", at.Nanosecond()/int(time.Millisecond)), "0")
	}
	return at.Format("2006-01-02T15:04:05") + "." + fraction
}

func searchSelectedCandidates(ctx context.Context, tx *sql.Tx, engine string, params url.Values, capacity int) ([]map[string]any, error) {
	page := &searchPageHeap{ordering: collate.New(language.English)}
	var cursor *searchCandidate
	if raw := params.Get("cursor"); raw != "" {
		data, err := base64.RawURLEncoding.DecodeString(raw)
		var encoded []json.RawMessage
		var pair []string
		if err != nil || json.Unmarshal(data, &encoded) != nil || len(encoded) != 2 || len(bytes.TrimSpace(encoded[0])) == 0 || bytes.TrimSpace(encoded[0])[0] != '"' || len(bytes.TrimSpace(encoded[1])) == 0 || bytes.TrimSpace(encoded[1])[0] != '"' || json.Unmarshal(data, &pair) != nil {
			return nil, &protocolError{"VALIDATION_FAILED", "Invalid cursor.", 400}
		}
		value := newSearchCandidate(pair[1], pair[0])
		if !value.valid {
			return nil, &protocolError{"VALIDATION_FAILED", "Invalid cursor.", 400}
		}
		cursor = &value
	}
	base, args := searchNativeAdmission(engine, params)
	datePredicate := []string{}
	for _, field := range []string{"from", "to"} {
		if value := params.Get(field); value != "" {
			args = append(args, searchChronologicalKey(value))
			operator := ">="
			if field == "to" {
				operator = "<="
			}
			datePredicate = append(datePredicate, fmt.Sprintf("(instant_key IS NULL OR instant_key %s $%d)", operator, len(args)))
		}
	}
	if len(datePredicate) > 0 {
		base += strings.Join(datePredicate, " AND ") + " AND "
	}
	admit := func(id, date, instant string) {
		for _, field := range []string{"from", "to"} {
			if value := params.Get(field); value != "" {
				order, err := compareInstants(instant, value)
				if err != nil || (field == "from" && order < 0) || (field == "to" && order > 0) {
					return
				}
			}
		}
		candidate := newSearchCandidate(id, date)
		if cursor != nil && page.compare(candidate, *cursor) >= 0 {
			return
		}
		if page.Len() < capacity {
			heap.Push(page, candidate)
		} else if page.compare(candidate, page.values[0]) > 0 {
			page.values[0] = candidate
			heap.Fix(page, 0)
		}
	}
	// Historical offsets and incomplete dates retain the existing Go ordering;
	// streaming this uncommon set retains only the bounded best page.
	rows, err := tx.QueryContext(ctx, base+"sort_key IS NULL", args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, date, instant string
		var key sql.NullString
		if err = rows.Scan(&id, &date, &instant, &key); err != nil {
			rows.Close()
			return nil, err
		}
		admit(id, date, instant)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	binary := "COLLATE BINARY"
	if engine == "postgres" {
		binary = `COLLATE "C"`
	}
	lastKey, lastID := "", ""
	for {
		windowArgs := append([]any{}, args...)
		statement := base + "sort_key IS NOT NULL"
		if cursor != nil {
			if key := searchChronologicalKey(cursor.date); key != "" {
				windowArgs = append(windowArgs, key)
				statement += fmt.Sprintf(" AND sort_key <= $%d", len(windowArgs))
			}
		}
		if lastKey != "" {
			windowArgs = append(windowArgs, lastKey, lastID)
			statement += fmt.Sprintf(" AND (sort_key < $%d OR (sort_key = $%d AND record_id %s < $%d))", len(windowArgs)-1, len(windowArgs)-1, binary, len(windowArgs))
		}
		statement += " ORDER BY sort_key DESC,record_id " + binary + " DESC LIMIT 256"
		rows, err = tx.QueryContext(ctx, statement, windowArgs...)
		if err != nil {
			return nil, err
		}
		count, stop := 0, false
		for rows.Next() {
			var id, date, instant, key string
			if err = rows.Scan(&id, &date, &instant, &key); err != nil {
				rows.Close()
				return nil, err
			}
			count++
			lastKey, lastID = key, id
			if page.Len() == capacity && page.values[0].valid {
				if worst := searchChronologicalKey(page.values[0].date); worst != "" && key < worst {
					stop = true
					break
				}
			}
			admit(id, date, instant)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if stop || count < 256 {
			break
		}
	}
	sort.Slice(page.values, func(i, j int) bool { return page.compare(page.values[i], page.values[j]) > 0 })
	result := []map[string]any{}
	for _, value := range page.values {
		result = append(result, map[string]any{"id": value.id, "createdAt": value.date})
	}
	return result, nil
}

func validateSearchPageHeaders(ctx context.Context, tx *sql.Tx, records []map[string]any) error {
	if len(records) == 0 {
		return nil
	}
	args := []any{}
	places := []string{}
	for _, record := range records {
		args = append(args, record["id"])
		places = append(places, fmt.Sprintf("$%d", len(args)))
	}
	rows, err := tx.QueryContext(ctx, "SELECT data FROM context_headers WHERE record_id IN ("+strings.Join(places, ",")+")", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var header map[string]any
		if err := json.Unmarshal(raw, &header); err != nil {
			return err
		}
		if header == nil {
			return errors.New("invalid search projection")
		}
	}
	return rows.Err()
}
