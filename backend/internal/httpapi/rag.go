package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

// RAGStatus reports feature availability separately from ordinary persistence
// readiness and, subsequently, per-project indexing coverage.
func (s *SSH) RAGStatus(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, err := apiActor(ctx, s.Store.DB, r, s.Origin); err != nil {
			s.fail(w, err)
			return
		}
		reason := "disabled"
		available, version := false, ""
		if enabled {
			reason = "postgres_required"
			if s.Store.Engine == "postgres" {
				reason = "pgvector_required"
				err := s.Store.DB.QueryRowContext(ctx, `SELECT extversion FROM pg_catalog.pg_extension WHERE extname='vector' AND extnamespace=(SELECT oid FROM pg_catalog.pg_namespace WHERE nspname=current_schema())`).Scan(&version)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					s.fail(w, err)
					return
				}
				if err == nil {
					row, err := readSettings(ctx, s.Store.DB, "ai_settings")
					if err != nil {
						s.fail(w, err)
						return
					}
					cfg, err := aiConfig(row)
					if err != nil {
						s.fail(w, err)
						return
					}
					if ragDimensionsAvailable(cfg) {
						available, reason = true, "available"
					} else {
						reason = "embedding_dimensions_unsupported"
					}
				}
			}
		}
		result := map[string]any{"enabled": enabled, "available": available, "database": s.Store.Engine, "reason": reason}
		if available {
			result["pgvectorVersion"] = version
		}
		JSON(w, 200, result)
	}
}
