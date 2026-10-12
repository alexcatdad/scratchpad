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
	records := []map[string]any{}
	var err error
	query := params.Get("q")
	if query == "" {
		records, err = entityList(ctx, tx, "record")
		if err != nil {
			return nil, err
		}
	} else {
		terms := lexicalTerms.FindAllString(query, 30)
		if len(terms) > 0 {
			statement := `SELECT DISTINCT r.data FROM record_search JOIN entities r ON r.kind='record' AND r.id=record_search.record_id WHERE record_search MATCH $1`
			parts := []string{}
			for _, term := range terms {
				parts = append(parts, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
			}
			argument := strings.Join(parts, " AND ")
			if engine == "postgres" {
				statement = `SELECT DISTINCT r.data FROM record_search JOIN entities r ON r.kind='record' AND r.id=record_search.record_id WHERE document @@ plainto_tsquery('simple',$1)`
				argument = strings.Join(terms, " ")
			}
			rows, err := tx.QueryContext(ctx, statement, argument)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var raw []byte
				if err = rows.Scan(&raw); err != nil {
					rows.Close()
					return nil, err
				}
				var record map[string]any
				if err = json.Unmarshal(raw, &record); err != nil {
					rows.Close()
					return nil, err
				}
				records = append(records, record)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, stringValue(record, "id"))
	}
	encodedIDs, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	metadataStatement := `SELECT data FROM entities WHERE kind='metadata' AND id IN (SELECT value FROM json_each($1))`
	if engine == "postgres" {
		metadataStatement = `SELECT data FROM entities WHERE kind='metadata' AND id IN (SELECT jsonb_array_elements_text($1::jsonb))`
	}
	metadataRows, err := tx.QueryContext(ctx, metadataStatement, string(encodedIDs))
	if err != nil {
		return nil, err
	}
	byID := map[string]map[string]any{}
	for metadataRows.Next() {
		var raw []byte
		if err = metadataRows.Scan(&raw); err != nil {
			metadataRows.Close()
			return nil, err
		}
		var item map[string]any
		if err = json.Unmarshal(raw, &item); err != nil {
			metadataRows.Close()
			return nil, err
		}
		byID[stringValue(item, "id")] = item
	}
	err = metadataRows.Err()
	metadataRows.Close()
	if err != nil {
		return nil, err
	}
	links, err := entityList(ctx, tx, "relationship")
	if err != nil {
		return nil, err
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
			if date < pair[0] || (date == pair[0] && id < pair[1]) {
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
	for _, record := range filtered {
		if item := byID[stringValue(record, "id")]; item != nil {
			record["metadata"] = item
		}
	}
	return map[string]any{"records": filtered, "nextCursor": next}, nil
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
	default:
		return true
	}
}
func lifecycle(record, metadata map[string]any, links []map[string]any) any {
	if truthy(metadata["archived"]) {
		return "archived"
	}
	partial := false
	for _, link := range links {
		if link["status"] == "accepted" && link["toRecordId"] == record["id"] {
			if link["type"] == "replaces" {
				return "superseded"
			}
			if link["type"] == "partially_replaces" {
				partial = true
			}
		}
	}
	if partial {
		return "partially_superseded"
	}
	if value, exists := metadata["legacyStatus"]; exists && value != nil {
		return value
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
	relation, related := params.Get("relationship"), params.Get("relatedTo")
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
