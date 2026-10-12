package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Normal schema migration never depends on a key. Conversion is a separate
// atomic transition; pending plaintext is never a provider fallback.
func (a *AI) ConvertLegacy(ctx context.Context) error {
	if !a.box.Available() {
		return nil
	}
	return a.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		raw, err := optional(ctx, conn, "ai_settings", "global")
		if err != nil || raw == nil {
			return err
		}
		var row map[string]any
		if json.Unmarshal(raw, &row) != nil {
			return aiInvalid()
		}
		legacy, ok := row["apiKey"].(string)
		if !ok || legacy == "" {
			return nil
		}
		sealed, err := a.box.Seal(legacy, "ai_settings", "global", "apiKey")
		if err != nil {
			return secretUnavailable()
		}
		row["apiKey"] = sealed
		generation, _ := row["dispatchGeneration"].(float64)
		row["dispatchGeneration"] = generation + 1
		row["secretConversion"] = map[string]any{"version": 1, "convertedAt": timestamp(time.Now())}
		if err = update(ctx, conn, "ai_settings", row); err != nil {
			return err
		}
		return apiAudit(ctx, conn, "secrets.converted", "ai_settings", "global", map[string]any{"kind": "system"}, map[string]any{"apiKeyConfigured": true, "protection": "legacy"}, map[string]any{"apiKeyConfigured": true, "protection": "encrypted", "conversionVersion": 1})
	})
}
