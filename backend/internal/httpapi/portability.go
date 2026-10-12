package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var knowledgeKinds = []string{"project", "source", "record", "metadata", "revision", "relationship", "evidence", "mirror", "audit", "settings", "profile", "ai_artifact", "curated_artifact"}

var portableIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:~-]{1,500}$`)

func portableID(value any) bool {
	id, ok := value.(string)
	return ok && id != "." && id != ".." && portableIDPattern.MatchString(id)
}
func importInvalid() error {
	return &protocolError{"IMPORT_INVALID", "The knowledge archive is invalid.", 400}
}

func (s *SSH) Import() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		if _, err := apiActor(ctx, s.Store.DB, r, s.Origin); err != nil {
			s.fail(w, err)
			return
		}
		cancel()
		// Authenticated archives may arrive over constrained remote connections.
		// Bound upload time separately from the ordinary request and transaction.
		controller := http.NewResponseController(w)
		uploadDeadline := time.Now().Add(5 * time.Minute)
		if controller.SetReadDeadline(uploadDeadline) != nil || controller.SetWriteDeadline(uploadDeadline.Add(30*time.Second)) != nil {
			s.fail(w, importInvalid())
			return
		}
		// Bound the stream before parsing. JSON decoding must not postpone the
		// byte-limit error while scanning a large string or whitespace suffix.
		payload, readErr := io.ReadAll(io.LimitReader(r.Body, 64*1024*1024+1))
		if controller.SetReadDeadline(time.Time{}) != nil || controller.SetWriteDeadline(time.Now().Add(30*time.Second)) != nil {
			s.fail(w, importInvalid())
			return
		}
		ctx, cancel = context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		if readErr != nil {
			s.fail(w, importInvalid())
			return
		}
		if len(payload) > 64*1024*1024 {
			Error(w, 413, "VALIDATION_FAILED", "Request exceeds 64 MiB.")
			return
		}
		var body map[string]any
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.UseNumber()
		decodeErr := decoder.Decode(&body)
		if decodeErr == nil {
			trailing := decoder.Decode(new(any))
			if trailing != io.EOF {
				decodeErr = trailing
				if decodeErr == nil {
					decodeErr = importInvalid()
				}
			}
		}
		if decodeErr != nil || body == nil {
			s.fail(w, importInvalid())
			return
		}
		if body["format"] == "jsonl" {
			var result map[string]any
			err := s.Store.Write(ctx, func(conn *sql.Conn) error {
				actor, err := apiActor(ctx, conn, r, s.Origin)
				if err != nil {
					return err
				}
				result, err = importLegacy(ctx, conn, body, actor)
				return err
			})
			if err != nil {
				s.fail(w, err)
				return
			}
			JSON(w, 200, result)
			return
		}
		if body["format"] != "scratchpad" || !EqualJSONNumbers(body["version"], 1) {
			s.fail(w, importInvalid())
			return
		}
		data, ok := body["data"].(map[string]any)
		if !ok {
			s.fail(w, importInvalid())
			return
		}
		for kind := range data {
			if !contains(knowledgeKinds, kind) {
				s.fail(w, importInvalid())
				return
			}
		}
		var result map[string]any
		err := s.Store.Write(ctx, func(conn *sql.Conn) error {
			actor, err := apiActor(ctx, conn, r, s.Origin)
			if err != nil {
				return err
			}
			imported, skipped := 0, 0
			var sources *portableSources
			for _, kind := range knowledgeKinds {
				entries := []any{}
				if value, exists := data[kind]; exists && value != nil {
					var ok bool
					entries, ok = value.([]any)
					if !ok {
						return importInvalid()
					}
				}
				for _, entry := range entries {
					value, ok := entry.(map[string]any)
					if !ok {
						return importInvalid()
					}
					if err := portableReferences(kind, value); err != nil {
						return err
					}
					id := stringValue(value, "id")
					previous, err := optional(ctx, conn, kind, id)
					if err != nil {
						return err
					}
					if previous != nil {
						var stored map[string]any
						if DecodeJSONNumbers(previous, &stored) != nil {
							return importInvalid()
						}
						if !EqualJSONNumbers(stored, value) {
							// A native export projects operational configuration out of
							// settings/audits. Compare that exact portable view without
							// replacing the existing immutable private history.
							portable, err := portableEntityJSON(kind, previous)
							var projected map[string]any
							if err != nil || DecodeJSONNumbers(portable, &projected) != nil {
								return importInvalid()
							}
							if !EqualJSONNumbers(projected, value) {
								return &protocolError{"CONFLICT", "Import conflicts with existing knowledge.", 409}
							}
						}
						if kind != "ai_artifact" && kind != "curated_artifact" {
							skipped++
							continue
						}
					}
					if (kind == "ai_artifact" || kind == "curated_artifact") && sources == nil {
						sources, err = loadPortableSources(ctx, conn)
						if err != nil {
							return err
						}
					}
					if err := validatePortable(ctx, conn, kind, value, sources); err != nil {
						return err
					}
					if previous != nil {
						skipped++
						continue
					}
					encoded, err := json.Marshal(value)
					if err != nil {
						return err
					}
					version, _ := portableVersion(value["version"])
					if _, err = conn.ExecContext(ctx, `INSERT INTO entities(kind,id,data,version,created_at) VALUES($1,$2,$3,$4,$5)`, kind, id, string(encoded), version, value["createdAt"]); err != nil {
						return err
					}
					if kind == "record" {
						if _, err = conn.ExecContext(ctx, `INSERT INTO record_search(record_id,title,content) VALUES($1,$2,$3)`, id, value["title"], value["content"]); err != nil {
							return err
						}
					}
					if kind == "source" {
						if _, err = conn.ExecContext(ctx, `INSERT INTO identities(identity,project_id) VALUES($1,$2) ON CONFLICT(identity) DO NOTHING`, value["identity"], value["projectId"]); err != nil {
							return err
						}
					}
					imported++
				}
			}
			records, err := entityList(ctx, conn, "record")
			if err != nil {
				return err
			}
			for _, record := range records {
				metadata, err := optional(ctx, conn, "metadata", stringValue(record, "id"))
				if err != nil {
					return err
				}
				if metadata == nil {
					return importInvalid()
				}
			}
			links, err := entityList(ctx, conn, "relationship")
			if err != nil {
				return err
			}
			for _, link := range links {
				if link["status"] == "accepted" {
					if err := relationshipSafe(ctx, conn, stringValue(link, "fromRecordId"), stringValue(link, "toRecordId"), stringValue(link, "type"), stringValue(link, "id")); err != nil {
						return err
					}
				}
			}
			id, err := entityID("import")
			if err != nil {
				return err
			}
			if err = apiAudit(ctx, conn, "data.imported", "import", id, actor, nil, map[string]any{"format": "scratchpad", "imported": imported, "skipped": skipped}); err != nil {
				return err
			}
			result = map[string]any{"imported": imported, "skipped": skipped, "warnings": []any{}}
			return nil
		})
		if err != nil {
			s.fail(w, err)
			return
		}
		JSON(w, 200, result)
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
func portableReferences(kind string, value map[string]any) error {
	version, ok := portableVersion(value["version"])
	// PostgreSQL stores INTEGER versions. Require that common range and exact
	// numerical admission before either engine can persist an archive entry.
	if !portableID(value["id"]) || !ok || version < 1 || version > 2147483647 || math.Trunc(version) != version || !EqualJSONNumbers(value["version"], version) || !portableTimestamp(value["createdAt"]) {
		return importInvalid()
	}
	fields := []string{}
	switch kind {
	case "record", "source":
		fields = []string{"projectId"}
	case "metadata", "revision", "evidence", "mirror":
		fields = []string{"recordId"}
	case "relationship":
		fields = []string{"fromRecordId", "toRecordId"}
	case "audit":
		fingerprint := value["entityType"] == "credential" && (value["action"] == "github.key_blocked" || value["action"] == "github.key_unblocked") && regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`).MatchString(stringValue(value, "entityId"))
		if !fingerprint {
			fields = []string{"entityId"}
		}
	}
	for _, field := range fields {
		if !portableID(value[field]) {
			return importInvalid()
		}
	}
	return nil
}

