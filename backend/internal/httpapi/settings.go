package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

func publicSettings(row map[string]any, enabled bool) map[string]any {
	result := map[string]any{"id": "global", "version": float64(0), "aiEnabled": enabled}
	for _, field := range []string{"id", "version", "createdAt", "updatedAt", "defaultProjectSettings"} {
		if value, exists := row[field]; exists {
			result[field] = value
		}
	}
	return result
}
func readSettings(ctx context.Context, q entityReader, kind string) (map[string]any, error) {
	raw, err := optional(ctx, q, kind, "global")
	if err != nil || raw == nil {
		return nil, err
	}
	var row map[string]any
	err = json.Unmarshal(raw, &row)
	return row, err
}
func (a *AI) GlobalSettings(patch bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if !patch {
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
			row, err := readSettings(ctx, tx, "settings")
			if err != nil {
				a.auth.fail(w, err)
				return
			}
			provider, err := readSettings(ctx, tx, "ai_settings")
			if err != nil {
				a.auth.fail(w, err)
				return
			}
			cfg, err := aiConfig(provider)
			if err != nil {
				a.auth.fail(w, err)
				return
			}
			JSON(w, 200, map[string]any{"settings": publicSettings(row, cfg.Enabled)})
			return
		}
		if _, err := apiActor(ctx, a.auth.Store.DB, r, a.auth.Origin); err != nil {
			a.auth.fail(w, err)
			return
		}
		var body map[string]any
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024*1024))
		if decoder.Decode(&body) != nil || body == nil || decoder.Decode(new(any)) != io.EOF {
			a.auth.fail(w, aiInvalid())
			return
		}
		changes, ok := body["settings"].(map[string]any)
		if !ok {
			a.auth.fail(w, aiInvalid())
			return
		}
		parsed := map[string]any{}
		if value, exists := changes["aiEnabled"]; exists {
			flag, ok := value.(bool)
			if !ok {
				a.auth.fail(w, aiInvalid())
				return
			}
			parsed["aiEnabled"] = flag
		}
		if value, exists := changes["defaultProjectSettings"]; exists {
			settings, err := normalizeSettings(value)
			if err != nil {
				a.auth.fail(w, err)
				return
			}
			parsed["defaultProjectSettings"] = settings
		}
		var result map[string]any
		err := a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
			actor, err := apiActor(ctx, conn, r, a.auth.Origin)
			if err != nil {
				return err
			}
			previous, err := readSettings(ctx, conn, "settings")
			if err != nil {
				return err
			}
			if previous == nil && body["expectedVersion"] != float64(0) {
				return &protocolError{"CONFLICT", "Initial settings require expectedVersion 0.", 409}
			}
			next := map[string]any{}
			for key, value := range previous {
				next[key] = value
			}
			for key, value := range parsed {
				next[key] = value
			}
			var before map[string]any
			if previous != nil {
				before = publicSettings(previous, previous["aiEnabled"] == true)
				expected, err := expectedVersion(body, r)
				if err != nil {
					return err
				}
				if previous["version"] != expected {
					return &versionConflict{previous["version"]}
				}
				err = update(ctx, conn, "settings", next)
				if err != nil {
					return err
				}
			} else {
				if err = insert(ctx, conn, "settings", "global", next); err != nil {
					return err
				}
			}
			if err = apiAudit(ctx, conn, "settings.updated", "settings", "global", actor, before, publicSettings(next, next["aiEnabled"] == true)); err != nil {
				return err
			}
			provider, err := readSettings(ctx, conn, "ai_settings")
			if err != nil {
				return err
			}
			if value, exists := parsed["aiEnabled"]; exists {
				version := float64(0)
				if provider != nil {
					version, _ = provider["version"].(float64)
				}
				if _, err = a.configure(ctx, conn, map[string]any{"enabled": value}, actor, version); err != nil {
					return err
				}
				provider, err = readSettings(ctx, conn, "ai_settings")
				if err != nil {
					return err
				}
			}
			cfg, err := aiConfig(provider)
			if err != nil {
				return err
			}
			result = publicSettings(next, cfg.Enabled)
			return nil
		})
		if err != nil {
			a.auth.fail(w, err)
			return
		}
		JSON(w, 200, map[string]any{"settings": result})
	}
}
