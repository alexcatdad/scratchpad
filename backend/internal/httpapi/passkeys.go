package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/alexcatdad/scratchpad/backend/internal/persistence"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

func tokenID(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

// AuthorizeSetup issues an operator-held capability; only its digest is stored.
func AuthorizeSetup(ctx context.Context, store *persistence.Store, recovery bool) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	err = store.Write(ctx, func(conn *sql.Conn) error {
		owner, err := optional(ctx, conn, "owner", "owner")
		if err != nil {
			return err
		}
		if (owner != nil) != recovery {
			return &protocolError{"SETUP_DISABLED", "Use setup for a new owner or recover for an existing owner.", 409}
		}
		if _, err = conn.ExecContext(ctx, `DELETE FROM entities WHERE kind='setup_token'`); err != nil {
			return err
		}
		if err = insert(ctx, conn, "setup_token", tokenID(token), map[string]any{"recovery": recovery, "expiresAt": timestamp(time.Now().Add(15 * time.Minute))}); err != nil {
			return err
		}
		action := "owner.setup_authorized"
		if recovery {
			action = "owner.recovery_authorized"
		}
		return passkeyAudit(ctx, conn, action, "owner", "owner", map[string]any{"kind": "system"})
	})
	var protocol *protocolError
	if err != nil && !errors.As(err, &protocol) {
		return "", errors.New("DATABASE_NOT_READY")
	}
	return token, err
}

func passkeyAudit(ctx context.Context, conn *sql.Conn, action, kind, id string, actor map[string]any) error {
	auditID, err := entityID("audit")
	if err != nil {
		return err
	}
	return insert(ctx, conn, "audit", auditID, map[string]any{"action": action, "entityType": kind, "entityId": id, "actor": actor})
}

func AuthStatus(store *persistence.Store, githubConfigured bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		tx, err := store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			Error(w, 503, "DATABASE_NOT_READY", "Persistence is unavailable.")
			return
		}
		defer tx.Rollback()
		owner, err := optional(ctx, tx, "owner", "owner")
		if err != nil {
			Error(w, 503, "DATABASE_NOT_READY", "Persistence is unavailable.")
			return
		}
		authErr := authorize(ctx, tx, r)
		if authErr != nil && !errors.Is(authErr, errAuthRequired) && !errors.Is(authErr, errUnauthorized) {
			Error(w, 503, "DATABASE_NOT_READY", "Persistence is unavailable.")
			return
		}
		JSON(w, 200, map[string]any{"initialized": owner != nil, "authenticated": authErr == nil, "githubConfigured": githubConfigured})
	}
}

func entityMap(ctx context.Context, q entityReader, kind, id string) (map[string]any, error) {
	data, err := get(ctx, q, kind, id)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	err = json.Unmarshal(data, &value)
	return value, err
}

type passkeyOwner struct {
	id          []byte
	credentials []webauthn.Credential
}

func (u passkeyOwner) WebAuthnID() []byte                         { return u.id }
func (u passkeyOwner) WebAuthnName() string                       { return "owner" }
func (u passkeyOwner) WebAuthnDisplayName() string                { return "Owner" }
func (u passkeyOwner) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

type Passkeys struct {
	auth *SSH
	web  *webauthn.WebAuthn
}

func NewPasskeys(auth *SSH, rpID string) (*Passkeys, error) {
	web, err := webauthn.New(&webauthn.Config{RPID: rpID, RPDisplayName: "Scratchpad", RPOrigins: []string{auth.Origin}, AttestationPreference: protocol.PreferNoAttestation, AuthenticatorSelection: protocol.AuthenticatorSelection{ResidentKey: protocol.ResidentKeyRequirementRequired, UserVerification: protocol.VerificationRequired}})
	return &Passkeys{auth: auth, web: web}, err
}

type passkeyRequest struct {
	SetupToken  string          `json:"setupToken"`
	ChallengeID string          `json:"challengeId"`
	Label       string          `json:"label"`
	Response    json.RawMessage `json:"response"`
}

