package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

func (g *Github) Management(unlink bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != g.auth.Origin {
			Error(w, 403, "AUTH_INVALID", "Invalid request origin.")
			return
		}
		client := g.auth.requestClient(r)
		release, err := g.auth.admission(client, false)
		if err != nil {
			g.auth.fail(w, err)
			return
		}
		defer release()
		key := ""
		var blocked *bool
		if !unlink {
			var body struct {
				PublicKey string `json:"publicKey"`
				Blocked   *bool  `json:"blocked"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
			if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || body.Blocked == nil {
				Error(w, 400, "VALIDATION_FAILED", "Provide a public key and block state.")
				return
			}
			key, err = normalizedKey(body.PublicKey)
			if err != nil || githubFingerprint(key) == "" {
				Error(w, 400, "VALIDATION_FAILED", "Provide a valid OpenSSH public key.")
				return
			}
			blocked = body.Blocked
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		err = g.auth.Store.Write(ctx, func(conn *sql.Conn) error {
			session, cred, err := githubBrowser(ctx, conn, r)
			if err != nil {
				return err
			}
			if unlink {
				if err = freshGithub(session, cred, true); err != nil {
					return err
				}
				if err = g.invalidate(ctx, conn); err != nil {
					return err
				}
				if _, err = conn.ExecContext(ctx, `DELETE FROM entities WHERE kind=$1 AND id=$2`, "github_binding", "owner"); err != nil {
					return err
				}
				return passkeyAudit(ctx, conn, "github.unlinked", "owner", "owner", map[string]any{"kind": "user"})
			}
			fingerprint := githubFingerprint(key)
			existing, err := optional(ctx, conn, "github_block", fingerprint)
			if err != nil {
				return err
			}
			if *blocked && existing == nil {
				if err = insert(ctx, conn, "github_block", fingerprint, map[string]any{}); err != nil {
					return err
				}
			}
			if !*blocked && existing != nil {
				if _, err = conn.ExecContext(ctx, `DELETE FROM entities WHERE kind=$1 AND id=$2`, "github_block", fingerprint); err != nil {
					return err
				}
			}
			if *blocked {
				credentials, err := entityList(ctx, conn, "credential")
				if err != nil {
					return err
				}
				ids := map[string]bool{}
				for _, cred := range credentials {
					if cred["kind"] != "webauthn" {
						normalized, err := normalizedKey(stringValue(cred, "publicKey"))
						if err == nil && normalized == key {
							ids[stringValue(cred, "id")] = true
						}
					}
				}
				if err = revokeGithubSessions(ctx, conn, ids, g.auth.Store.Engine); err != nil {
					return err
				}
			}
			action := "github.key_unblocked"
			if *blocked {
				action = "github.key_blocked"
			}
			return passkeyAudit(ctx, conn, action, "credential", fingerprint, map[string]any{"kind": "user"})
		})
		if err != nil {
			g.auth.fail(w, err)
			return
		}
		if unlink {
			JSON(w, 200, map[string]any{"unlinked": true})
			return
		}
		result, err := g.status(ctx, r)
		if err != nil {
			g.auth.fail(w, err)
			return
		}
		JSON(w, 200, result)
	}
}