func portableVersion(value any) (float64, bool) {
	if version, ok := value.(float64); ok {
		return version, true
	}
	if version, ok := value.(json.Number); ok {
		parsed, err := version.Float64()
		return parsed, err == nil
	}
	return 0, false
}

func portableEntity(ctx context.Context, conn *sql.Conn, kind string, id any) error {
	if !portableID(id) {
		return importInvalid()
	}
	_, err := get(ctx, conn, kind, id.(string))
	if errors.Is(err, sql.ErrNoRows) {
		code := "IMPORT_INVALID"
		if kind == "project" {
			code = "PROJECT_NOT_FOUND"
		}
		return &protocolError{code, "Import references missing knowledge.", 404}
	}
	return err
}
func validatePortable(ctx context.Context, conn *sql.Conn, kind string, value map[string]any, sources *portableSources) error {
	switch kind {
	case "project":
		if _, err := normalizeSettings(value["settings"]); err != nil {
			return importInvalid()
		}
		if stringValue(value, "name") == "" || !contains([]string{"normal", "external"}, stringValue(value, "kind")) {
			return importInvalid()
		}
	case "profile":
		if value["id"] != "owner-profile" {
			return importInvalid()
		}
		name, ok := value["displayName"].(string)
		_, err := curationText(strings.TrimSpace(name), 200)
		if !ok || err != nil || strings.TrimSpace(name) == "" {
			return importInvalid()
		}
	case "settings":
		if value["id"] != "global" {
			return importInvalid()
		}
		for field := range value {
			if !contains([]string{"id", "version", "createdAt", "updatedAt", "aiEnabled", "defaultProjectSettings"}, field) {
				return importInvalid()
			}
		}
		if flag, exists := value["aiEnabled"]; exists {
			if _, ok := flag.(bool); !ok {
				return importInvalid()
			}
		}
		if settings, exists := value["defaultProjectSettings"]; exists {
			if _, err := normalizeSettings(settings); err != nil {
				return importInvalid()
			}
		}
	case "record":
		if err := portableEntity(ctx, conn, "project", value["projectId"]); err != nil {
			return err
		}
		if !contains(recordTypes, stringValue(value, "type")) {
			return importInvalid()
		}
		for _, field := range []string{"title", "content"} {
			if _, ok := value[field].(string); !ok {
				return importInvalid()
			}
		}
		if _, ok := value["payload"].(map[string]any); !ok {
			return importInvalid()
		}
		if value["authority"] != nil && !contains([]string{"explicit", "observed", "inferred", "derived", "suggested"}, stringValue(value, "authority")) {
			return importInvalid()
		}
		if _, exists := value["authority"]; !exists {
			return importInvalid()
		}
		if confidence, exists := value["confidence"]; exists {
			if _, err := captureEnum(confidence, []string{"high", "medium", "low", "unknown"}); err != nil {
				return importInvalid()
			}
		}
		if reason, exists := value["confidenceReason"]; exists {
			if _, err := captureText(reason, 100000); err != nil {
				return importInvalid()
			}
		}
		if !portableTimestamp(value["recordedAt"]) {
			return importInvalid()
		}
		if happenedAt, exists := value["happenedAt"]; exists && !portableTimestamp(happenedAt) {
			return importInvalid()
		}
	case "source":
		if err := portableEntity(ctx, conn, "project", value["projectId"]); err != nil {
			return err
		}
		// Resolver-created identities have no 500-character field limit;
		// escaping can expand their paths. The archive's existing byte bound
		// contains this required nonblank string without truncating history.
		identity, err := curationText(value["identity"], 64*1024*1024)
		if err != nil || strings.TrimSpace(identity) == "" {
			return importInvalid()
		}
		var previous string
		err = conn.QueryRowContext(ctx, `SELECT project_id FROM identities WHERE identity=$1`, identity).Scan(&previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && previous != value["projectId"] {
			return &protocolError{"CONFLICT", "Source identity belongs to another project.", 409}
		}
	case "metadata", "revision", "evidence", "mirror":
		if err := portableEntity(ctx, conn, "record", value["recordId"]); err != nil {
			return err
		}
		if (kind == "metadata" || kind == "mirror") && value["id"] != value["recordId"] {
			return importInvalid()
		}
		if kind == "revision" {
			if reason, exists := value["reason"]; exists {
				if _, ok := reason.(string); !ok {
					return importInvalid()
				}
			}
		}
		if kind == "mirror" {
			for _, field := range []string{"path", "error"} {
				if text, exists := value[field]; exists {
					if _, err := curationText(text, 4000); err != nil {
						return importInvalid()
					}
				}
			}
		}
		if kind == "evidence" {
			if _, err := captureEnum(value["kind"], []string{"url", "git_commit", "pull_request", "issue", "file", "conversation", "test", "deployment", "other"}); err != nil {
				return importInvalid()
			}
			reference, err := curationText(value["reference"], 4000)
			if err != nil || reference == "" {
				return importInvalid()
			}
			for field, limit := range map[string]int{"revision": 500, "description": 10000} {
				if text, exists := value[field]; exists {
					if _, err := curationText(text, limit); err != nil {
						return importInvalid()
					}
				}
			}
		}
		if kind == "metadata" {
			tags, ok := value["tags"].([]any)
			if !ok {
				return importInvalid()
			}
			for _, tag := range tags {
				if _, ok := tag.(string); !ok {
					return importInvalid()
				}
			}
			if archived, exists := value["archived"]; exists {
				if _, ok := archived.(bool); !ok {
					return importInvalid()
				}
			}
			for field, limit := range map[string]int{"displayTitle": 500, "curatedSummary": 100000} {
				if text, exists := value[field]; exists {
					if _, err := curationText(text, limit); err != nil {
						return importInvalid()
					}
				}
			}
		}
	case "relationship":
		if note, exists := value["note"]; exists {
			if _, err := curationText(note, 10000); err != nil {
				return importInvalid()
			}
		}
		for _, field := range []string{"fromRecordId", "toRecordId"} {
			if err := portableEntity(ctx, conn, "record", value[field]); err != nil {
				return err
			}
		}
		if value["fromRecordId"] == value["toRecordId"] || !contains(relationshipTypes, stringValue(value, "type")) || !contains([]string{"suggested", "accepted", "rejected"}, stringValue(value, "status")) || !contains([]string{"explicit", "inferred", "suggested"}, stringValue(value, "authority")) {
			return importInvalid()
		}
	case "audit":
		for _, field := range []string{"action", "entityType"} {
			if text, exists := value[field]; exists {
				if _, ok := text.(string); !ok {
					return importInvalid()
				}
			}
		}
	case "ai_artifact", "curated_artifact":
		return validatePortableArtifact(ctx, conn, kind, value, sources)
	}
	return nil
}

func portableTimestamp(value any) bool {
	text, ok := value.(string)
	if !ok || !isoTimestampPattern.MatchString(text) {
		return false
	}
	_, _, err := timestampParts(text)
	return err == nil
}

// Portable archives carry knowledge, not the deployment's authentication or
// provider configuration. Full operational backups have a separate boundary.
func (s *SSH) Export() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
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
		data := map[string][]json.RawMessage{}
		for _, kind := range knowledgeKinds {
			values, err := exportSection(ctx, tx, kind)
			if err != nil {
				s.fail(w, err)
				return
			}
			data[kind] = values
		}
		w.Header().Set("Content-Disposition", `attachment; filename="scratchpad-export.json"`)
		JSON(w, 200, map[string]any{"format": "scratchpad", "version": 1, "exportedAt": timestamp(time.Now()), "data": data})
	}
}

