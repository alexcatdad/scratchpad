package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

var legacyDateTimePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}(?::[0-9]{2}(?:\.[0-9]+)?)?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])$`)

func legacyReadable(value any) string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			return typed
		}
	case []any:
		parts := []string{}
		for _, item := range typed {
			if text := legacyReadable(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// Match the established canonical JSON identity, including locale sorting and
// JavaScript property enumeration. Raw lines remain separately immutable.
func legacyCanonical(raw json.RawMessage, order *collate.Collator) (string, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return "", importInvalid()
	}
	switch raw[0] {
	case '{':
		fields, err := payloadFields(raw)
		if err != nil {
			return "", err
		}
		sort.SliceStable(fields, func(i, j int) bool { return order.CompareString(fields[i].key, fields[j].key) < 0 })
		parts := []string{}
		for _, field := range fields {
			value, err := legacyCanonical(field.raw, order)
			if err != nil {
				return "", err
			}
			parts = append(parts, captureJSONString(field.key)+":"+value)
		}
		return "{" + strings.Join(parts, ",") + "}", nil
	case '[':
		var array []json.RawMessage
		if json.Unmarshal(raw, &array) != nil {
			return "", importInvalid()
		}
		parts := []string{}
		for _, entry := range array {
			value, err := legacyCanonical(entry, order)
			if err != nil {
				return "", err
			}
			parts = append(parts, value)
		}
		return "[" + strings.Join(parts, ",") + "]", nil
	default:
		return compactCaptureJSON(raw)
	}
}
func legacyDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func importLegacy(ctx context.Context, conn *sql.Conn, body, actor map[string]any) (map[string]any, error) {
	projectID, ok := body["projectId"].(string)
	if !ok || !portableID(projectID) {
		return nil, importInvalid()
	}
	if err := portableEntity(ctx, conn, "project", projectID); err != nil {
		return nil, err
	}
	sourceName := body["sourceName"]
	if sourceName == nil {
		if source, ok := body["source"].(map[string]any); ok {
			sourceName = source["filename"]
		}
	}
	if sourceName == nil {
		sourceName = "legacy.jsonl"
	}
	name, ok := sourceName.(string)
	if !ok || name == "" || len(utf16.Encode([]rune(name))) > 1000 {
		return nil, importInvalid()
	}
	text, ok := body["jsonl"].(string)
	if !ok || len(utf16.Encode([]rune(text))) > 32*1024*1024 {
		return nil, importInvalid()
	}
	warnings, results := []any{}, []any{}
	warn := func(line int, code string, details map[string]any) {
		warning := map[string]any{"record": line, "code": code}
		if details != nil {
			warning["details"] = details
		}
		warnings = append(warnings, warning)
	}
	imported, skipped := 0, 0
	occurrences := map[string]int{}
	type pendingRecord struct {
		line     int
		recordID string
		source   map[string]any
	}
	pending := []pendingRecord{}
	order := collate.New(language.English)
	lines := strings.Split(text, "\n")
	for offset, rawLine := range lines {
		line := offset + 1
		if offset < len(lines)-1 {
			rawLine = strings.TrimSuffix(rawLine, "\r")
		}
		if strings.TrimSpace(rawLine) == "" {
			continue
		}
		var original map[string]any
		if DecodeJSONNumbers([]byte(rawLine), &original) != nil || original == nil {
			warn(line, "INVALID_JSON", nil)
			results = append(results, map[string]any{"line": line, "status": "skipped"})
			skipped++
			continue
		}
		canonical, err := legacyCanonical(json.RawMessage(rawLine), order)
		if err != nil {
			return nil, err
		}
		fingerprint := legacyDigest(canonical)
		originalID := ""
		if id, ok := original["id"].(string); ok && strings.TrimSpace(id) != "" {
			originalID = id
		}
		occurrence := occurrences[fingerprint]
		occurrences[fingerprint] = occurrence + 1
		identity := fingerprint
		if originalID != "" {
			identity = originalID
			occurrence = 0
		}
		encoded, _ := json.Marshal(map[string]any{"projectId": projectID, "sourceName": name, "identity": identity, "occurrence": occurrence})
		identityJSON, err := legacyCanonical(encoded, order)
		if err != nil {
			return nil, err
		}
		deterministic := "rec_import_" + legacyDigest(identityJSON)[:32]
		recordID := deterministic
		if originalID != "" && legacyHistoricalID(originalID) {
			recordID = originalID
		}
		previous, err := optional(ctx, conn, "record", recordID)
		if err != nil {
			return nil, err
		}
		var existing map[string]any
		if previous != nil {
			if DecodeJSONNumbers(previous, &existing) != nil {
				return nil, importInvalid()
			}
		}
		if existing != nil && existing["projectId"] != projectID {
			details := map[string]any{}
			if originalID != "" {
				details["originalId"] = originalID
			}
			warn(line, "ID_COLLISION", details)
			recordID = deterministic
			previous, err = optional(ctx, conn, "record", recordID)
			if err != nil {
				return nil, err
			}
			existing = nil
			if previous != nil {
				if DecodeJSONNumbers(previous, &existing) != nil {
					return nil, importInvalid()
				}
			}
		}
		if existing != nil {
			legacy := objectValue(existing, "payload")["legacyOriginal"]
			matches := false
			if legacy != nil {
				encoded, err := json.Marshal(legacy)
				if err != nil {
					return nil, err
				}
				prior, err := legacyCanonical(encoded, order)
				if err != nil {
					return nil, err
				}
				matches = prior == canonical
			}
			status := "skipped"
			if !matches {
				status = "conflict"
				warn(line, "IMPORT_ID_CONFLICT", map[string]any{"recordId": recordID})
			}
			skipped++
			results = append(results, map[string]any{"line": line, "recordId": recordID, "status": status})
			if matches {
				pending = append(pending, pendingRecord{line, recordID, original})
			}
			continue
		}
		if originalID == "" {
			warn(line, "MISSING_ID", nil)
		} else if recordID != originalID {
			warn(line, "ID_NOT_PRESERVED", map[string]any{"originalId": originalID, "recordId": recordID})
		}
		requested := original["type"]
		if requested == nil {
			requested = original["recordType"]
		}
		if requested == nil {
			requested = "decision"
		}
		kind, ok := requested.(string)
		if !ok || !contains(recordTypes, kind) {
			kind = "decision"
			warn(line, "UNSUPPORTED_RECORD_TYPE", map[string]any{"originalType": requested})
		}
		payload := map[string]any{}
		if submitted, ok := original["payload"].(map[string]any); ok {
			for key, value := range submitted {
				payload[key] = value
			}
		}
		primary := map[string]string{"decision": "decision", "adr": "decision", "business_decision": "decision", "finding": "finding", "failure": "observed", "constraint": "constraint", "project_state": "state", "qa": "question"}[kind]
		main := ""
		for _, key := range []string{"decision", "content", "summary", "bodyMarkdown"} {
			if main = legacyReadable(original[key]); main != "" {
				break
			}
		}
		if _, exists := payload[primary]; !exists {
			value := legacyReadable(original[primary])
			if value == "" {
				value = main
			}
			if value != "" {
				payload[primary] = value
			}
		}
		for _, key := range []string{"question", "answer", "rationale", "expected", "observed", "cause", "resolution", "lesson", "reason", "scope", "previousState", "followUp", "requestedBy", "businessContext", "expectedOutcome", "context", "environment", "alternatives", "consequences", "limitations"} {
			if _, exists := payload[key]; !exists {
				if value, exists := original[key]; exists {
					payload[key] = value
				}
			}
		}
		if _, exists := payload["rationale"]; !exists {
			value := legacyReadable(original["why"])
			if value == "" {
				value = legacyReadable(original["reason"])
			}
			if value != "" {
				payload["rationale"] = value
			}
		}
		if _, exists := payload["consequences"]; !exists {
			if value, exists := original["implications"]; exists {
				payload["consequences"] = value
			}
		}
		if legacyReadable(payload[primary]) == "" {
			warn(line, "MISSING_CONTENT", nil)
		}
		if contains([]string{"decision", "adr", "business_decision"}, kind) && legacyReadable(payload["rationale"]) == "" {
			warn(line, "MISSING_RATIONALE", nil)
		}
		var authority any
		if value, ok := original["authority"].(string); ok && contains([]string{"explicit", "observed", "inferred", "derived", "suggested"}, value) {
			authority = value
		} else {
			warn(line, "UNVERIFIED_LEGACY_PROVENANCE", nil)
		}
		confidence := "unknown"
		if value, ok := original["confidence"].(string); ok && contains([]string{"high", "medium", "low", "unknown"}, value) {
			confidence = value
		}
		sourceDate := original["happenedAt"]
		if sourceDate == nil {
			sourceDate = original["date"]
		}
		if sourceDate == nil {
			sourceDate = original["timestamp"]
		}
		happenedAt := ""
		if value, ok := sourceDate.(string); ok {
			happenedAt = legacyDate(value)
			if happenedAt == "" {
				warn(line, "UNSUPPORTED_DATE", map[string]any{"value": value})
			}
		} else {
			warn(line, "MISSING_DATE", nil)
		}
		title, ok := original["title"].(string)
		if !ok || strings.TrimSpace(title) == "" {
			prose := legacyReadable(payload[primary])
			if prose == "" {
				title = "Imported record " + strconv.Itoa(line)
			} else {
				title = strings.Split(prose, "\n")[0]
			}
		}
		units := utf16.Encode([]rune(title))
		if len(units) > 500 {
			title = string(utf16.Decode(units[:500]))
		}
		fields, err := payloadFields(json.RawMessage(rawLine))
		if err != nil {
			return nil, err
		}
		parts := []string{}
		for _, field := range fields {
			value := legacyReadable(original[field.key])
			if value == "" {
				value, err = compactCaptureJSON(field.raw)
				if err != nil {
					return nil, err
				}
			}
			parts = append(parts, field.key+": "+value)
		}
		content := strings.Join(parts, "\n\n")
		payload["legacyOriginal"] = original
		importActor := map[string]any{}
		for key, value := range actor {
			importActor[key] = value
		}
		importActor["kind"] = "import"
		recordActor := map[string]any{}
		for key, value := range importActor {
			recordActor[key] = value
		}
		recordActor["displayName"] = name
		precision := "unknown"
		if value, ok := sourceDate.(string); ok && len(value) == 10 {
			precision = "day"
		} else if happenedAt != "" {
			precision = "instant"
		}
		provenance := map[string]any{"format": "jsonl", "sourceName": name, "line": line, "rawLine": rawLine, "sha256": fingerprint, "datePrecision": precision, "authorityVerified": false}
		if originalID != "" {
			provenance["originalId"] = originalID
		}
		if sourceDate != nil {
			provenance["sourceDate"] = sourceDate
		}
		for _, pair := range [][2]string{{"actor", "sourceActor"}, {"source", "source"}} {
			if value, exists := original[pair[0]]; exists {
				provenance[pair[1]] = value
			}
		}
		record := map[string]any{"projectId": projectID, "type": kind, "title": title, "content": content, "payload": payload, "payloadVersion": 1, "authority": authority, "confidence": confidence, "recordedAt": timestamp(time.Now()), "actor": recordActor, "provenance": map[string]any{"import": provenance}}
		if happenedAt != "" {
			record["happenedAt"] = happenedAt
		}
		if git, ok := original["gitContext"].(map[string]any); ok {
			record["gitContext"] = git
		}
		if err = insert(ctx, conn, "record", recordID, record); err != nil {
			return nil, err
		}
		metadata := map[string]any{"recordId": recordID, "tags": []any{}, "archived": false, "updatedAt": timestamp(time.Now())}
		if status, ok := original["status"].(string); ok {
			metadata["legacyStatus"] = status
		}
		if err = insert(ctx, conn, "metadata", recordID, metadata); err != nil {
			return nil, err
		}
		if _, err = conn.ExecContext(ctx, `INSERT INTO record_search(record_id,title,content) VALUES($1,$2,$3)`, recordID, title, content); err != nil {
			return nil, err
		}
		if err = apiAudit(ctx, conn, "record.imported", "record", recordID, importActor, nil, record); err != nil {
			return nil, err
		}
		if value, exists := original["evidence"]; exists {
			id, err := entityID("evi")
			if err != nil {
				return nil, err
			}
			if err = insert(ctx, conn, "evidence", id, map[string]any{"recordId": recordID, "kind": "other", "reference": name + ":" + strconv.Itoa(line), "description": "Unverified evidence preserved from the imported source.", "sourceValue": value}); err != nil {
				return nil, err
			}
		}
		imported++
		results = append(results, map[string]any{"line": line, "recordId": recordID, "status": "imported"})
		pending = append(pending, pendingRecord{line, recordID, original})
	}
	ids := map[string]string{}
	for _, item := range pending {
		if id, ok := item.source["id"].(string); ok {
			ids[id] = item.recordID
		}
	}
	importActor := map[string]any{}
	for key, value := range actor {
		importActor[key] = value
	}
	importActor["kind"] = "import"
	for _, item := range pending {
		nested := objectValue(item.source, "supersession")
		if truthy(item.source["supersession"]) && !truthy(nested["supersededBy"]) {
			warn(item.line, "SUPERSESSION_REQUIRES_REVIEW", nil)
		}
		forward, hasForward := item.source["supersedes"]
		reverse, hasReverse := item.source["supersededBy"]
		if reverse == nil {
			reverse, hasReverse = item.source["superseded_by"]
		}
		if reverse == nil {
			reverse, hasReverse = nested["supersededBy"]
		}
		for _, ref := range []struct {
			value            any
			present, reverse bool
		}{{forward, hasForward, false}, {reverse, hasReverse, true}} {
			if !ref.present {
				continue
			}
			references, ok := ref.value.([]any)
			if !ok {
				references = []any{ref.value}
			}
			for _, reference := range references {
				target, ok := reference.(string)
				if mapped, exists := ids[target]; exists && ok {
					target = mapped
				}
				var targetRecord map[string]any
				if ok && target != "" {
					raw, err := optional(ctx, conn, "record", target)
					if err != nil {
						return nil, err
					}
					if raw != nil {
						if json.Unmarshal(raw, &targetRecord) != nil {
							return nil, importInvalid()
						}
					}
				}
				if targetRecord == nil || targetRecord["projectId"] != projectID {
					warn(item.line, "UNRESOLVED_RELATIONSHIP", map[string]any{"reference": reference})
					continue
				}
				from, to := item.recordID, target
				if ref.reverse {
					from, to = target, item.recordID
				}
				if from == to {
					warn(item.line, "INVALID_RELATIONSHIP", nil)
					continue
				}
				encoded, _ := json.Marshal(map[string]any{"fromRecordId": from, "toRecordId": to, "type": "replaces"})
				canonical, err := legacyCanonical(encoded, order)
				if err != nil {
					return nil, err
				}
				id := "rel_import_" + legacyDigest(canonical)[:32]
				existing, err := optional(ctx, conn, "relationship", id)
				if err != nil {
					return nil, err
				}
				if existing != nil {
					continue
				}
				link := map[string]any{"fromRecordId": from, "toRecordId": to, "type": "replaces", "authority": "suggested", "status": "suggested", "note": "Imported explicit reference; historical applicability requires review.", "provenance": map[string]any{"sourceName": name, "line": item.line}}
				if err = insert(ctx, conn, "relationship", id, link); err != nil {
					return nil, err
				}
				if err = apiAudit(ctx, conn, "relationship.imported", "relationship", id, importActor, nil, link); err != nil {
					return nil, err
				}
			}
		}
	}
	id, err := entityID("import")
	if err != nil {
		return nil, err
	}
	if err = apiAudit(ctx, conn, "data.imported", "import", id, actor, nil, map[string]any{"format": "jsonl", "sourceName": name, "imported": imported, "skipped": skipped}); err != nil {
		return nil, err
	}
	return map[string]any{"imported": imported, "skipped": skipped, "warnings": warnings, "records": results}, nil
}

func legacyHistoricalID(id string) bool {
	if len(id) > 200 || len(id) == 0 || !((id[0] >= 'A' && id[0] <= 'Z') || (id[0] >= 'a' && id[0] <= 'z') || (id[0] >= '0' && id[0] <= '9')) {
		return false
	}
	return portableID(id) && !strings.ContainsAny(id, "~")
}
func legacyDate(value string) string {
	if len(value) == 10 {
		if date, err := time.Parse("2006-01-02", value); err == nil {
			return timestamp(date)
		}
	}
	if !legacyDateTimePattern.MatchString(value) {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00"} {
		if date, err := time.Parse(layout, value); err == nil {
			return timestamp(date)
		}
	}
	return ""
}
