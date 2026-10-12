package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/alexcatdad/scratchpad/backend/internal/domain"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

var lexicalTerms = regexp.MustCompile(`[\p{L}\p{N}_]+`)

func (s *SSH) Search(post bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		params := r.URL.Query()
		if post {
			var body map[string]any
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 3*1024*1024))
			if decoder.Decode(&body) != nil || body == nil || decoder.Decode(new(any)) != io.EOF {
				s.fail(w, validationProject())
				return
			}
			params = url.Values{}
			if value, exists := body["query"]; exists {
				query, ok := value.(string)
				if !ok || len(utf16.Encode([]rune(query))) > 2000 {
					s.fail(w, validationProject())
					return
				}
				if query != "" {
					params.Set("q", query)
				}
			}
			for _, field := range []string{"projectIds", "types"} {
				if value, exists := body[field]; exists {
					array, ok := value.([]any)
					if !ok {
						s.fail(w, validationProject())
						return
					}
					for _, entry := range array {
						text, ok := entry.(string)
						if !ok {
							s.fail(w, validationProject())
							return
						}
						params.Add(field, text)
					}
				}
			}
			if value, exists := body["limit"]; exists {
				params.Set("limit", fmt.Sprint(value))
			}
			if value, exists := body["cursor"]; exists {
				text, ok := value.(string)
				if !ok {
					s.fail(w, validationProject())
					return
				}
				params.Set("cursor", text)
			}
		}
		if !params.Has("relationshipType") && params.Get("relationship") != "" {
			params.Set("relationshipType", params.Get("relationship"))
		}
		tx, err := s.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			s.fail(w, err)
			return
		}
		defer tx.Rollback()
		if _, err = apiActor(ctx, tx, r, s.Origin); err != nil {
			s.fail(w, err)
			return
		}
		result, err := search(ctx, tx, s.Store.Engine, params)
		if err != nil {
			s.fail(w, err)
			return
		}
		JSON(w, 200, result)
	}
}
func search(ctx context.Context, tx *sql.Tx, engine string, params url.Values) (map[string]any, error) {
	limit := 30
	if raw, exists := params["limit"]; exists {
		value, err := strconv.ParseFloat(raw[0], 64)
		if err != nil || value < 1 || value > 100 || value != float64(int(value)) {
			return nil, validationProject()
		}
		limit = int(value)
	}
	records, err := searchCandidateHeaders(ctx, tx, engine, params)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, stringValue(record, "id"))
	}
	encodedIDs, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	byID := map[string]map[string]any{}
	if params.Get("tag") != "" || params.Get("status") != "" || params.Get("archived") != "" {
		byID, err = searchMetadata(ctx, tx, engine, string(encodedIDs), true)
		if err != nil {
			return nil, err
		}
	}
	links := []map[string]any{}
	if params.Get("status") != "" || params.Get("relationshipType") != "" || params.Get("relatedTo") != "" {
		links, err = searchRelationships(ctx, tx, engine, string(encodedIDs))
		if err != nil {
			return nil, err
		}
	}
	for _, field := range []string{"from", "to"} {
		if value := params.Get(field); value != "" {
			if !isoTimestampPattern.MatchString(value) {
				return nil, validationProject()
			}
			if _, _, err = timestampParts(value); err != nil {
				return nil, validationProject()
			}
		}
	}
	filtered := []map[string]any{}
	for _, record := range records {
		if !searchFilter(record, byID[stringValue(record, "id")], links, params) {
			continue
		}
		filtered = append(filtered, record)
	}
	ordering := collate.New(language.English)
	sort.Slice(filtered, func(i, j int) bool {
		left, right := filtered[i], filtered[j]
		if left["createdAt"] != right["createdAt"] {
			return ordering.CompareString(stringValue(left, "createdAt"), stringValue(right, "createdAt")) > 0
		}
		return ordering.CompareString(stringValue(left, "id"), stringValue(right, "id")) > 0
	})
	if cursor := params.Get("cursor"); cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return nil, &protocolError{"VALIDATION_FAILED", "Invalid cursor.", 400}
		}
		var encoded []json.RawMessage
		var pair []string
		if json.Unmarshal(raw, &encoded) != nil || len(encoded) != 2 || len(bytes.TrimSpace(encoded[0])) == 0 || bytes.TrimSpace(encoded[0])[0] != '"' || len(bytes.TrimSpace(encoded[1])) == 0 || bytes.TrimSpace(encoded[1])[0] != '"' || json.Unmarshal(raw, &pair) != nil {
			return nil, &protocolError{"VALIDATION_FAILED", "Invalid cursor.", 400}
		}
		selected := []map[string]any{}
		for _, record := range filtered {
			date, id := stringValue(record, "createdAt"), stringValue(record, "id")
			dateOrder := ordering.CompareString(date, pair[0])
			if dateOrder < 0 || (dateOrder == 0 && ordering.CompareString(id, pair[1]) < 0) {
				selected = append(selected, record)
			}
		}
		filtered = selected
	}
	var next any
	if len(filtered) > limit {
		last := filtered[limit-1]
		data, _ := json.Marshal([]string{stringValue(last, "createdAt"), stringValue(last, "id")})
		next = base64.RawURLEncoding.EncodeToString(data)
		filtered = filtered[:limit]
	}
	if err := hydrateContextRecords(ctx, tx, filtered); err != nil {
		return nil, err
	}
	pageIDs := []string{}
	for _, record := range filtered {
		pageIDs = append(pageIDs, stringValue(record, "id"))
	}
	encodedPage, err := json.Marshal(pageIDs)
	if err != nil {
		return nil, err
	}
	byID, err = searchMetadata(ctx, tx, engine, string(encodedPage), false)
	if err != nil {
		return nil, err
	}
	for _, record := range filtered {
		if item := byID[stringValue(record, "id")]; item != nil {
			record["metadata"] = item
		}
	}
	return map[string]any{"records": filtered, "nextCursor": next}, nil
}

