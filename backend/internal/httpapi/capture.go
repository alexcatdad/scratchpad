package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

func captureText(value any, max int) (string, error) {
	text, ok := value.(string)
	text = strings.TrimSpace(text)
	if !ok || text == "" || len(utf16.Encode([]rune(text))) > max {
		return "", validationProject()
	}
	return text, nil
}
func captureEnum(value any, allowed []string) (string, error) {
	text, ok := value.(string)
	if ok {
		for _, known := range allowed {
			if text == known {
				return text, nil
			}
		}
	}
	return "", validationProject()
}
func captureRecord(raw json.RawMessage) (map[string]any, json.RawMessage, error) {
	var submitted map[string]json.RawMessage
	if json.Unmarshal(raw, &submitted) != nil || submitted == nil {
		return nil, nil, validationProject()
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil {
		return nil, nil, validationProject()
	}
	record := map[string]any{}
	for _, field := range []string{"type", "authority", "confidence"} {
		allowed := recordTypes
		if field == "authority" {
			allowed = []string{"explicit", "observed", "inferred", "derived", "suggested"}
		}
		if field == "confidence" {
			allowed = []string{"high", "medium", "low", "unknown"}
			if _, exists := fields[field]; !exists {
				fields[field] = "unknown"
			}
		}
		value, err := captureEnum(fields[field], allowed)
		if err != nil {
			return nil, nil, err
		}
		record[field] = value
	}
	title, err := captureText(fields["title"], 500)
	if err != nil {
		return nil, nil, err
	}
	record["title"] = title
	if value, exists := fields["confidenceReason"]; exists {
		record["confidenceReason"], err = captureText(value, 100000)
		if err != nil {
			return nil, nil, err
		}
	}
	if value, exists := fields["happenedAt"]; exists {
		at, ok := value.(string)
		if !ok || !isoTimestampPattern.MatchString(at) {
			return nil, nil, validationProject()
		}
		_, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			_, err = time.Parse("2006-01-02T15:04Z", at)
		}
		if err != nil {
			return nil, nil, validationProject()
		}
		record["happenedAt"] = at
	}
	payload, ok := fields["payload"].(map[string]any)
	if !ok || payload == nil {
		return nil, nil, validationProject()
	}
	record["payload"] = payload
	if value, exists := fields["actor"]; exists {
		source, ok := value.(map[string]any)
		if !ok {
			return nil, nil, validationProject()
		}
		kind, err := captureEnum(source["kind"], []string{"user", "agent", "import", "system"})
		if err != nil {
			return nil, nil, err
		}
		actor := map[string]any{"kind": kind}
		for _, field := range []string{"displayName", "client", "clientVersion"} {
			if value, exists := source[field]; exists {
				actor[field], err = captureText(value, 100000)
				if err != nil {
					return nil, nil, err
				}
			}
		}
		record["actor"] = actor
	}
	return record, submitted["payload"], nil
}

type payloadField struct {
	key     string
	raw     json.RawMessage
	index   uint64
	numeric bool
}

