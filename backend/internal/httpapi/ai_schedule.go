package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"time"

	"github.com/alexcatdad/scratchpad/backend/internal/domain"
)

// Probe only potentially overlapping active scopes. Narrow rows are fetched in
// bounded pages so unrelated completed history and artifact bodies stay in SQL.
func (a *AI) activeJobExists(ctx context.Context, conn *sql.Conn, typ, projectID string, cross bool) (bool, error) {
	status, kind := "json_extract(data,'$.status')", "json_extract(data,'$.type')"
	scope := "json_extract(data,'$.scope')"
	match := "((json_type(data,'$.scope.projectIds') IS NULL OR json_type(data,'$.scope.projectIds')='null') AND json_extract(data,'$.scope.projectId')=$2 OR EXISTS(SELECT 1 FROM json_each(CASE WHEN json_type(data,'$.scope.projectIds')='array' THEN json_extract(data,'$.scope.projectIds') ELSE '[]' END) WHERE value=$2))"
	if cross {
		match = "json_extract(data,'$.scope.crossProject')=1"
	}
	if a.auth.Store.Engine == "postgres" {
		status, kind, scope = "data->>'status'", "data->>'type'", "data->'scope'"
		match = "((data#>'{scope,projectIds}' IS NULL OR jsonb_typeof(data#>'{scope,projectIds}')='null') AND data#>>'{scope,projectId}'=$2 OR EXISTS(SELECT 1 FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(data#>'{scope,projectIds}')='array' THEN data#>'{scope,projectIds}' ELSE '[]'::jsonb END) v(value) WHERE value=$2))"
		if cross {
			match = "data#>>'{scope,crossProject}'='true'"
		}
	}
	aliases := "EXISTS(SELECT 1 FROM json_each(CASE WHEN json_type(data,'$.scope')='object' THEN json_extract(data,'$.scope') ELSE '{}' END) WHERE replace(lower(key),'ſ','s') IN ('projectid','projectids','crossproject') AND key NOT IN ('projectId','projectIds','crossProject'))"
	if a.auth.Store.Engine == "postgres" {
		aliases = "EXISTS(SELECT 1 FROM jsonb_each(CASE WHEN jsonb_typeof(data->'scope')='object' THEN data->'scope' ELSE '{}'::jsonb END) v(key,value) WHERE replace(lower(key),'ſ','s') IN ('projectid','projectids','crossproject') AND key NOT IN ('projectId','projectIds','crossProject'))"
	}
	// encoding/json admits historical case-insensitive aliases. Let its typed
	// scope decoder decide those uncommon rows instead of silently duplicating work.
	match = "((" + match + ") OR " + aliases + ")"
	if cross {
		match = "($2='' AND (" + match + "))"
		projectID = ""
	}
	created, id := "", ""
	for {
		args := []any{typ, projectID, created, id}
		rows, err := conn.QueryContext(ctx, "SELECT created_at,id,"+scope+" FROM entities WHERE kind='ai_job' AND "+status+" IN ('queued','running') AND "+kind+"=$1 AND ("+match+") AND (created_at>$3 OR (created_at=$3 AND id>$4)) ORDER BY created_at,id LIMIT 32", args...)
		if err != nil {
			return false, err
		}
		count := 0
		for rows.Next() {
			var raw []byte
			if err = rows.Scan(&created, &id, &raw); err != nil {
				rows.Close()
				return false, err
			}
			count++
			// Match aiScope's historical map normalization before resolving aliases.
			var stored any
			if json.Unmarshal(raw, &stored) != nil {
				continue
			}
			admitted, err := aiScope(map[string]any{"scope": stored})
			if err != nil {
				continue
			}
			if admitted.ProjectIDs == nil && admitted.ProjectID != "" {
				admitted.ProjectIDs = []string{admitted.ProjectID}
			}
			if (cross && admitted.CrossProject) || (!cross && contains(admitted.ProjectIDs, projectID)) {
				rows.Close()
				return true, nil
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return false, err
		}
		if count < 32 {
			return false, nil
		}
	}
}
func (a *AI) schedule(ctx context.Context, snapshot providerSnapshot) error {
	if snapshot.config.ScheduleMinutes == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		if err := a.providerCurrent(ctx, conn, nil, snapshot); err != nil {
			return err
		}
		if err := a.jobProvider(ctx, conn); err != nil {
			return err
		}
		raw, err := optional(ctx, conn, "ai_schedule", "global")
		if err != nil {
			return err
		}
		var previous map[string]any
		now := time.Now().UnixMilli()
		if raw != nil {
			if err = json.Unmarshal(raw, &previous); err != nil {
				return err
			}
			schedule := domain.ScheduleFrom(previous)
			if schedule.NextAt == nil {
				return aiInvalid()
			}
			deadline := *schedule.NextAt
			if math.IsNaN(deadline) || math.IsInf(deadline, 0) || deadline != math.Trunc(deadline) || deadline < 0 {
				return aiInvalid()
			}
			if float64(now) < deadline {
				return nil
			}
		}
		projects, err := entityList(ctx, conn, "project")
		if err != nil {
			return err
		}
		crossProjects := []string{}
		for _, project := range projects {
			settings, err := normalizeSettings(project["settings"])
			if err != nil {
				return err
			}
			if settings["aiProcessing"] != true {
				continue
			}
			id := stringValue(project, "id")
			if settings["crossProjectAnalysis"] == true {
				crossProjects = append(crossProjects, id)
			}
			for _, typ := range []string{"embed", "analyze"} {
				if typ == "analyze" && len(snapshot.config.AnalysisModes) == 0 {
					continue
				}
				exists, err := a.activeJobExists(ctx, conn, typ, id, false)
				if err != nil {
					return err
				}
				if exists {
					continue
				}
				if _, err = enqueueAIJob(ctx, conn, typ, jobScope{ProjectID: id}, "handoff", aiWorkerActor); err != nil {
					return err
				}
			}
		}
		if len(crossProjects) > 1 && len(snapshot.config.AnalysisModes) > 0 {
			exists, err := a.activeJobExists(ctx, conn, "analyze", "", true)
			if err != nil {
				return err
			}
			if !exists {
				if _, err = enqueueAIJob(ctx, conn, "analyze", jobScope{ProjectIDs: crossProjects, CrossProject: true}, "handoff", aiWorkerActor); err != nil {
					return err
				}
			}
		}
		next := map[string]any{"nextAt": now + int64(snapshot.config.ScheduleMinutes)*60000}
		if previous == nil {
			return insert(ctx, conn, "ai_schedule", "global", next)
		}
		previous["nextAt"] = next["nextAt"]
		return update(ctx, conn, "ai_schedule", previous)
	})
}