func searchCandidateHeaders(ctx context.Context, tx *sql.Tx, engine string, params url.Values) ([]map[string]any, error) {
	fields := []string{}
	for _, field := range []string{"authority", "confidence"} {
		if params.Get(field) != "" {
			fields = append(fields, field)
		}
	}
	if params.Get("branch") != "" || params.Get("gitPath") != "" {
		fields = append(fields, "gitContext")
	}
	if params.Get("source") != "" {
		fields = append(fields, "actor", "provenance")
	}
	projection, join := "'[]'", ""
	if len(fields) > 0 {
		parts := []string{}
		for _, field := range fields {
			expression := "json_extract(r.data,'$." + field + "')"
			if engine == "postgres" {
				expression = "r.data->'" + field + "'"
			}
			parts = append(parts, expression)
		}
		projection = "json_array(" + strings.Join(parts, ",") + ")"
		if engine == "postgres" {
			projection = "jsonb_build_array(" + strings.Join(parts, ",") + ")"
		}
		join = " JOIN entities r ON r.kind='record' AND r.id=h.record_id"
	}
	where, args := []string{}, []any{}
	for _, filter := range []struct{ parameter, column string }{{"projectId", "h.project_id"}, {"projectIds", "h.project_id"}, {"type", "json_extract(h.data,'$.type')"}, {"types", "json_extract(h.data,'$.type')"}} {
		column := filter.column
		if engine == "postgres" && strings.HasPrefix(column, "json_extract") {
			column = "h.data->>'type'"
		}
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
		placeholders := []string{}
		for _, value := range values {
			args = append(args, value)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		where = append(where, column+" IN ("+strings.Join(placeholders, ",")+")")
	}
	if query := params.Get("q"); query != "" {
		terms := lexicalTerms.FindAllString(query, 30)
		if len(terms) == 0 {
			return []map[string]any{}, nil
		}
		parts := []string{}
		for _, term := range terms {
			parts = append(parts, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
		}
		argument := strings.Join(parts, " AND ")
		join += " JOIN record_search ON record_search.record_id=h.record_id"
		args = append(args, argument)
		predicate := fmt.Sprintf("record_search MATCH $%d", len(args))
		if engine == "postgres" {
			args[len(args)-1] = strings.Join(terms, " ")
			predicate = fmt.Sprintf("record_search.document @@ plainto_tsquery('simple',$%d)", len(args))
		}
		where = append(where, predicate)
	}
	statement := "SELECT DISTINCT h.record_id,h.data," + projection + " FROM context_headers h" + join
	if len(where) > 0 {
		statement += " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []map[string]any{}
	for rows.Next() {
		var id string
		var raw, extra []byte
		if err := rows.Scan(&id, &raw, &extra); err != nil {
			return nil, err
		}
		var record map[string]any
		var values []any
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(extra, &values); err != nil {
			return nil, err
		}
		// The table identity also lets page hydration surface corruption in a
		// selected raw body instead of silently dropping its header.
		record["id"] = id
		for index, field := range fields {
			record[field] = values[index]
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func searchRelationships(ctx context.Context, tx *sql.Tx, engine, encodedIDs string) ([]map[string]any, error) {
	from, to := "json_extract(data,'$.fromRecordId')", "json_extract(data,'$.toRecordId')"
	ids := "SELECT value FROM json_each($1)"
	if engine == "postgres" {
		from, to = "data->>'fromRecordId'", "data->>'toRecordId'"
		ids = "SELECT jsonb_array_elements_text($1::jsonb)"
	}
	rows, err := tx.QueryContext(ctx, "SELECT data FROM entities WHERE kind='relationship' AND ("+from+" IN ("+ids+") OR "+to+" IN ("+ids+"))", encodedIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var link map[string]any
		if err := json.Unmarshal(raw, &link); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

func searchMetadata(ctx context.Context, tx *sql.Tx, engine, encodedIDs string, filterOnly bool) (map[string]map[string]any, error) {
	projection := "data"
	ids := "SELECT value FROM json_each($1)"
	if engine == "postgres" {
		ids = "SELECT jsonb_array_elements_text($1::jsonb)"
	}
	if filterOnly {
		projection = "json_array(json_extract(data,'$.tags'),json_extract(data,'$.archived'),json_extract(data,'$.legacyStatus'))"
		if engine == "postgres" {
			projection = "jsonb_build_array(data->'tags',data->'archived',data->'legacyStatus')"
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,"+projection+" FROM entities WHERE kind='metadata' AND id IN ("+ids+")", encodedIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]map[string]any{}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var item map[string]any
		if filterOnly {
			var fields []any
			if err := json.Unmarshal(raw, &fields); err != nil {
				return nil, err
			}
			item = map[string]any{"id": id, "tags": fields[0], "archived": fields[1], "legacyStatus": fields[2]}
		} else {
			if err := json.Unmarshal(raw, &item); err != nil {
				return nil, err
			}
		}
		byID[stringValue(item, "id")] = item
	}
	return byID, rows.Err()
}

func containsString(values any, target any) bool {
	switch array := values.(type) {
	case []any:
		for _, value := range array {
			if value == target {
				return true
			}
		}
	case []string:
		for _, value := range array {
			if value == target {
				return true
			}
		}
	}
	return false
}
func objectValue(object map[string]any, key string) map[string]any {
	value, _ := object[key].(map[string]any)
	return value
}
func truthy(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case float64:
		return value != 0
	case json.Number:
		return !EqualJSONNumbers(value, json.Number("0"))
	default:
		return true
	}
}
func lifecycle(record, metadata map[string]any, links []map[string]any) any {
	curated := domain.MetadataFrom(metadata)
	if curated.Archived {
		return "archived"
	}
	partial := false
	for _, link := range links {
		edge := domain.RelationshipFrom(link)
		if edge.Status == "accepted" && edge.ToRecordID == record["id"] {
			if edge.Type == "replaces" {
				return "superseded"
			}
			if edge.Type == "partially_replaces" {
				partial = true
			}
		}
	}
	if partial {
		return "partially_superseded"
	}
	if curated.HasLegacyStatus && curated.LegacyStatus != nil {
		return curated.LegacyStatus
	}
	return "active"
}

var isoTimestampPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}(?::[0-9]{2}(?:\.[0-9]+)?)?Z$`)
var preciseTimestamp = regexp.MustCompile(`^(.*?)(?:\.(\d+))?Z$`)

func timestampParts(value string) (int64, string, error) {
	fraction := ""
	if match := preciseTimestamp.FindStringSubmatch(value); match != nil {
		value = match[1] + "Z"
		fraction = strings.TrimRight(match[2], "0")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		parsed, err = time.Parse("2006-01-02T15:04Z", value)
	}
	return parsed.UnixMilli(), fraction, err
}
func compareInstants(left, right string) (int, error) {
	a, x, err := timestampParts(left)
	if err != nil {
		return 0, err
	}
	b, y, err := timestampParts(right)
	if err != nil {
		return 0, err
	}
	if a < b {
		return -1, nil
	}
	if a > b {
		return 1, nil
	}
	length := len(x)
	if len(y) > length {
		length = len(y)
	}
	x += strings.Repeat("0", length-len(x))
	y += strings.Repeat("0", length-len(y))
	return strings.Compare(x, y), nil
}
func searchFilter(record, metadata map[string]any, links []map[string]any, params url.Values) bool {
	for _, field := range []string{"projectId", "type", "authority", "confidence"} {
		if value := params.Get(field); value != "" && record[field] != value {
			return false
		}
	}
	for _, pair := range []struct{ query, field string }{{"projectIds", "projectId"}, {"types", "type"}} {
		values := params[pair.query]
		if len(values) > 0 && !containsString(values, record[pair.field]) {
			return false
		}
	}
	if value := params.Get("tag"); value != "" && !containsString(metadata["tags"], value) {
		return false
	}
	git := objectValue(record, "gitContext")
	if value := params.Get("branch"); value != "" && git["branch"] != value {
		return false
	}
	if value := params.Get("gitPath"); value != "" && git["rootPathHint"] != value {
		return false
	}
	instant := stringValue(record, "recordedAt")
	if value, exists := record["happenedAt"]; exists && value != nil {
		instant, _ = value.(string)
	}
	for _, field := range []string{"from", "to"} {
		if value := params.Get(field); value != "" {
			comparison, err := compareInstants(instant, value)
			if err != nil || (field == "from" && comparison < 0) || (field == "to" && comparison > 0) {
				return false
			}
		}
	}
	if value := params.Get("status"); value != "" && lifecycle(record, metadata, links) != value {
		return false
	}
	relation, related := params.Get("relationshipType"), params.Get("relatedTo")
	if relation != "" || related != "" {
		found := false
		for _, link := range links {
			if link["status"] == "rejected" || (relation != "" && link["type"] != relation) || (related != "" && link["fromRecordId"] != related && link["toRecordId"] != related) {
				continue
			}
			if link["fromRecordId"] == record["id"] || link["toRecordId"] == record["id"] {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	if value := params.Get("source"); value != "" {
		actor := objectValue(record, "actor")
		imported := objectValue(objectValue(record, "provenance"), "import")
		if imported["sourceName"] != value && actor["client"] != value && actor["displayName"] != value {
			return false
		}
	}
	if value := params.Get("archived"); value != "" && truthy(metadata["archived"]) != (value == "true") {
		return false
	}
	return true
}
