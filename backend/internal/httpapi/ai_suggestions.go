package httpapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type artifactSnapshot struct {
	sources  *portableSources
	projects map[string]map[string]any
}

func referencedEntities(ctx context.Context, conn *sql.Conn, kind string, ids map[string]bool) (map[string]map[string]any, error) {
	keys := []string{}
	for id := range ids {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	result := map[string]map[string]any{}
	for start := 0; start < len(keys); start += 400 {
		batch := keys[start:min(start+400, len(keys))]
		marks := make([]string, len(batch))
		args := []any{kind}
		for i, id := range batch {
			marks[i] = fmt.Sprintf("$%d", i+2)
			args = append(args, id)
		}
		rows, err := conn.QueryContext(ctx, "SELECT data FROM entities WHERE kind=$1 AND id IN ("+strings.Join(marks, ",")+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var raw []byte
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			var row map[string]any
			if err = json.Unmarshal(raw, &row); err != nil {
				rows.Close()
				return nil, err
			}
			result[stringValue(row, "id")] = row
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
func loadArtifactSnapshot(ctx context.Context, conn *sql.Conn, artifacts []map[string]any) (*artifactSnapshot, error) {
	recordIDs, projectIDs := map[string]bool{}, map[string]bool{}
	add := func(ids map[string]bool, value any) {
		if array, ok := value.([]any); ok {
			for _, id := range array {
				if portableID(id) {
					ids[id.(string)] = true
				}
			}
		}
	}
	for _, artifact := range artifacts {
		add(recordIDs, artifact["sourceRecordIds"])
		add(projectIDs, artifact["projectIds"])
		if deps, ok := artifact["privacyDependencies"].(map[string]any); ok {
			add(recordIDs, deps["recordIds"])
			add(projectIDs, deps["projectIds"])
		}
	}
	projects, err := referencedEntities(ctx, conn, "project", projectIDs)
	if err != nil {
		return nil, err
	}
	records, err := referencedEntities(ctx, conn, "record", recordIDs)
	if err != nil {
		return nil, err
	}
	sources := &portableSources{projects: map[string]bool{}, records: records}
	for id := range projects {
		sources.projects[id] = true
	}
	return &artifactSnapshot{sources, projects}, nil
}
func artifactAllowed(artifact map[string]any, snapshot *artifactSnapshot, scope jobScope) bool {
	deps, ok := artifact["privacyDependencies"].(map[string]any)
	if !ok {
		return false
	}
	projects, ok := portableIDs(deps["projectIds"])
	if !ok {
		return false
	}
	requested := scope.ProjectIDs
	if requested == nil && scope.ProjectID != "" {
		requested = []string{scope.ProjectID}
	}
	cited, _ := portableIDs(artifact["projectIds"])
	cross := scope.CrossProject || deps["crossProject"] == true || artifact["crossProject"] == true || len(cited) > 1
	for _, id := range projects {
		project := snapshot.projects[id]
		if project == nil || (requested != nil && !contains(requested, id)) {
			return false
		}
		settings, err := normalizeSettings(project["settings"])
		if err != nil || settings["aiProcessing"] != true || (cross && settings["crossProjectAnalysis"] != true) {
			return false
		}
	}
	return true
}

type suggestionRow struct {
	data []byte
	key  []string
}

// suggestionInstant separates whole seconds from arbitrary fractional digits,
// including historical offsets whose UTC instant falls outside years 0000–9999.
func suggestionInstant(value string) (int64, string, error) {
	base, fraction := value, ""
	if len(value) > 19 && (value[19] == '.' || value[19] == ',') {
		end := 20
		for end < len(value) && value[end] >= '0' && value[end] <= '9' {
			end++
		}
		if end == 20 {
			return 0, "", strconv.ErrSyntax
		}
		fraction = strings.TrimRight(value[20:end], "0")
		base = value[:19] + value[end:]
	}
	at, err := time.Parse(time.RFC3339Nano, base)
	if err != nil {
		at, err = time.Parse("2006-01-02T15:04Z", base)
	}
	return at.Unix(), fraction, err
}

func suggestionTimestamp(value string) string {
	seconds, fraction, err := suggestionInstant(value)
	at := time.Unix(seconds, 0).UTC()
	if err != nil || at.Year() < 0 || at.Year() > 9999 {
		return value
	}
	key := at.Format("2006-01-02T15:04:05")
	if fraction != "" {
		key += "." + fraction
	}
	return key + "Z"
}

func compareSuggestionKeys(left, right []string, scoped bool) int {
	leftSeconds, leftFraction, leftErr := suggestionInstant(left[0])
	rightSeconds, rightFraction, rightErr := suggestionInstant(right[0])
	order := 0
	if leftErr != nil || rightErr != nil {
		if leftErr == nil && rightErr != nil {
			order = 1
		} else if leftErr != nil && rightErr == nil {
			order = -1
		} else {
			order = strings.Compare(left[0], right[0])
		}
	} else if leftSeconds < rightSeconds {
		order = -1
	} else if leftSeconds > rightSeconds {
		order = 1
	} else {
		width := max(len(leftFraction), len(rightFraction))
		order = strings.Compare(leftFraction+strings.Repeat("0", width-len(leftFraction)), rightFraction+strings.Repeat("0", width-len(rightFraction)))
	}
	if order != 0 {
		return -order
	}
	if order = strings.Compare(left[1], right[1]); order != 0 {
		if scoped {
			return -order
		}
		return order
	}
	return strings.Compare(left[2], right[2])
}

func (a *AI) suggestionWindow(ctx context.Context, conn *sql.Conn, scope jobScope, after []string) ([]suggestionRow, error) {
	ids := scope.ProjectIDs
	if ids == nil && scope.ProjectID != "" {
		ids = []string{scope.ProjectID}
	}
	args := []any{}
	marks := make([]string, len(ids))
	for i, id := range ids {
		args = append(args, id)
		marks[i] = fmt.Sprintf("$%d", i+1)
	}
	membership := "1=1"
	if ids != nil {
		membership = "EXISTS(SELECT 1 FROM json_each(CASE WHEN json_valid(entities.data) THEN entities.data ELSE '{}' END,'$.projectIds') WHERE value IN (" + strings.Join(marks, ",") + "))"
	}
	engine := a.auth.Store.Engine
	if engine == "postgres" && ids != nil {
		membership = "EXISTS(SELECT 1 FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(data->'projectIds')='array' THEN data->'projectIds' ELSE '[]'::jsonb END) AS projects(value) WHERE value IN (" + strings.Join(marks, ",") + "))"
	}
	storedOrder, storedCompare := "ASC", ">"
	if ids != nil {
		storedOrder, storedCompare = "DESC", "<"
	}
	collation := "BINARY"
	if engine == "postgres" {
		collation = `"C"`
	}
	statement := "WITH admitted AS(SELECT id COLLATE " + collation + " AS id,created_at COLLATE " + collation + " AS stored_at,COALESCE((" + searchText(engine, "data", "createdAt") + "),'') AS timestamp FROM entities WHERE kind='ai_artifact' AND " + membership + "), ordered AS(SELECT id,stored_at,timestamp," + searchCanonicalUTC(engine, "timestamp") + " AS sort_key FROM admitted) SELECT timestamp,stored_at,id FROM ordered WHERE "
	results := []suggestionRow{}
	if after != nil {
		after = append([]string{}, after...)
		after[0] = suggestionTimestamp(after[0])
	}
	read := func(query string, arguments []any) error {
		rows, err := conn.QueryContext(ctx, query, arguments...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			key := make([]string, 3)
			if err = rows.Scan(&key[0], &key[1], &key[2]); err != nil {
				return err
			}
			key[0] = suggestionTimestamp(key[0])
			if after != nil && compareSuggestionKeys(key, after, ids != nil) <= 0 {
				continue
			}
			index := sort.Search(len(results), func(i int) bool { return compareSuggestionKeys(results[i].key, key, ids != nil) >= 0 })
			if index < 64 {
				results = append(results, suggestionRow{})
				copy(results[index+1:], results[index:])
				results[index] = suggestionRow{key: key}
				if len(results) > 64 {
					results = results[:64]
				}
			}
		}
		return rows.Err()
	}
	// Only historical/noncanonical headers require a streaming comparison.
	// Native canonical timestamps use exact keyset ordering and a bounded limit.
	if err := read(statement+"sort_key IS NULL", args); err != nil {
		return nil, err
	}
	canonical := "sort_key IS NOT NULL"
	canonicalArgs := append([]any{}, args...)
	bounded := after == nil
	if after != nil {
		if key := searchChronologicalKey(after[0]); key != "" {
			base := len(canonicalArgs)
			canonicalArgs = append(canonicalArgs, key, after[1], after[2])
			first, second, third := fmt.Sprintf("$%d", base+1), fmt.Sprintf("$%d", base+2), fmt.Sprintf("$%d", base+3)
			canonical += " AND (sort_key<" + first + " OR (sort_key=" + first + " AND (stored_at" + storedCompare + second + " OR (stored_at=" + second + " AND id>" + third + "))))"
			bounded = true
		}
	}
	canonical += " ORDER BY sort_key DESC,stored_at " + storedOrder + ",id"
	if bounded {
		canonical += " LIMIT 64"
	}
	if err := read(statement+canonical, canonicalArgs); err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return results, nil
	}
	// Hydrate only the selected window, preserving malformed off-page isolation.
	marks, args = make([]string, len(results)), []any{}
	byID := map[string]int{}
	for i, row := range results {
		marks[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, row.key[2])
		byID[row.key[2]] = i
	}
	rows, err := conn.QueryContext(ctx, "SELECT id,data FROM entities WHERE kind='ai_artifact' AND id IN ("+strings.Join(marks, ",")+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		results[byID[id]].data = raw
	}
	return results, rows.Err()
}
func (a *AI) Suggestions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if _, err := apiActor(ctx, a.auth.Store.DB, r, a.auth.Origin); err != nil {
			a.auth.fail(w, err)
			return
		}
		body := map[string]any{}
		params := r.URL.Query()
		if value, exists := params["projectId"]; exists {
			body["projectId"] = value[0]
		}
		if values, exists := params["projectIds"]; exists {
			ids := []any{}
			for _, id := range values {
				ids = append(ids, id)
			}
			body["projectIds"] = ids
		}
		if values, exists := params["crossProject"]; exists {
			if values[0] != "true" && values[0] != "false" {
				a.auth.fail(w, aiInvalid())
				return
			}
			body["crossProject"] = values[0] == "true"
		}
		scope, err := parseJobScope(body)
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		limit := 100
		if params.Has("limit") {
			limit, err = strconv.Atoi(params.Get("limit"))
			if err != nil || limit < 1 || limit > 200 {
				a.auth.fail(w, aiInvalid())
				return
			}
		}
		boundScope := scope
		if scope.ProjectIDs != nil {
			boundScope.ProjectIDs = uniqueIDs(scope.ProjectIDs)
			sort.Strings(boundScope.ProjectIDs)
		}
		scopeRaw, _ := json.Marshal(boundScope)
		scopeKey := legacyDigest(string(scopeRaw))
		var after []string
		if params.Has("cursor") {
			raw, decodeErr := base64.RawURLEncoding.DecodeString(params.Get("cursor"))
			if decodeErr != nil || len(raw) > 8192 || json.Unmarshal(raw, &after) != nil || len(after) != 4 || after[2] == "" || after[3] != scopeKey {
				a.auth.fail(w, aiInvalid())
				return
			}
			after = after[:3]
		}
		results := []map[string]any{}
		keys := [][]string{}
		err = func() error {
			conn, err := a.auth.Store.DB.Conn(ctx)
			if err != nil {
				return err
			}
			defer conn.Close()
			begin := "BEGIN"
			if a.auth.Store.Engine == "postgres" {
				begin = "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY"
			}
			if _, err = conn.ExecContext(ctx, begin); err != nil {
				return err
			}
			defer conn.ExecContext(context.Background(), "ROLLBACK")
			if _, err := apiActor(ctx, conn, r, a.auth.Origin); err != nil {
				return err
			}
			row, err := readSettings(ctx, conn, "ai_settings")
			if err != nil {
				return err
			}
			cfg, err := aiConfig(row)
			if err != nil {
				return err
			}
			if !cfg.Enabled {
				return nil
			}
			for len(results) <= limit {
				window, err := a.suggestionWindow(ctx, conn, scope, after)
				if err != nil {
					return err
				}
				for _, row := range window {
					after = row.key
					var candidate map[string]any
					if err = json.Unmarshal(row.data, &candidate); err != nil {
						return err
					}
					snapshot, err := loadArtifactSnapshot(ctx, conn, []map[string]any{candidate})
					if err != nil {
						return err
					}
					if validatePortableArtifact(ctx, conn, "ai_artifact", candidate, snapshot.sources) == nil && artifactAllowed(candidate, snapshot, scope) {
						results = append(results, candidate)
						keys = append(keys, row.key)
						if len(results) > limit {
							break
						}
					}
				}
				if len(window) < 64 {
					break
				}
			}
			return nil
		}()
		if err == nil {
			// Fresh authorization and dependency-project consent are bounded by
			// this page plus its readable lookahead. Immutable source bodies stay
			// in the read snapshot rather than being decoded under the writer lock.
			err = a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
				if _, err := apiActor(ctx, conn, r, a.auth.Origin); err != nil {
					return err
				}
				row, err := readSettings(ctx, conn, "ai_settings")
				if err != nil {
					return err
				}
				cfg, err := aiConfig(row)
				if err != nil {
					return err
				}
				if !cfg.Enabled {
					results = []map[string]any{}
					return nil
				}
				ids := map[string]bool{}
				for _, artifact := range results {
					deps := artifact["privacyDependencies"].(map[string]any)
					projects, _ := portableIDs(deps["projectIds"])
					for _, id := range projects {
						ids[id] = true
					}
				}
				projects, err := referencedEntities(ctx, conn, "project", ids)
				if err != nil {
					return err
				}
				fresh := &artifactSnapshot{projects: projects}
				for _, artifact := range results {
					if !artifactAllowed(artifact, fresh, scope) {
						return &protocolError{"AI_NOT_ALLOWED", "Current consent does not permit this suggestion page.", 403}
					}
				}
				return nil
			})
		}
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		hasMore := len(results) > limit
		response := map[string]any{"suggestions": results, "hasMore": hasMore}
		if hasMore {
			response["suggestions"] = results[:limit]
			key := append(append([]string{}, keys[limit-1]...), scopeKey)
			raw, _ := json.Marshal(key)
			response["nextCursor"] = base64.RawURLEncoding.EncodeToString(raw)
		}
		JSON(w, 200, response)
	}
}
func (a *AI) ReviewSuggestion(accept bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if _, err := apiActor(ctx, a.auth.Store.DB, r, a.auth.Origin); err != nil {
			a.auth.fail(w, err)
			return
		}
		var body map[string]any
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		if decoder.Decode(&body) != nil || body == nil || decoder.Decode(new(any)) != io.EOF {
			a.auth.fail(w, aiInvalid())
			return
		}
		var result map[string]any
		err := a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
			actor, err := apiActor(ctx, conn, r, a.auth.Origin)
			if err != nil {
				return err
			}
			original, err := curationEntity(ctx, conn, "ai_artifact", r.PathValue("id"))
			if err != nil {
				return err
			}
			snapshot, err := loadArtifactSnapshot(ctx, conn, []map[string]any{original})
			if err != nil {
				return err
			}
			if err = validatePortableArtifact(ctx, conn, "ai_artifact", original, snapshot.sources); err != nil {
				var protocol *protocolError
				if errors.As(err, &protocol) && contains([]string{"IMPORT_INVALID", "PROJECT_NOT_FOUND"}, protocol.code) {
					return artifactInvalid()
				}
				return err
			}
			row, err := readSettings(ctx, conn, "ai_settings")
			if err != nil {
				return err
			}
			cfg, err := aiConfig(row)
			if err != nil {
				return err
			}
			if !cfg.Enabled || !artifactAllowed(original, snapshot, jobScope{}) {
				return &protocolError{"AI_NOT_ALLOWED", "Current consent or privacy provenance does not permit this artifact.", 403}
			}
			if original["status"] != "pending" {
				return &protocolError{"CONFLICT", "Suggestion has already been reviewed.", 409}
			}
			expected, err := expectedVersion(body, r)
			if err != nil {
				return err
			}
			if original["version"] != expected {
				return &versionConflict{original["version"]}
			}
			previous, err := copyEntity(original)
			if err != nil {
				return err
			}
			status := "rejected"
			if accept {
				status = "accepted"
			}
			original["status"], original["reviewedBy"], original["reviewedAt"] = status, actor, timestamp(time.Now())
			if err = update(ctx, conn, "ai_artifact", original); err != nil {
				return err
			}
			if accept {
				id, err := entityID("curated")
				if err != nil {
					return err
				}
				curated := map[string]any{"artifactId": original["id"], "content": original["content"], "kind": original["kind"], "sourceRecordIds": original["sourceRecordIds"], "generator": original["generator"], "authority": "derived", "acceptedBy": actor, "projectIds": original["projectIds"], "privacyDependencies": original["privacyDependencies"], "private": true}
				if err = insert(ctx, conn, "curated_artifact", id, curated); err != nil {
					return err
				}
				deps := original["privacyDependencies"].(map[string]any)
				projects, _ := portableIDs(deps["projectIds"])
				if len(uniqueIDs(projects)) == 1 && contains([]string{"relationship_candidate", "contradiction"}, stringValue(original, "kind")) {
					content := original["content"].(map[string]any)
					from, to := stringValue(content, "fromRecordId"), stringValue(content, "toRecordId")
					kind := stringValue(content, "relationshipType")
					if kind == "" {
						kind = "related_to"
					}
					if original["kind"] == "contradiction" {
						kind = "contradicts"
					}
					if err = relationshipSafe(ctx, conn, from, to, kind, ""); err != nil {
						return err
					}
					id, err := entityID("rel")
					if err != nil {
						return err
					}
					link := map[string]any{"fromRecordId": from, "toRecordId": to, "type": kind, "authority": "suggested", "status": "accepted", "actor": actor, "generator": original["generator"], "artifactId": original["id"], "derivation": map[string]any{"authority": "derived", "artifactId": original["id"], "generator": original["generator"]}}
					if err = insert(ctx, conn, "relationship", id, link); err != nil {
						return err
					}
					if err = apiAudit(ctx, conn, "relationship.ai.accepted", "relationship", id, actor, nil, link); err != nil {
						return err
					}
				}
				if len(uniqueIDs(projects)) == 1 && original["kind"] == "classification" {
					sources, _ := portableIDs(original["sourceRecordIds"])
					raw, err := optional(ctx, conn, "metadata", sources[0])
					if err != nil {
						return err
					}
					if raw != nil {
						var metadata map[string]any
						if err = json.Unmarshal(raw, &metadata); err != nil {
							return err
						}
						before, err := copyEntity(metadata)
						if err != nil {
							return err
						}
						content := original["content"].(map[string]any)
						for _, field := range []string{"tags", "classification"} {
							if value, exists := content[field]; exists {
								metadata[field] = value
							}
						}
						metadata["classificationArtifactId"] = original["id"]
						if err = update(ctx, conn, "metadata", metadata); err != nil {
							return err
						}
						if err = apiAudit(ctx, conn, "metadata.ai.accepted", "record", sources[0], actor, before, metadata); err != nil {
							return err
						}
					}
				}
			}
			if err = apiAudit(ctx, conn, "ai.suggestion."+status, "ai_artifact", stringValue(original, "id"), actor, previous, original); err != nil {
				return err
			}
			result = original
			return nil
		})
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		JSON(w, 200, map[string]any{"suggestion": result})
	}
}
func uniqueIDs(ids []string) []string {
	result := []string{}
	for _, id := range ids {
		if !contains(result, id) {
			result = append(result, id)
		}
	}
	return result
}
