package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"time"
)

type jobScope struct {
	ProjectID    string   `json:"projectId,omitempty"`
	ProjectIDs   []string `json:"projectIds,omitempty"`
	CrossProject bool     `json:"crossProject"`
}

func parseJobScope(body map[string]any) (jobScope, error) {
	scope := jobScope{}
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
			jobs, err := entityList(ctx, a.auth.Store.DB, "ai_job")
			if err != nil {
				a.auth.fail(w, err)
				return
			}
			sort.SliceStable(jobs, func(i, j int) bool { return stringValue(jobs[i], "createdAt") > stringValue(jobs[j], "createdAt") })
			JSON(w, 200, map[string]any{"jobs": jobs})
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
