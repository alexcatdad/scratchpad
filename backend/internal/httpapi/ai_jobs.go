package httpapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/alexcatdad/scratchpad/backend/internal/domain"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"
)

type jobScope = domain.JobScope

func parseJobScope(body map[string]any) (jobScope, error) {
	scope := jobScope{}
	_, single := body["projectId"]
	_, multiple := body["projectIds"]
	if single && multiple {
		return scope, aiInvalid()
	}
	if value, exists := body["crossProject"]; exists {
		flag, ok := value.(bool)
		if !ok {
			return scope, aiInvalid()
		}
		scope.CrossProject = flag
	}
	if value, exists := body["projectId"]; exists {
		id, ok := value.(string)
		if !ok || id == "" {
			return scope, aiInvalid()
		}
		scope.ProjectID = id
	}
	if value, exists := body["projectIds"]; exists {
		ids, ok := value.([]any)
		if !ok || len(ids) < 1 || len(ids) > 100 {
			return scope, aiInvalid()
		}
		scope.ProjectIDs = []string{}
		for _, value := range ids {
			id, ok := value.(string)
			if !ok || id == "" {
				return scope, aiInvalid()
			}
			scope.ProjectIDs = append(scope.ProjectIDs, id)
		}
	}
	return scope, nil
}

type scopeReader interface {
	entityReader
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func allowedJobs(ctx context.Context, conn scopeReader, scope jobScope) ([]string, error) {
	if scope.ProjectID != "" && scope.ProjectIDs != nil {
		return nil, aiInvalid()
	}
	ids := scope.ProjectIDs
	if ids == nil && scope.ProjectID != "" {
		ids = []string{scope.ProjectID}
	}
	if !scope.CrossProject && len(ids) != 1 {
		return nil, aiInvalid()
	}
	projects := []map[string]any{}
	if ids == nil {
		rows, err := entityList(ctx, conn, "project")
		if err != nil {
			return nil, err
		}
		projects = rows
	} else {
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			project, err := projectEntity(ctx, conn, id)
			if err != nil {
				return nil, err
			}
			projects = append(projects, project)
		}
		sort.SliceStable(projects, func(i, j int) bool {
			if projects[i]["createdAt"] != projects[j]["createdAt"] {
				return stringValue(projects[i], "createdAt") < stringValue(projects[j], "createdAt")
			}
			return stringValue(projects[i], "id") < stringValue(projects[j], "id")
		})
	}
	selected := []string{}
	for _, project := range projects {
		settings, err := normalizeSettings(project["settings"])
		if err != nil {
			return nil, err
		}
		allowed := settings["aiProcessing"] == true && (!scope.CrossProject || settings["crossProjectAnalysis"] == true)
		if !allowed {
			if ids != nil {
				return nil, &protocolError{"AI_NOT_ALLOWED", "A selected project does not permit this AI operation.", 403}
			}
			continue
		}
		selected = append(selected, stringValue(project, "id"))
	}
	if len(selected) == 0 {
		return nil, &protocolError{"AI_NOT_ALLOWED", "No selected project permits this AI operation.", 403}
	}
	return selected, nil
}
func (a *AI) jobProvider(ctx context.Context, conn *sql.Conn) error {
	row, err := readSettings(ctx, conn, "ai_settings")
	if err != nil {
		return err
	}
	cfg, err := aiConfig(row)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return &protocolError{"AI_DISABLED", "Enable optional AI first.", 409}
	}
	if _, err = a.box.Open(row["apiKey"], "ai_settings", "global", "apiKey"); err != nil {
		return secretUnavailable()
	}
	return nil
}
func enqueueAIJob(ctx context.Context, conn *sql.Conn, typ string, scope jobScope, format string, actor map[string]any) (map[string]any, error) {
	ids, err := allowedJobs(ctx, conn, scope)
	if err != nil {
		return nil, err
	}
	if format == "" {
		format = "handoff"
	}
	id, err := entityID("job")
	if err != nil {
		return nil, err
	}
	job := map[string]any{"type": typ, "scope": map[string]any{"projectIds": ids, "crossProject": scope.CrossProject}, "format": format, "status": "queued", "attempts": 0, "runAfter": timestamp(time.Now())}
	if err = insert(ctx, conn, "ai_job", id, job); err != nil {
		return nil, err
	}
	if err = apiAudit(ctx, conn, "ai.job.queued", "ai_job", id, actor, nil, job); err != nil {
		return nil, err
	}
	return job, nil
}
func (a *AI) Jobs(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if _, err := apiActor(ctx, a.auth.Store.DB, r, a.auth.Origin); err != nil {
			a.auth.fail(w, err)
			return
		}
		if operation == "list" {
			limit := 100
			params := r.URL.Query()
			if params.Has("limit") {
				var err error
				limit, err = strconv.Atoi(params.Get("limit"))
				if err != nil || limit < 1 || limit > 200 {
					a.auth.fail(w, validationProject())
					return
				}
			}
			ordering := "CASE WHEN json_valid(data) THEN CASE WHEN json_type(data,'$.createdAt')='text' THEN json_extract(data,'$.createdAt') ELSE '' END ELSE '' END"
			statusField := "CASE WHEN json_valid(data) THEN json_extract(data,'$.status') ELSE NULL END"
			typeField := "CASE WHEN json_valid(data) THEN json_extract(data,'$.type') ELSE NULL END"
			if a.auth.Store.Engine == "postgres" {
				ordering = "(CASE WHEN jsonb_typeof(data->'createdAt')='string' THEN data->>'createdAt' ELSE '' END) COLLATE \"C\""
				statusField = "data->>'status'"
				typeField = "data->>'type'"
			}
			where := "kind='ai_job'"
			args := []any{}
			if params.Has("jobId") {
				if !portableID(params.Get("jobId")) || params.Has("cursor") {
					a.auth.fail(w, validationProject())
					return
				}
				where += " AND id=$1"
				args = append(args, params.Get("jobId"))
			} else if params.Has("cursor") {
				var cursor []string
				raw, err := base64.RawURLEncoding.DecodeString(params.Get("cursor"))
				if err != nil || len(raw) > 8192 || json.Unmarshal(raw, &cursor) != nil || len(cursor) != 3 || cursor[2] == "" {
					a.auth.fail(w, validationProject())
					return
				}
				where += " AND (" + ordering + "<$1 OR (" + ordering + "=$1 AND (created_at>$2 OR (created_at=$2 AND id>$3))))"
				args = append(args, cursor[0], cursor[1], cursor[2])
			}
			args = append(args, limit+1)
			options := &sql.TxOptions{ReadOnly: true}
			if a.auth.Store.Engine == "postgres" {
				options.Isolation = sql.LevelRepeatableRead
			}
			tx, err := a.auth.Store.DB.BeginTx(ctx, options)
			if err != nil {
				a.auth.fail(w, err)
				return
			}
			defer tx.Rollback()
			if _, err = apiActor(ctx, tx, r, a.auth.Origin); err != nil {
				a.auth.fail(w, err)
				return
			}
			rows, err := tx.QueryContext(ctx, "SELECT data,"+ordering+",created_at,id FROM entities WHERE "+where+" ORDER BY "+ordering+" DESC,created_at,id LIMIT $"+strconv.Itoa(len(args)), args...)
			jobs := []map[string]any{}
			var last []string
			hasMore := false
			if err == nil {
				for rows.Next() {
					if len(jobs) == limit {
						hasMore = true
						break
					}
					var raw []byte
					var created, stored, id string
					if err = rows.Scan(&raw, &created, &stored, &id); err != nil {
						break
					}
					var job map[string]any
					if err = json.Unmarshal(raw, &job); err != nil {
						break
					}
					if job == nil {
						err = errors.New("Invalid persisted AI job.")
						break
					}
					jobs = append(jobs, job)
					last = []string{created, stored, id}
				}
				if err == nil {
					err = rows.Err()
				}
				rows.Close()
			}
			if err != nil {
				a.auth.fail(w, err)
				return
			}
			var hasActive bool
			row, err := readSettings(ctx, tx, "ai_settings")
			if err != nil {
				a.auth.fail(w, err)
				return
			}
			cfg, err := aiConfig(row)
			if err != nil {
				a.auth.fail(w, err)
				return
			}
			queued := "FALSE"
			activeArgs := []any{}
			if cfg.Enabled {
				queued = typeField + " IN ('embed','analyze','export')"
				if a.rag && a.auth.Store.Engine == "postgres" {
					if err = a.ragAvailable(ctx, tx); err == nil {
						fingerprint, err := ragFingerprint(cfg)
						if err != nil {
							a.auth.fail(w, err)
							return
						}
						queued += " OR (" + typeField + "='rag_index' AND data->>'fingerprint'=$1 AND data->>'processingVersion'=$2)"
						activeArgs = append(activeArgs, fingerprint, ragProcessingVersion)
					} else {
						var protocol *protocolError
						if !errors.As(err, &protocol) || protocol.code != "RAG_UNAVAILABLE" {
							a.auth.fail(w, err)
							return
						}
					}
				}
			}
			lease := searchText(a.auth.Store.Engine, "data", "leaseUntil")
			activeArgs = append(activeArgs, timestamp(time.Now()))
			now := "$" + strconv.Itoa(len(activeArgs))
			running := searchCanonicalUTC(a.auth.Store.Engine, lease) + " IS NOT NULL AND (" + lease + ">" + now + " OR (" + lease + "<=" + now + " AND (" + queued + ")))"
			if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM entities WHERE kind='ai_job' AND (("+statusField+"='running' AND ("+running+")) OR ("+statusField+"='queued' AND ("+queued+"))))", activeArgs...).Scan(&hasActive); err != nil {
				a.auth.fail(w, err)
				return
			}
			result := map[string]any{"jobs": jobs, "hasActive": hasActive}
			if hasMore {
				raw, _ := json.Marshal(last)
				result["nextCursor"] = base64.RawURLEncoding.EncodeToString(raw)
			}
			JSON(w, 200, result)
			return
		}
		var body map[string]any
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024*1024))
		if decoder.Decode(&body) != nil || body == nil || decoder.Decode(new(any)) != io.EOF {
			a.auth.fail(w, aiInvalid())
			return
		}
		var scope jobScope
		var err error
		enqueue := operation == "enqueue" || operation == "document"
		if operation == "document" {
			body["type"] = "export"
		}
		if enqueue {
			scope, err = parseJobScope(body)
			if err != nil {
				a.auth.fail(w, err)
				return
			}
			if !contains([]string{"analyze", "embed", "export"}, stringValue(body, "type")) {
				a.auth.fail(w, aiInvalid())
				return
			}
			if format, exists := body["format"]; exists {
				if !contains([]string{"handoff", "architecture", "decisions", "client_history", "adr"}, stringValue(map[string]any{"format": format}, "format")) {
					a.auth.fail(w, aiInvalid())
					return
				}
			}
		}
		var result map[string]any
		err = a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
			actor, err := apiActor(ctx, conn, r, a.auth.Origin)
			if err != nil {
				return err
			}
			if err = a.jobProvider(ctx, conn); err != nil {
				return err
			}
			if enqueue {
				result, err = enqueueAIJob(ctx, conn, stringValue(body, "type"), scope, stringValue(body, "format"), actor)
				return err
			}
			job, err := curationEntity(ctx, conn, "ai_job", r.PathValue("id"))
			if err != nil {
				return err
			}
			raw, _ := json.Marshal(job["scope"])
			var storedScope jobScope
			if json.Unmarshal(raw, &storedScope) != nil {
				return aiInvalid()
			}
			if _, err = allowedJobs(ctx, conn, storedScope); err != nil {
				return err
			}
			if job["status"] != "failed" {
				return &protocolError{"CONFLICT", "Only failed jobs may be retried.", 409}
			}
			expected, err := expectedVersion(body, r)
			if err != nil {
				return err
			}
			if job["version"] != expected {
				return &versionConflict{job["version"]}
			}
			if job["type"] == "rag_index" {
				if err = a.ragAvailable(ctx, conn); err != nil {
					return err
				}
				settings, err := readSettings(ctx, conn, "ai_settings")
				if err != nil {
					return err
				}
				cfg, err := aiConfig(settings)
				if err != nil {
					return err
				}
				fingerprint, err := ragFingerprint(cfg)
				if err != nil {
					return err
				}
				if job["fingerprint"] != fingerprint || job["processingVersion"] != ragProcessingVersion {
					return &protocolError{"CONFLICT", "Restore compatible embedding settings before retrying this passage job.", 409}
				}
			}
			previous, err := copyEntity(job)
			if err != nil {
				return err
			}
			job["status"], job["attempts"], job["runAfter"] = "queued", 0, timestamp(time.Now())
			for _, field := range []string{"lastError", "completedAt", "leaseToken", "leaseUntil"} {
				job[field] = nil
			}
			if err = update(ctx, conn, "ai_job", job); err != nil {
				return err
			}
			if err = apiAudit(ctx, conn, "ai.job.retried", "ai_job", stringValue(job, "id"), actor, previous, job); err != nil {
				return err
			}
			result = job
			return nil
		})
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		JSON(w, 202, map[string]any{"job": result})
	}
}