func (p *Passkeys) Handler(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != p.auth.Origin {
			Error(w, 403, "AUTH_INVALID", "Request origin does not match this instance.")
			return
		}
		if p.web == nil {
			Error(w, 503, "CONFIG_INVALID", "Passkeys require a public hostname; use localhost for local development.")
			return
		}
		client, _, _ := net.SplitHostPort(r.RemoteAddr)
		release, err := p.auth.admission(client, strings.HasSuffix(operation, "verify"))
		if err != nil {
			p.auth.fail(w, err)
			return
		}
		defer release()
		var body passkeyRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		if err := decoder.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			Error(w, 400, "VALIDATION_FAILED", "Invalid JSON request.")
			return
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			Error(w, 400, "VALIDATION_FAILED", "Invalid JSON request.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		var result any
		var token string
		switch operation {
		case "register/options":
			result, err = p.registrationOptions(ctx, r, body)
		case "register/verify":
			result, token, err = p.registrationVerify(ctx, r, body)
		case "login/options":
			result, err = p.loginOptions(ctx)
		case "login/verify":
			result, token, err = p.loginVerify(ctx, body)
		}
		if err != nil {
			p.auth.fail(w, err)
			return
		}
		if token != "" {
			p.cookie(w, token, 86400)
		}
		JSON(w, 200, result)
	}
}
func (p *Passkeys) cookie(w http.ResponseWriter, token string, age int) {
	http.SetCookie(w, &http.Cookie{Name: "scratchpad_session", Value: token, HttpOnly: true, SameSite: http.SameSiteStrictMode, Path: "/", MaxAge: age, Secure: strings.HasPrefix(p.auth.Origin, "https:")})
}
func setupCapability(ctx context.Context, q entityReader, token string) (map[string]any, error) {
	if token == "" || len(token) > 500 {
		return nil, invalidProof("AUTH_REQUIRED", "Provide an owner setup or recovery token.")
	}
	value, err := entityMap(ctx, q, "setup_token", tokenID(token))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, invalidProof("AUTH_INVALID", "Invalid setup or recovery token.")
	}
	if err != nil {
		return nil, err
	}
	expires, _ := time.Parse(time.RFC3339Nano, stringValue(value, "expiresAt"))
	if !expires.After(time.Now()) {
		return nil, invalidProof("AUTH_INVALID", "Invalid setup or recovery token.")
	}
	owner, err := optional(ctx, q, "owner", "owner")
	if err != nil {
		return nil, err
	}
	if owner != nil && value["recovery"] != true {
		return nil, &protocolError{"SETUP_DISABLED", "Owner setup has already completed.", 409}
	}
	return value, nil
}
func stringValue(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return result
}
func browserCredential(ctx context.Context, q entityReader, r *http.Request) (string, error) {
	if bearerPattern.MatchString(r.Header.Get("Authorization")) {
		return "", &protocolError{"AUTH_INVALID", "Enroll passkeys through an authenticated browser.", 403}
	}
	if err := authorize(ctx, q, r); err != nil {
		return "", invalidProof("AUTH_INVALID", "Sign in to Scratchpad.")
	}
	cookie, err := r.Cookie("scratchpad_session")
	if err != nil {
		return "", invalidProof("AUTH_REQUIRED", "Sign in to Scratchpad.")
	}
	value, err := entityMap(ctx, q, "session", tokenID(cookie.Value))
	return stringValue(value, "credentialId"), err
}
func ownerUser(ctx context.Context, q entityReader) (passkeyOwner, error) {
	data, err := optional(ctx, q, "owner", "owner")
	if err != nil {
		return passkeyOwner{}, err
	}
	id := "scratchpad-owner"
	if data != nil {
		var owner map[string]any
		if err := json.Unmarshal(data, &owner); err != nil {
			return passkeyOwner{}, err
		}
		id = stringValue(owner, "userId")
	}
	return passkeyOwner{id: []byte(id)}, nil
}
func entityList(ctx context.Context, conn interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, kind string) ([]map[string]any, error) {
	rows, err := conn.QueryContext(ctx, `SELECT data FROM entities WHERE kind=$1 ORDER BY created_at,id`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
func (p *Passkeys) registrationOptions(ctx context.Context, r *http.Request, body passkeyRequest) (any, error) {
	var result any
	err := p.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		if err := p.retireChallenges(ctx, conn); err != nil {
			return err
		}
		capabilityID, credentialID := "", ""
		if body.SetupToken != "" {
			value, err := setupCapability(ctx, conn, body.SetupToken)
			if err != nil {
				return err
			}
			capabilityID = stringValue(value, "id")
		} else {
			var err error
			credentialID, err = browserCredential(ctx, conn, r)
			if err != nil {
				return err
			}
		}
		user, err := ownerUser(ctx, conn)
		if err != nil {
			return err
		}
		credentials, err := entityList(ctx, conn, "credential")
		if err != nil {
			return err
		}
		exclusions := []protocol.CredentialDescriptor{}
		for _, c := range credentials {
			if c["kind"] == "webauthn" && stringValue(c, "revokedAt") == "" {
				id, err := base64.RawURLEncoding.DecodeString(stringValue(c, "webauthnId"))
				if err != nil {
					return err
				}
				exclusions = append(exclusions, protocol.CredentialDescriptor{Type: protocol.PublicKeyCredentialType, CredentialID: id})
			}
		}
		options, session, err := p.web.BeginRegistration(user, webauthn.WithExclusions(exclusions))
		if err != nil {
			return err
		}
		id, err := entityID("challenge")
		if err != nil {
			return err
		}
		label := body.Label
		if label == "" {
			label = "Passkey"
		}
		label = string([]rune(label)[:min(len([]rune(label)), 200)])
		err = insert(ctx, conn, "challenge", id, map[string]any{"kind": "registration", "nonce": session.Challenge, "expiresAt": timestamp(time.Now().Add(2 * time.Minute)), "setupTokenId": capabilityID, "credentialId": credentialID, "label": label, "webauthnSession": session})
		result = map[string]any{"challengeId": id, "options": options.Response}
		return err
	})
	return result, err
}
func passkeyChallenge(ctx context.Context, q entityReader, id, kind string) (map[string]any, error) {
	if id == "" || len(id) > 500 {
		return nil, invalidProof("AUTH_INVALID", "Unknown authentication challenge.")
	}
	value, err := entityMap(ctx, q, "challenge", id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, invalidProof("AUTH_INVALID", "Unknown authentication challenge.")
	}
	if err != nil {
		return nil, err
	}
	if value["kind"] != kind {
		return nil, invalidProof("AUTH_INVALID", "Invalid authentication challenge.")
	}
	if stringValue(value, "consumedAt") != "" {
		return nil, invalidProof("AUTH_CHALLENGE_USED", "Authentication challenge has already been used.")
	}
	expires, _ := time.Parse(time.RFC3339Nano, stringValue(value, "expiresAt"))
	if !expires.After(time.Now()) {
		return nil, invalidProof("AUTH_CHALLENGE_EXPIRED", "Authentication challenge has expired.")
	}
	return value, nil
}
func registrationContext(ctx context.Context, q entityReader, r *http.Request, body passkeyRequest, challenge map[string]any) (map[string]any, error) {
	if id := stringValue(challenge, "setupTokenId"); id != "" {
		capability, err := setupCapability(ctx, q, body.SetupToken)
		if err != nil {
			return nil, err
		}
		if capability["id"] != id {
			return nil, invalidProof("AUTH_INVALID", "Registration context mismatch.")
		}
		return capability, nil
	}
	id, err := browserCredential(ctx, q, r)
	if err != nil {
		return nil, err
	}
	if id != challenge["credentialId"] {
		return nil, invalidProof("AUTH_INVALID", "Registration context mismatch.")
	}
	return nil, nil
}
func (p *Passkeys) registrationVerify(ctx context.Context, r *http.Request, body passkeyRequest) (any, string, error) {
	tx, err := p.auth.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, "", err
	}
	challenge, err := passkeyChallenge(ctx, tx, body.ChallengeID, "registration")
	if err != nil {
		tx.Rollback()
		return nil, "", err
	}
	_, err = registrationContext(ctx, tx, r, body, challenge)
	if err != nil {
		tx.Rollback()
		return nil, "", err
	}
	user, err := ownerUser(ctx, tx)
	tx.Rollback()
	if err != nil {
		return nil, "", err
	}
	var ceremony webauthn.SessionData
	if raw, ok := challenge["webauthnSession"]; ok {
		data, _ := json.Marshal(raw)
		if err := json.Unmarshal(data, &ceremony); err != nil {
			return nil, "", err
		}
	} else {
		// TypeScript challenges remain usable across the backend upgrade.
		ceremony = webauthn.SessionData{Challenge: stringValue(challenge, "nonce"), UserID: user.id, UserVerification: protocol.VerificationRequired, CredParams: webauthn.CredentialParametersDefault()}
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(body.Response)
	if err != nil {
		return nil, "", invalidProof("AUTH_INVALID", "Passkey verification failed.")
	}
	credential, err := p.web.CreateCredential(user, ceremony, parsed)
	if err != nil {
		return nil, "", invalidProof("AUTH_INVALID", "Passkey verification failed.")
	}
	token, err := randomToken()
	if err != nil {
		return nil, "", err
	}
	expires := timestamp(time.Now().Add(24 * time.Hour))
	err = p.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		fresh, err := passkeyChallenge(ctx, conn, body.ChallengeID, "registration")
		if err != nil {
			return err
		}
		capability, err := registrationContext(ctx, conn, r, body, fresh)
		if err != nil {
			return err
		}
		credentials, err := entityList(ctx, conn, "credential")
		if err != nil {
			return err
		}
		webauthnID := base64.RawURLEncoding.EncodeToString(credential.ID)
		for _, c := range credentials {
			if c["webauthnId"] == webauthnID {
				return &protocolError{"CONFLICT", "Passkey already enrolled.", 409}
			}
		}
		fresh["consumedAt"] = timestamp(time.Now())
		if err := update(ctx, conn, "challenge", fresh); err != nil {
			return err
		}
		if capability != nil {
			if _, err := conn.ExecContext(ctx, `DELETE FROM entities WHERE kind='setup_token' AND id=$1`, capability["id"]); err != nil {
				return err
			}
		}
		owner, err := optional(ctx, conn, "owner", "owner")
		if err != nil {
			return err
		}
		if owner == nil {
			if err := insert(ctx, conn, "owner", "owner", map[string]any{"displayName": "Owner", "userId": "scratchpad-owner"}); err != nil {
				return err
			}
		}
		action := "credential.enrolled"
		if capability != nil && capability["recovery"] == true {
			action = "owner.recovered"
			if err := revokeGithubSessions(ctx, conn, nil, p.auth.Store.Engine); err != nil {
				return err
			}
		}
		id, err := entityID("credential")
		if err != nil {
			return err
		}
		value := map[string]any{"kind": "webauthn", "label": fresh["label"], "webauthnId": webauthnID, "publicKey": base64.RawURLEncoding.EncodeToString(credential.PublicKey), "counter": credential.Authenticator.SignCount, "transports": credential.Transport, "webauthnFlags": credential.Flags, "lastUsedAt": timestamp(time.Now())}
		if err := insert(ctx, conn, "credential", id, value); err != nil {
			return err
		}
		if err := passkeyAudit(ctx, conn, action, "credential", id, map[string]any{"kind": "user", "credentialFingerprint": id}); err != nil {
			return err
		}
		return insert(ctx, conn, "session", tokenID(token), map[string]any{"credentialId": id, "browser": true, "expiresAt": expires, "authenticatedAt": timestamp(time.Now())})
	})
	return map[string]any{"authenticated": true, "expiresAt": expires}, token, err
}

