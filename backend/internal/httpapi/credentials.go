package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
	"unicode/utf8"
)

func (s *SSH) Enrollment(verify bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != s.Origin {
			Error(w, 403, "AUTH_INVALID", "Invalid request origin.")
			return
		}
		client, _, _ := net.SplitHostPort(r.RemoteAddr)
		release, err := s.admission(client, verify)
		if err != nil {
			s.fail(w, err)
			return
		}
		defer release()
		var body struct {
			PublicKey   string  `json:"publicKey"`
			ChallengeID string  `json:"challengeId"`
			Signature   string  `json:"signature"`
			Label       *string `json:"label"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
			Error(w, 400, "VALIDATION_ERROR", "Invalid enrollment request.")
			return
		}
		key, err := normalizedKey(body.PublicKey)
		if err != nil {
			s.fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		initiating, err := browserCredential(ctx, s.Store.DB, r)
		if err != nil {
			s.fail(w, err)
			return
		}
		if !verify {
			id, err := entityID("challenge")
			if err != nil {
				s.fail(w, err)
				return
			}
			nonce, err := randomToken()
			if err != nil {
				s.fail(w, err)
				return
			}
			expiry := timestamp(time.Now().Add(2 * time.Minute))
			label := "MCP key"
			if body.Label != nil {
				label = *body.Label
				if utf8.RuneCountInString(label) > 200 {
					label = string([]rune(label)[:200])
				}
			}
			err = s.Store.Write(ctx, func(conn *sql.Conn) error {
				credential, err := browserCredential(ctx, conn, r)
				if err != nil {
					return err
				}
				if credential != initiating {
					return invalidProof("AUTH_INVALID", "Enrollment authorization changed.")
				}
				// Expired enrollment and login challenges share the same bounded lifecycle.
				field := `json_extract(data,'$.expiresAt')`
				if s.Store.Engine == "postgres" {
					field = "data->>'expiresAt'"
				}
				if _, err = conn.ExecContext(ctx, `DELETE FROM entities WHERE kind='challenge' AND `+field+` < $1`, timestamp(time.Now())); err != nil {
					return err
				}
				return insert(ctx, conn, "challenge", id, map[string]any{"kind": "ssh_enroll", "proofVersion": 2, "recipient": s.Origin, "publicKey": key, "enrolledBy": credential, "nonce": nonce, "expiresAt": expiry, "label": label})
			})
			if err != nil {
				s.fail(w, err)
				return
			}
			JSON(w, 200, map[string]any{"challengeId": id, "version": 2, "recipient": s.Origin, "purpose": "ssh_enroll", "nonce": nonce, "namespace": sshNamespace, "expiresAt": expiry})
			return
		}
		if len(body.ChallengeID) == 0 || len(body.ChallengeID) > 500 || len(body.Signature) > 32768 {
			Error(w, 400, "VALIDATION_ERROR", "Invalid enrollment proof.")
			return
		}
		challenge, err := s.loadSSHChallenge(ctx, s.Store.DB, body.ChallengeID, key, "ssh_enroll")
		if err != nil {
			s.fail(w, err)
			return
		}
		if challenge["enrolledBy"] != initiating {
			s.fail(w, invalidProof("AUTH_INVALID", "Enrollment belongs to another credential."))
			return
		}
		if err = s.verifySignature(ctx, body.ChallengeID, key, "ssh_enroll", body.Signature, challenge); err != nil {
			s.fail(w, err)
			return
		}
		id, err := entityID("credential")
		if err != nil {
			s.fail(w, err)
			return
		}
		fingerprint := githubFingerprint(key)
		err = s.Store.Write(ctx, func(conn *sql.Conn) error {
			credential, err := browserCredential(ctx, conn, r)
			if err != nil {
				return err
			}
			if credential != initiating {
				return invalidProof("AUTH_INVALID", "Enrollment authorization changed.")
			}
			challenge, err := s.loadSSHChallenge(ctx, conn, body.ChallengeID, key, "ssh_enroll")
			if err != nil {
				return err
			}
			if challenge["enrolledBy"] != credential {
				return invalidProof("AUTH_INVALID", "Enrollment authorization changed.")
			}
			credentials, err := entityList(ctx, conn, "credential")
			if err != nil {
				return err
			}
			for _, existing := range credentials {
				if existing["kind"] == "ssh" && existing["publicKey"] == key && (existing["source"] == "github" || stringValue(existing, "revokedAt") == "") {
					return &protocolError{"CONFLICT", "Key is already enrolled or GitHub-managed.", 409}
				}
			}
			raw, err := optional(ctx, conn, "github_binding", "owner")
			if err != nil {
				return err
			}
			if raw != nil {
				var binding struct {
					Keys []githubKey `json:"keys"`
				}
				if err = json.Unmarshal(raw, &binding); err != nil {
					return err
				}
				for _, published := range binding.Keys {
					if published.PublicKey == key {
						return &protocolError{"CONFLICT", "GitHub-managed keys cannot become local credentials.", 409}
					}
				}
			}
			if err = insert(ctx, conn, "credential", id, map[string]any{"kind": "ssh", "source": "local", "publicKey": key, "label": challenge["label"], "fingerprint": fingerprint}); err != nil {
				return err
			}
			challenge["consumedAt"] = timestamp(time.Now())
			if err = update(ctx, conn, "challenge", challenge); err != nil {
				return err
			}
			return passkeyAudit(ctx, conn, "credential.enrolled", "credential", id, map[string]any{"kind": "user"})
		})
		if err != nil {
			s.fail(w, err)
			return
		}
		JSON(w, 200, map[string]any{"credentialId": id, "fingerprint": fingerprint})
	}
}

// Revocation always retains the credential and its provenance.
func (s *SSH) Revoke(configured bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != s.Origin {
			Error(w, 403, "AUTH_INVALID", "Invalid request origin.")
			return
		}
		client, _, _ := net.SplitHostPort(r.RemoteAddr)
		release, err := s.admission(client, false)
		if err != nil {
			s.fail(w, err)
			return
		}
		defer release()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		err = s.Store.Write(ctx, func(conn *sql.Conn) error {
			if _, err := browserCredential(ctx, conn, r); err != nil {
				return err
			}
			credential, err := entityMap(ctx, conn, "credential", r.PathValue("id"))
			if errors.Is(err, sql.ErrNoRows) {
				return &protocolError{"NOT_FOUND", "Credential not found.", 404}
			}
			if err != nil {
				return err
			}
			if credential["source"] == "github" {
				return &protocolError{"CONFLICT", "Use GitHub key blocking or unlinking to revoke managed access.", 409}
			}
			if credential["kind"] == "webauthn" {
				bound, err := optional(ctx, conn, "github_binding", "owner")
				if err != nil {
					return err
				}
				alternative := configured && bound != nil
				credentials, err := entityList(ctx, conn, "credential")
				if err != nil {
					return err
				}
				for _, other := range credentials {
					if other["kind"] == "webauthn" && other["id"] != credential["id"] && stringValue(other, "revokedAt") == "" {
						alternative = true
					}
				}
				if !alternative {
					return &protocolError{"CONFLICT", "Enroll another passkey before revoking the last passkey.", 409}
				}
			}
			credential["revokedAt"] = timestamp(time.Now())
			if err = update(ctx, conn, "credential", credential); err != nil {
				return err
			}
			if err = revokeGithubSessions(ctx, conn, map[string]bool{stringValue(credential, "id"): true}, s.Store.Engine); err != nil {
				return err
			}
			return passkeyAudit(ctx, conn, "credential.revoked", "credential", stringValue(credential, "id"), map[string]any{"kind": "user"})
		})
		if err != nil {
			s.fail(w, err)
			return
		}
		JSON(w, 200, map[string]any{"revoked": true})
	}
}
