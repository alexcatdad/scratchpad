package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
	"unicode/utf16"
)

func curationEntity(ctx context.Context, q entityReader, kind, id string) (map[string]any, error) {
	value, err := entityMap(ctx, q, kind, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &protocolError{"NOT_FOUND", kind + " not found.", 404}
	}
	return value, err
}
func copyEntity(value map[string]any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var copy map[string]any
	err = json.Unmarshal(data, &copy)
	return copy, err
}
func curationText(value any, max int) (string, error) {
	text, ok := value.(string)
	if !ok || len(utf16.Encode([]rune(text))) > max {
		return "", validationProject()
	}
	return text, nil
}
func metadataChanges(raw any) (map[string]any, error) {
	body, ok := raw.(map[string]any)
	if !ok {
		return nil, validationProject()
	}
	changes := map[string]any{}
	for key, value := range body {
		switch key {
		case "displayTitle", "curatedSummary":
			limit := 500
			if key == "curatedSummary" {
				limit = 100000
			}
			text, err := curationText(value, limit)
			if err != nil {
				return nil, err
			}
			changes[key] = text
		case "archived":
			flag, ok := value.(bool)
			if !ok {
				return nil, validationProject()
			}
			changes[key] = flag
		case "tags":
			values, ok := value.([]any)
			if !ok || len(values) > 100 {
				return nil, validationProject()
			}
			tags := []string{}
			for _, value := range values {
				text, err := curationText(value, 100)
				if err != nil {
					return nil, err
				}
				tags = append(tags, text)
			}
			changes[key] = tags
		default:
			return nil, validationProject()
		}
	}
	return changes, nil
}
func amendMetadata(ctx context.Context, conn *sql.Conn, r *http.Request, body, actor map[string]any, revisions bool) (map[string]any, error) {
	id := r.PathValue("id")
	if _, err := curationEntity(ctx, conn, "record", id); err != nil {
		return nil, err
	}
	metadata, err := curationEntity(ctx, conn, "metadata", id)
	if err != nil {
		return nil, err
	}
	raw := any(body)
	if revisions {
		raw = body["changes"]
	} else {
		fields := map[string]any{}
		for key, value := range body {
			if key != "expectedVersion" {
				fields[key] = value
			}
		}
		raw = fields
	}
	changes, err := metadataChanges(raw)
	if err != nil {
		return nil, err
	}
	expected, err := expectedVersion(body, r)
	if err != nil {
		return nil, err
	}
	if metadata["version"] != expected {
		return nil, &versionConflict{metadata["version"]}
	}
	previous, err := copyEntity(metadata)
	if err != nil {
		return nil, err
	}
	for key, value := range changes {
		metadata[key] = value
	}
	if err = update(ctx, conn, "metadata", metadata); err != nil {
		return nil, err
	}
	revisionID, err := entityID("rev")
	if err != nil {
		return nil, err
	}
	revision := map[string]any{"recordId": id, "revisionNumber": metadata["version"], "previous": previous, "next": metadata, "patch": changes, "actor": actor}
	if reason, ok := body["reason"].(string); ok {
		revision["reason"] = reason
	}
	if err = insert(ctx, conn, "revision", revisionID, revision); err != nil {
		return nil, err
	}
	if err = apiAudit(ctx, conn, "record.curated", "record", id, actor, previous, metadata); err != nil {
		return nil, err
	}
	return map[string]any{"metadata": metadata, "revision": revision}, nil
}
func (s *SSH) Curation(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if _, err := apiActor(ctx, s.Store.DB, r, s.Origin); err != nil {
			s.fail(w, err)
			return
		}
		var body map[string]any
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 3*1024*1024))
		if decoder.Decode(&body) != nil || body == nil || decoder.Decode(new(any)) != io.EOF {
			s.fail(w, validationProject())
			return
		}
		var result map[string]any
		err := s.Store.Write(ctx, func(conn *sql.Conn) error {
			actor, err := apiActor(ctx, conn, r, s.Origin)
			if err != nil {
				return err
			}
			switch operation {
			case "relationship-create", "relationship-accept", "relationship-reject":
				result, err = relationshipMutation(ctx, conn, r, body, actor, operation)
			case "evidence":
				result, err = addEvidence(ctx, conn, r.PathValue("id"), body, actor)
			case "mirror":
				result, err = reportMirror(ctx, conn, r.PathValue("id"), body, actor)
			default:
				result, err = amendMetadata(ctx, conn, r, body, actor, operation == "revisions")
			}
			return err
		})
		if err != nil {
			s.fail(w, err)
			return
		}
		status := 200
		if operation == "evidence" || operation == "relationship-create" {
			status = 201
		}
		JSON(w, status, result)
	}
}