func exportSection(ctx context.Context, tx *sql.Tx, kind string) ([]json.RawMessage, error) {
	rows, err := tx.QueryContext(ctx, `SELECT data FROM entities WHERE kind=$1 ORDER BY created_at,id`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		raw, err = portableEntityJSON(kind, raw)
		if err != nil {
			return nil, err
		}
		values = append(values, raw)
	}
	return values, rows.Err()
}

// Export and replay share one lossless projection of portable entity fields.
// Incoming entries are compared intact so changed portable or private fields
// cannot be hidden by projecting both sides of an identity conflict.
func portableEntityJSON(kind string, raw []byte) ([]byte, error) {
	var err error
	if kind == "settings" {
		// Legacy provider secrets may reside in the global settings row.
		// Only the portable global policy fields cross this boundary.
		var stored map[string]json.RawMessage
		if err := json.Unmarshal(raw, &stored); err != nil {
			return nil, err
		}
		public := map[string]json.RawMessage{}
		for _, field := range []string{"id", "version", "createdAt", "updatedAt", "aiEnabled", "defaultProjectSettings"} {
			if value, ok := stored[field]; ok {
				public[field] = value
			}
		}
		raw, err = json.Marshal(public)
		if err != nil {
			return nil, err
		}
	}
	if kind == "audit" {
		var stored map[string]json.RawMessage
		if err := json.Unmarshal(raw, &stored); err != nil {
			return nil, err
		}
		var entityType, action string
		_ = json.Unmarshal(stored["entityType"], &entityType)
		_ = json.Unmarshal(stored["action"], &action)
		if entityType == "ai_settings" || action == "ai.configured" || action == "secrets.converted" {
			// Configuration snapshots are operational state, including legacy
			// plaintext keys. Preserve the audit envelope without exporting them.
			delete(stored, "previous")
			delete(stored, "next")
		} else if entityType == "settings" || action == "settings.updated" {
			for _, snapshot := range []string{"previous", "next"} {
				if value, exists := stored[snapshot]; exists {
					var fields map[string]json.RawMessage
					if err := json.Unmarshal(value, &fields); err != nil {
						delete(stored, snapshot)
						continue
					}
					public := map[string]json.RawMessage{}
					for _, field := range []string{"id", "version", "createdAt", "updatedAt", "aiEnabled", "defaultProjectSettings"} {
						if v, ok := fields[field]; ok {
							public[field] = v
						}
					}
					stored[snapshot], err = json.Marshal(public)
					if err != nil {
						return nil, err
					}
				}
			}
		}
		if entityType == "ai_settings" || entityType == "settings" || action == "ai.configured" || action == "secrets.converted" || action == "settings.updated" {
			raw, err = json.Marshal(stored)
			if err != nil {
				return nil, err
			}
		}
	}
	return raw, nil
}
