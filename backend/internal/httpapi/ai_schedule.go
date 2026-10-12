package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"time"
)

type activeAIJob struct {
	typ   string
	scope jobScope
}

func (a *AI) activeJobs(ctx context.Context, conn *sql.Conn) ([]activeAIJob, error) {
	status := "json_extract(data,'$.status')"
	if a.auth.Store.Engine == "postgres" {
		status = "data->>'status'"
	}
	rows, err := conn.QueryContext(ctx, "SELECT data FROM entities WHERE kind='ai_job' AND "+status+" IN ('queued','running')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []activeAIJob{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var job map[string]any
		if err = json.Unmarshal(raw, &job); err != nil {
			return nil, err
		}
		scope, err := aiScope(job)
		if err != nil {
			continue
		}
		if scope.ProjectIDs == nil && scope.ProjectID != "" {
			scope.ProjectIDs = []string{scope.ProjectID}
		}
		jobs = append(jobs, activeAIJob{stringValue(job, "type"), scope})
	}
	return jobs, rows.Err()
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
			deadline, ok := previous["nextAt"].(float64)
			if !ok || math.IsNaN(deadline) || math.IsInf(deadline, 0) || deadline != math.Trunc(deadline) || deadline < 0 {
				return aiInvalid()
			}
			if float64(now) < deadline {
				return nil
			}
		}
		active, err := a.activeJobs(ctx, conn)
		if err != nil {
			return err
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
				exists := false
				for _, job := range active {
					if job.typ == typ && contains(job.scope.ProjectIDs, id) {
						exists = true
						break
					}
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
			exists := false
			for _, job := range active {
				if job.typ == "analyze" && job.scope.CrossProject {
					exists = true
					break
				}
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