func addEvidence(ctx context.Context, conn *sql.Conn, id string, body, actor map[string]any) (map[string]any, error) {
	if _, err := curationEntity(ctx, conn, "record", id); err != nil {
		return nil, err
	}
	kind, err := captureEnum(body["kind"], []string{"url", "git_commit", "pull_request", "issue", "file", "conversation", "test", "deployment", "other"})
	if err != nil {
		return nil, err
	}
	reference, err := curationText(body["reference"], 4000)
	if err != nil || reference == "" {
		return nil, validationProject()
	}
	evidence := map[string]any{"kind": kind, "reference": reference, "recordId": id}
	for _, field := range []string{"revision", "description"} {
		if value, exists := body[field]; exists {
			limit := 500
			if field == "description" {
				limit = 10000
			}
			evidence[field], err = curationText(value, limit)
			if err != nil {
				return nil, err
			}
		}
	}
	key, err := entityID("evi")
	if err != nil {
		return nil, err
	}
	if err = insert(ctx, conn, "evidence", key, evidence); err != nil {
		return nil, err
	}
	if err = apiAudit(ctx, conn, "evidence.created", "record", id, actor, nil, evidence); err != nil {
		return nil, err
	}
	return map[string]any{"evidence": evidence}, nil
}
func reportMirror(ctx context.Context, conn *sql.Conn, id string, body, actor map[string]any) (map[string]any, error) {
	record, err := curationEntity(ctx, conn, "record", id)
	if err != nil {
		return nil, err
	}
	next := map[string]any{"recordId": id, "updatedAt": timestamp(time.Now())}
	for _, field := range []string{"attempted", "succeeded"} {
		flag, ok := body[field].(bool)
		if !ok {
			return nil, validationProject()
		}
		next[field] = flag
	}
	for _, field := range []string{"path", "error"} {
		if value, exists := body[field]; exists {
			next[field], err = curationText(value, 4000)
			if err != nil {
				return nil, err
			}
		}
	}
	project, err := projectEntity(ctx, conn, stringValue(record, "projectId"))
	if err != nil {
		return nil, err
	}
	if !mirrorEligible(project, record) {
		return nil, &protocolError{"MIRROR_NOT_ALLOWED", "Project settings prohibit mirroring.", 403}
	}
	raw, err := optional(ctx, conn, "mirror", id)
	if err != nil {
		return nil, err
	}
	var previous map[string]any
	if raw == nil {
		err = insert(ctx, conn, "mirror", id, next)
	} else {
		if err = json.Unmarshal(raw, &previous); err != nil {
			return nil, err
		}
		current, err := copyEntity(previous)
		if err != nil {
			return nil, err
		}
		for key, value := range next {
			current[key] = value
		}
		next = current
		err = update(ctx, conn, "mirror", next)
	}
	if err != nil {
		return nil, err
	}
	if err = apiAudit(ctx, conn, "record.mirror_reported", "record", id, actor, previous, next); err != nil {
		return nil, err
	}
	return map[string]any{"mirror": next}, nil
}