func (p *Passkeys) loginOptions(ctx context.Context) (any, error) {
	var result any
	err := p.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		if err := p.retireChallenges(ctx, conn); err != nil {
			return err
		}
		owner, err := optional(ctx, conn, "owner", "owner")
		if err != nil {
			return err
		}
		if owner == nil {
			return &protocolError{"SETUP_REQUIRED", "Complete owner setup first.", 409}
		}
		options, session, err := p.web.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
		if err != nil {
			return err
		}
		id, err := entityID("challenge")
		if err != nil {
			return err
		}
		err = insert(ctx, conn, "challenge", id, map[string]any{"kind": "login", "nonce": session.Challenge, "expiresAt": timestamp(time.Now().Add(2 * time.Minute)), "webauthnSession": session})
		result = map[string]any{"challengeId": id, "options": options.Response}
		return err
	})
	return result, err
}
func (p *Passkeys) loginVerify(ctx context.Context, body passkeyRequest) (any, string, error) {
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body.Response)
	if err != nil {
		return nil, "", invalidProof("AUTH_INVALID", "Passkey verification failed.")
	}
	tx, err := p.auth.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, "", err
	}
	challenge, err := passkeyChallenge(ctx, tx, body.ChallengeID, "login")
	if err != nil {
		tx.Rollback()
		return nil, "", err
	}
	user, err := ownerUser(ctx, tx)
	if err != nil {
		tx.Rollback()
		return nil, "", err
	}
	field := `json_extract(data,'$.webauthnId')`
	if p.auth.Store.Engine == "postgres" {
		field = "data->>'webauthnId'"
	}
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT data FROM entities WHERE kind='credential' AND `+field+`=$1`, base64.RawURLEncoding.EncodeToString(parsed.RawID)).Scan(&raw)
	tx.Rollback()
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", invalidProof("AUTH_INVALID", "Unknown passkey.")
	}
	if err != nil {
		return nil, "", err
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, "", err
	}
	if stored["kind"] != "webauthn" || stringValue(stored, "revokedAt") != "" {
		return nil, "", invalidProof("AUTH_INVALID", "Unknown passkey.")
	}
	key, err := base64.RawURLEncoding.DecodeString(stringValue(stored, "publicKey"))
	if err != nil {
		return nil, "", invalidProof("AUTH_INVALID", "Invalid passkey.")
	}
	counter, ok := stored["counter"].(float64)
	if !ok || counter < 0 || counter > math.MaxUint32 || math.Trunc(counter) != counter {
		return nil, "", invalidProof("AUTH_INVALID", "Invalid passkey counter.")
	}
	flags := webauthn.CredentialFlags{UserVerified: true, BackupEligible: parsed.Response.AuthenticatorData.Flags.HasBackupEligible()}
	if storedFlags, ok := stored["webauthnFlags"]; ok {
		data, _ := json.Marshal(storedFlags)
		if err := json.Unmarshal(data, &flags); err != nil {
			return nil, "", err
		}
	}
	// Earlier TypeScript rows do not contain backup flags. Their first verified
	// assertion initializes them; subsequent assertions enforce consistency.
	user.credentials = []webauthn.Credential{{ID: parsed.RawID, PublicKey: key, Flags: flags, Authenticator: webauthn.Authenticator{SignCount: uint32(counter)}}}
	ceremony := webauthn.SessionData{Challenge: stringValue(challenge, "nonce"), UserVerification: protocol.VerificationRequired}
	if storedSession, ok := challenge["webauthnSession"]; ok {
		data, _ := json.Marshal(storedSession)
		if err := json.Unmarshal(data, &ceremony); err != nil {
			return nil, "", err
		}
	}
	verified, err := p.web.ValidateDiscoverableLogin(func(rawID, userHandle []byte) (webauthn.User, error) {
		if !bytes.Equal(rawID, parsed.RawID) || !bytes.Equal(userHandle, user.id) {
			return nil, errUnauthorized
		}
		return user, nil
	}, ceremony, parsed)
	if err != nil || verified.Authenticator.CloneWarning {
		return nil, "", invalidProof("AUTH_INVALID", "Passkey verification failed.")
	}
	token, err := randomToken()
	if err != nil {
		return nil, "", err
	}
	expires := timestamp(time.Now().Add(24 * time.Hour))
	err = p.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		freshChallenge, err := passkeyChallenge(ctx, conn, body.ChallengeID, "login")
		if err != nil {
			return err
		}
		fresh, err := entityMap(ctx, conn, "credential", stringValue(stored, "id"))
		if errors.Is(err, sql.ErrNoRows) {
			return invalidProof("AUTH_INVALID", "Credential changed. Please sign in again.")
		}
		if err != nil {
			return err
		}
		if stringValue(fresh, "revokedAt") != "" || fresh["version"] != stored["version"] {
			return invalidProof("AUTH_INVALID", "Credential changed. Please sign in again.")
		}
		freshChallenge["consumedAt"] = timestamp(time.Now())
		if err := update(ctx, conn, "challenge", freshChallenge); err != nil {
			return err
		}
		fresh["counter"], fresh["webauthnFlags"], fresh["lastUsedAt"] = verified.Authenticator.SignCount, verified.Flags, timestamp(time.Now())
		if err := update(ctx, conn, "credential", fresh); err != nil {
			return err
		}
		return insert(ctx, conn, "session", tokenID(token), map[string]any{"credentialId": fresh["id"], "browser": true, "expiresAt": expires, "authenticatedAt": timestamp(time.Now())})
	})
	return map[string]any{"authenticated": true, "expiresAt": expires}, token, err
}

func (p *Passkeys) Logout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bearer := bearerPattern.FindStringSubmatch(r.Header.Get("Authorization"))
		if len(bearer) != 2 && r.Header.Get("Origin") != p.auth.Origin {
			Error(w, 403, "AUTH_INVALID", "Request origin does not match this instance.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		err := p.auth.Store.Write(ctx, func(conn *sql.Conn) error {
			if err := authorize(ctx, conn, r); err != nil {
				return invalidProof("AUTH_INVALID", "Sign in to Scratchpad.")
			}
			token := ""
			if len(bearer) == 2 {
				token = bearer[1]
			} else {
				cookie, _ := r.Cookie("scratchpad_session")
				token = cookie.Value
			}
			value, err := entityMap(ctx, conn, "session", tokenID(token))
			if err != nil {
				return err
			}
			value["revokedAt"] = timestamp(time.Now())
			return update(ctx, conn, "session", value)
		})
		if err != nil {
			p.auth.fail(w, err)
			return
		}
		p.cookie(w, "", -1)
		JSON(w, 200, map[string]any{"authenticated": false})
	}
}

func (p *Passkeys) Credentials() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		tx, err := p.auth.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			p.auth.fail(w, err)
			return
		}
		defer tx.Rollback()
		if err := authorize(ctx, tx, r); err != nil {
			p.auth.fail(w, invalidProof("AUTH_INVALID", "Sign in to Scratchpad."))
			return
		}
		credentials, err := entityList(ctx, tx, "credential")
		if err != nil {
			p.auth.fail(w, err)
			return
		}
		result := []map[string]any{}
		for _, credential := range credentials {
			value := map[string]any{"source": "local"}
			for _, key := range []string{"id", "kind", "label", "fingerprint", "createdAt", "lastUsedAt", "revokedAt", "source"} {
				if field, ok := credential[key]; ok {
					value[key] = field
				}
			}
			result = append(result, value)
		}
		JSON(w, 200, map[string]any{"credentials": result})
	}
}

func (p *Passkeys) retireChallenges(ctx context.Context, conn *sql.Conn) error {
	field := `json_extract(data,'$.expiresAt')`
	if p.auth.Store.Engine == "postgres" {
		field = "data->>'expiresAt'"
	}
	_, err := conn.ExecContext(ctx, `DELETE FROM entities WHERE kind='challenge' AND `+field+` < $1`, timestamp(time.Now()))
	return err
}