func payloadFields(raw json.RawMessage) ([]payloadField, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, validationProject()
	}
	fields := []payloadField{}
	positions := map[string]int{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key := keyToken.(string)
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return nil, err
		}
		index, err := strconv.ParseUint(key, 10, 32)
		numeric := err == nil && index < 4294967295 && strconv.FormatUint(index, 10) == key
		field := payloadField{key, value, index, numeric}
		if position, exists := positions[key]; exists {
			fields[position] = field
		} else {
			positions[key] = len(fields)
			fields = append(fields, field)
		}
	}
	if _, err = decoder.Token(); err != nil {
		return nil, err
	}
	sort.SliceStable(fields, func(i, j int) bool {
		if fields[i].numeric && fields[j].numeric {
			return fields[i].index < fields[j].index
		}
		return fields[i].numeric && !fields[j].numeric
	})
	return fields, nil
}
func renderCapture(kind string, raw json.RawMessage) (string, error) {
	required := map[string][]string{"decision": {"decision"}, "adr": {"decision"}, "business_decision": {"decision"}, "finding": {"finding"}, "qa": {"question"}, "failure": {"observed"}, "constraint": {"constraint"}, "project_state": {"state"}}
	fields, err := payloadFields(raw)
	if err != nil {
		return "", err
	}
	values := map[string]json.RawMessage{}
	for _, field := range fields {
		values[field.key] = field.raw
	}
	for _, key := range required[kind] {
		var text string
		if json.Unmarshal(values[key], &text) != nil || strings.TrimSpace(text) == "" {
			return "", &protocolError{"VALIDATION_FAILED", "payload." + key + " is required.", 400}
		}
	}
	if kind == "qa" {
		if raw, exists := values["answer"]; exists {
			var answer string
			if json.Unmarshal(raw, &answer) != nil || strings.TrimSpace(answer) == "" {
				return "", &protocolError{"VALIDATION_FAILED", "payload.answer must be nonempty when supplied.", 400}
			}
		}
	}
	parts := []string{}
	for _, field := range fields {
		var text string
		if len(bytes.TrimSpace(field.raw)) > 0 && bytes.TrimSpace(field.raw)[0] == '"' {
			if json.Unmarshal(field.raw, &text) != nil {
				return "", validationProject()
			}
		} else {
			text, err = compactCaptureJSON(field.raw)
			if err != nil {
				return "", err
			}
		}
		parts = append(parts, field.key+": "+text)
	}
	return strings.Join(parts, "\n\n"), nil
}
func mirrorEligible(project, record map[string]any) bool {
	settings := project["settings"].(map[string]any)
	mirror := settings["repoMirroring"].(map[string]any)
	if mirror["enabled"] != true {
		return false
	}
	for _, kind := range mirror["recordTypes"].([]string) {
		if kind == record["type"] {
			return true
		}
	}
	return false
}
func (s *SSH) Capture() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if _, err := apiActor(ctx, s.Store.DB, r, s.Origin); err != nil {
			s.fail(w, err)
			return
		}
		var body map[string]json.RawMessage
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 3*1024*1024))
		if decoder.Decode(&body) != nil || body == nil || decoder.Decode(new(any)) != io.EOF {
			s.fail(w, validationProject())
			return
		}
		var rawID any
		if json.Unmarshal(body["projectId"], &rawID) != nil {
			s.fail(w, validationProject())
			return
		}
		projectID, err := captureText(rawID, 100000)
		if err != nil {
			s.fail(w, err)
			return
		}
		record, payload, err := captureRecord(body["record"])
		if err != nil {
			s.fail(w, err)
			return
		}
		content, renderErr := renderCapture(stringValue(record, "type"), payload)
		var result map[string]any
		key := r.Header.Get("Idempotency-Key")
		if _, exists := r.Header["Idempotency-Key"]; exists && (len(utf16.Encode([]rune(key))) < 1 || len(utf16.Encode([]rune(key))) > 200) {
			s.fail(w, validationProject())
			return
		}
		serialized, err := json.Marshal(map[string]any{"projectId": projectID, "record": record})
		if err != nil {
			s.fail(w, err)
			return
		}
		err = s.Store.Write(ctx, func(conn *sql.Conn) error {
			actor, err := apiActor(ctx, conn, r, s.Origin)
			if err != nil {
				return err
			}
			project, err := projectEntity(ctx, conn, projectID)
			if err != nil {
				return err
			}
			scope := ""
			if key != "" {
				matches := bearerPattern.FindStringSubmatch(r.Header.Get("Authorization"))
				token := ""
				if len(matches) == 2 {
					token = matches[1]
				} else {
					cookie, _ := r.Cookie("scratchpad_session")
					token = cookie.Value
				}
				session, err := entityMap(ctx, conn, "session", tokenID(token))
				if err != nil {
					return err
				}
				scope = "capture:" + stringValue(session, "credentialId")
				var previousRequest, previousResponse string
				err = conn.QueryRowContext(ctx, `SELECT request,response FROM retries WHERE scope=$1 AND key=$2`, scope, key).Scan(&previousRequest, &previousResponse)
				if err == nil {
					var previous, current any
					if json.Unmarshal([]byte(previousRequest), &previous) != nil || json.Unmarshal(serialized, &current) != nil {
						return errors.New("invalid retry")
					}
					if !reflect.DeepEqual(previous, current) {
						return &protocolError{"CONFLICT", "Idempotency key was used for a different capture.", 409}
					}
					if err = json.Unmarshal([]byte(previousResponse), &result); err != nil {
						return err
					}
					result["mirror"] = map[string]any{"eligible": mirrorEligible(project, record)}
					return nil
				}
				if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
			enabled := false
			for _, kind := range project["settings"].(map[string]any)["enabledRecordTypes"].([]string) {
				if kind == record["type"] {
					enabled = true
				}
			}
			if !enabled {
				return &protocolError{"RECORD_TYPE_DISABLED", "This record type is disabled for the project.", 403}
			}
			if renderErr != nil {
				return renderErr
			}
			var git map[string]any
			if raw, exists := body["gitContext"]; exists {
				if json.Unmarshal(raw, &git) != nil || git == nil {
					return validationProject()
				}
			}
			id, err := entityID("rec")
			if err != nil {
				return err
			}
			submittedActor, _ := record["actor"].(map[string]any)
			if submittedActor == nil {
				submittedActor = map[string]any{}
			}
			for key, value := range actor {
				submittedActor[key] = value
			}
			record["actor"] = submittedActor
			record["projectId"], record["content"], record["payloadVersion"], record["recordedAt"] = projectID, content, 1, timestamp(time.Now())
			if git != nil {
				record["gitContext"] = git
			}
			if err = insert(ctx, conn, "record", id, record); err != nil {
				return err
			}
			if err = insert(ctx, conn, "metadata", id, map[string]any{"recordId": id, "tags": []any{}, "archived": false, "updatedAt": timestamp(time.Now())}); err != nil {
				return err
			}
			if _, err = conn.ExecContext(ctx, `INSERT INTO record_search(record_id,title,content) VALUES($1,$2,$3)`, id, record["title"], content); err != nil {
				return err
			}
			if err = apiAudit(ctx, conn, "record.created", "record", id, actor, nil, record); err != nil {
				return err
			}
			result = map[string]any{"record": record, "mirror": map[string]any{"eligible": mirrorEligible(project, record)}}
			if key != "" {
				response, err := json.Marshal(result)
				if err != nil {
					return err
				}
				_, err = conn.ExecContext(ctx, `INSERT INTO retries(scope,key,request,response) VALUES($1,$2,$3,$4)`, scope, key, string(serialized), string(response))
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			s.fail(w, err)
			return
		}
		JSON(w, 201, result)
	}
}

func captureJSONString(text string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, value := range text {
		switch value {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if value < 32 {
				fmt.Fprintf(&out, `\u%04x`, value)
			} else {
				out.WriteRune(value)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}
func compactCaptureJSON(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", validationProject()
	}
	switch raw[0] {
	case '{':
		fields, err := payloadFields(raw)
		if err != nil {
			return "", err
		}
		parts := []string{}
		for _, field := range fields {
			value, err := compactCaptureJSON(field.raw)
			if err != nil {
				return "", err
			}
			parts = append(parts, captureJSONString(field.key)+":"+value)
		}
		return "{" + strings.Join(parts, ",") + "}", nil
	case '[':
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return "", validationProject()
		}
		parts := []string{}
		for _, raw := range values {
			value, err := compactCaptureJSON(raw)
			if err != nil {
				return "", err
			}
			parts = append(parts, value)
		}
		return "[" + strings.Join(parts, ",") + "]", nil
	case '"':
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return "", validationProject()
		}
		return captureJSONString(text), nil
	default:
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return "", validationProject()
		}
		if number, ok := value.(float64); ok && number == 0 {
			value = float64(0)
		}
		data, err := json.Marshal(value)
		return string(data), err
	}
}
