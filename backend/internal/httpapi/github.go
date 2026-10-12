package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Github struct {
	auth             *SSH
	passkeys         *Passkeys
	clientID, secret string
}

func NewGithub(auth *SSH, passkeys *Passkeys, clientID, secret string) (*Github, error) {
	if (clientID != "" || secret != "") && (strings.TrimSpace(clientID) == "" || strings.TrimSpace(secret) == "") {
		return nil, errors.New("CONFIG_INVALID: both GitHub OAuth values are required")
	}
	return &Github{auth, passkeys, clientID, secret}, nil
}
func (g *Github) Configured() bool { return g.clientID != "" && g.secret != "" }
func (g *Github) stateCookie(w http.ResponseWriter, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: "scratchpad_github_state", Value: value, HttpOnly: true, SameSite: http.SameSiteLaxMode, Path: "/api/v1/auth/github", MaxAge: age, Secure: strings.HasPrefix(g.auth.Origin, "https:")})
}
func githubInvalid() error {
	return invalidProof("AUTH_INVALID", "GitHub authorization is invalid, expired or revoked.")
}
func freshGithub(session, cred map[string]any, independent bool) error {
	at := stringValue(session, "authenticatedAt")
	if at == "" {
		at = stringValue(session, "createdAt")
	}
	parsed, err := time.Parse(time.RFC3339Nano, at)
	age := time.Since(parsed)
	if err != nil || age < 0 || age >= 5*time.Minute || (independent && cred["source"] == "github") {
		return &protocolError{"AUTH_FRESH_REQUIRED", "Sign in again with an appropriate credential before changing GitHub access.", 403}
	}
	return nil
}
func githubBrowser(ctx context.Context, q entityReader, r *http.Request) (map[string]any, map[string]any, error) {
	id, err := browserCredential(ctx, q, r)
	if err != nil {
		return nil, nil, err
	}
	cookie, _ := r.Cookie("scratchpad_session")
	session, err := entityMap(ctx, q, "session", tokenID(cookie.Value))
	if err != nil {
		return nil, nil, err
	}
	cred, err := entityMap(ctx, q, "credential", id)
	return session, cred, err
}
func (g *Github) Options() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !g.Configured() {
			Error(w, 409, "GITHUB_NOT_CONFIGURED", "The administrator has not configured GitHub sign-in.")
			return
		}
		if r.Header.Get("Origin") != g.auth.Origin {
			Error(w, 403, "AUTH_INVALID", "Invalid request origin.")
			return
		}
		client, _, _ := net.SplitHostPort(r.RemoteAddr)
		release, err := g.auth.admission(client, false)
		if err != nil {
			g.auth.fail(w, err)
			return
		}
		defer release()
		var body struct {
			Intent     string `json:"intent"`
			SetupToken string `json:"setupToken"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
			Error(w, 400, "VALIDATION_FAILED", "Invalid GitHub options.")
			return
		}
		switch body.Intent {
		case "setup", "recover", "link", "replace", "login":
		default:
			Error(w, 400, "VALIDATION_FAILED", "Invalid GitHub intent.")
			return
		}
		state, err := randomToken()
		if err != nil {
			g.auth.fail(w, err)
			return
		}
		verifier, err := randomToken()
		if err != nil {
			g.auth.fail(w, err)
			return
		}
		nonce, err := randomToken()
		if err != nil {
			g.auth.fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		err = g.auth.Store.Write(ctx, func(conn *sql.Conn) error {
			entry := map[string]any{"intent": body.Intent, "verifier": verifier, "browserHash": tokenID(nonce), "expiresAt": timestamp(time.Now().Add(10 * time.Minute))}
			bound, err := optional(ctx, conn, "github_binding", "owner")
			if err != nil {
				return err
			}
			if bound != nil {
				var b map[string]any
				if err = json.Unmarshal(bound, &b); err != nil {
					return err
				}
				entry["bindingGeneration"] = b["generation"]
			}
			if body.Intent == "setup" || body.Intent == "recover" {
				cap, err := setupCapability(ctx, conn, body.SetupToken)
				if err != nil {
					return err
				}
				if (cap["recovery"] == true) != (body.Intent == "recover") {
					return githubInvalid()
				}
				entry["setupTokenId"] = cap["id"]
			} else if body.Intent == "login" {
				if bound == nil {
					return githubInvalid()
				}
			} else {
				session, cred, err := githubBrowser(ctx, conn, r)
				if err != nil {
					return err
				}
				if body.Intent == "replace" {
					if err = freshGithub(session, cred, false); err != nil {
						return err
					}
				}
				if body.Intent == "link" && bound != nil {
					return &protocolError{"CONFLICT", "A GitHub account is already linked.", 409}
				}
				entry["credentialId"], entry["sessionId"] = cred["id"], session["id"]
			}
			entries, err := entityList(ctx, conn, "github_oauth")
			if err != nil {
				return err
			}
			for _, old := range entries {
				expiry, _ := time.Parse(time.RFC3339Nano, stringValue(old, "expiresAt"))
				if !expiry.After(time.Now()) {
					if _, err = conn.ExecContext(ctx, `DELETE FROM entities WHERE kind=$1 AND id=$2`, "github_oauth", old["id"]); err != nil {
						return err
					}
				}
			}
			return insert(ctx, conn, "github_oauth", tokenID(state), entry)
		})
		if err != nil {
			g.auth.fail(w, err)
			return
		}
		digest := sha256.Sum256([]byte(verifier))
		query := url.Values{"client_id": {g.clientID}, "redirect_uri": {g.auth.Origin + "/api/v1/auth/github/callback"}, "scope": {""}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"}, "prompt": {"select_account"}}
		g.stateCookie(w, nonce, 600)
		JSON(w, 200, map[string]any{"authorizationUrl": "https://github.com/login/oauth/authorize?" + query.Encode()})
	}
}

func githubUnavailable() error {
	return &protocolError{"GITHUB_UNAVAILABLE", "GitHub is unavailable or returned an invalid response. Use an independent credential or try again later.", 503}
}
func (g *Github) request(ctx context.Context, target string, form url.Values, bearer string) (json.RawMessage, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	method := "GET"
	var body io.Reader
	if form != nil {
		method = "POST"
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, "", githubUnavailable()
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	req.Header.Set("User-Agent", "Scratchpad-owner-access")
	if form != nil {
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	} else if req.URL.Host == "api.github.com" && g.Configured() {
		req.SetBasicAuth(g.clientID, g.secret)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, "", githubUnavailable()
	}
	defer res.Body.Close()
	if res.StatusCode == 403 || res.StatusCode == 429 {
		return nil, "", &protocolError{"GITHUB_RATE_LIMITED", "GitHub is limiting requests. Try again later.", 503}
	}
	if res.StatusCode != http.StatusOK {
		return nil, "", githubUnavailable()
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 || !json.Valid(data) {
		return nil, "", githubUnavailable()
	}
	links := res.Header.Values("Link")
	link := strings.Join(links, ",")
	if len(links) > 0 && strings.TrimSpace(link) == "" {
		return nil, "", githubUnavailable()
	}
	return data, link, nil
}

type githubAccount struct {
	ID    json.Number `json:"id"`
	Login string      `json:"login"`
	Type  string      `json:"type"`
}

var githubLoginPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)

func (g *Github) account(ctx context.Context, target, bearer string) (githubAccount, error) {
	data, _, err := g.request(ctx, target, nil, bearer)
	if err != nil {
		return githubAccount{}, err
	}
	var account githubAccount
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields["id"]) == 0 || fields["id"][0] == '"' || json.Unmarshal(data, &account) != nil {
		return account, githubUnavailable()
	}
	id, err := account.ID.Int64()
	account.Login = strings.TrimSpace(account.Login)
	if err != nil || id <= 0 || id > 9007199254740991 || account.Type != "User" || strings.Contains(account.Login, "--") || !githubLoginPattern.MatchString(account.Login) {
		return account, githubUnavailable()
	}
	return account, nil
}
func (g *Github) callbackAuthorization(ctx context.Context, q entityReader, entry map[string]any) error {
	if id := stringValue(entry, "sessionId"); id != "" {
		if authorizeSession(ctx, q, id, false) != nil {
			return githubInvalid()
		}
		session, err := entityMap(ctx, q, "session", id)
		if err != nil {
			return err
		}
		if session["credentialId"] != entry["credentialId"] {
			return githubInvalid()
		}
		if entry["intent"] == "replace" {
			cred, err := entityMap(ctx, q, "credential", stringValue(entry, "credentialId"))
			if err != nil {
				return err
			}
			if err = freshGithub(session, cred, false); err != nil {
				return err
			}
		}
	}
	if id := stringValue(entry, "setupTokenId"); id != "" {
		cap, err := entityMap(ctx, q, "setup_token", id)
		if errors.Is(err, sql.ErrNoRows) {
			return githubInvalid()
		}
		if err != nil {
			return err
		}
		expiry, _ := time.Parse(time.RFC3339Nano, stringValue(cap, "expiresAt"))
		owner, err := optional(ctx, q, "owner", "owner")
		if err != nil {
			return err
		}
		if !expiry.After(time.Now()) || (cap["recovery"] != true && owner != nil) || (cap["recovery"] == true) != (entry["intent"] == "recover") {
			return githubInvalid()
		}
	}
	return nil
}
func (g *Github) loadState(ctx context.Context, q entityReader, id, nonce string) (map[string]any, error) {
	entry, err := entityMap(ctx, q, "github_oauth", id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, githubInvalid()
	}
	if err != nil {
		return nil, err
	}
	expiry, _ := time.Parse(time.RFC3339Nano, stringValue(entry, "expiresAt"))
	if nonce == "" || entry["browserHash"] != tokenID(nonce) || stringValue(entry, "consumedAt") != "" || !expiry.After(time.Now()) {
		return nil, githubInvalid()
	}
	return entry, g.callbackAuthorization(ctx, q, entry)
}
func (g *Github) Callback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		client, _, _ := net.SplitHostPort(r.RemoteAddr)
		release, err := g.auth.admission(client, true)
		if err != nil {
			g.auth.fail(w, err)
			return
		}
		defer release()
		token, err := g.callback(r)
		g.stateCookie(w, "", -1)
		if err != nil {
			code := "AUTH_INVALID"
			var protocol *protocolError
			if errors.As(err, &protocol) {
				switch protocol.code {
				case "AUTH_INVALID", "AUTH_FRESH_REQUIRED", "CONFLICT", "GITHUB_NOT_CONFIGURED", "GITHUB_UNAVAILABLE", "GITHUB_RATE_LIMITED":
					code = protocol.code
				}
			}
			http.Redirect(w, r, "/?authError="+code, http.StatusSeeOther)
			return
		}
		g.passkeys.cookie(w, token, 24*60*60)
		http.Redirect(w, r, "/", http.StatusFound)
	}
}
func (g *Github) callback(r *http.Request) (string, error) {
	if !g.Configured() {
		return "", &protocolError{"GITHUB_NOT_CONFIGURED", "GitHub sign-in is not configured.", 409}
	}
	state, code := r.URL.Query().Get("state"), r.URL.Query().Get("code")
	if len(state) < 20 || len(state) > 200 || len(code) < 1 || len(code) > 1000 {
		return "", githubInvalid()
	}
	cookie, err := r.Cookie("scratchpad_github_state")
	if err != nil {
		return "", githubInvalid()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	candidate, err := g.loadState(ctx, g.auth.Store.DB, tokenID(state), cookie.Value)
	if err != nil {
		return "", err
	}
	verifier := stringValue(candidate, "verifier")
	err = g.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		entry, err := g.loadState(ctx, conn, tokenID(state), cookie.Value)
		if err != nil {
			return err
		}
		// Keep the original authorization only in memory after atomic consumption.
		candidate = entry
		verifier = stringValue(entry, "verifier")
		entry["consumedAt"] = timestamp(time.Now())
		delete(entry, "verifier")
		return update(ctx, conn, "github_oauth", entry)
	})
	if err != nil {
		return "", err
	}
	data, _, err := g.request(ctx, "https://github.com/login/oauth/access_token", url.Values{"client_id": {g.clientID}, "client_secret": {g.secret}, "code": {code}, "redirect_uri": {g.auth.Origin + "/api/v1/auth/github/callback"}, "code_verifier": {verifier}}, "")
	if err != nil {
		return "", err
	}
	var oauth struct {
		AccessToken string `json:"access_token"`
		Type        string `json:"token_type"`
	}
	if json.Unmarshal(data, &oauth) != nil || len(oauth.AccessToken) < 1 || len(oauth.AccessToken) > 4096 || oauth.Type != "bearer" {
		return "", githubInvalid()
	}
	account, err := g.account(ctx, "https://api.github.com/user", oauth.AccessToken)
	if err != nil {
		return "", err
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	err = g.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		raw, err := optional(ctx, conn, "github_binding", "owner")
		if err != nil {
			return err
		}
		var bound map[string]any
		if raw != nil {
			if err = json.Unmarshal(raw, &bound); err != nil {
				return err
			}
		}
		if bound["generation"] != candidate["bindingGeneration"] {
			return &protocolError{"CONFLICT", "GitHub binding changed during sign-in. Start again.", 409}
		}
		if err = g.callbackAuthorization(ctx, conn, candidate); err != nil {
			return err
		}
		if id := stringValue(candidate, "setupTokenId"); id != "" {
			if _, err = conn.ExecContext(ctx, `DELETE FROM entities WHERE kind=$1 AND id=$2`, "setup_token", id); err != nil {
				return err
			}
			owner, err := optional(ctx, conn, "owner", "owner")
			if err != nil {
				return err
			}
			if owner == nil {
				if err = insert(ctx, conn, "owner", "owner", map[string]any{"displayName": "Owner", "userId": "scratchpad-owner"}); err != nil {
					return err
				}
			}
		}
		if candidate["intent"] == "recover" {
			if err = revokeGithubSessions(ctx, conn, nil); err != nil {
				return err
			}
		}
		if candidate["intent"] == "login" {
			if bound == nil || bound["accountId"] != account.ID.String() {
				return githubInvalid()
			}
		} else {
			if err = g.invalidate(ctx, conn); err != nil {
				return err
			}
			generation, err := entityID("binding")
			if err != nil {
				return err
			}
			next := map[string]any{"accountId": account.ID.String(), "username": account.Login, "generation": generation, "keys": []any{}, "lastSuccessfulSyncAt": nil, "lastSyncError": nil}
			if bound == nil {
				err = insert(ctx, conn, "github_binding", "owner", next)
			} else {
				for k, v := range next {
					bound[k] = v
				}
				err = update(ctx, conn, "github_binding", bound)
				next = bound
			}
			if err != nil {
				return err
			}
			bound = next
			if err = passkeyAudit(ctx, conn, "github."+stringValue(candidate, "intent"), "owner", "owner", map[string]any{"kind": "user"}); err != nil {
				return err
			}
		}
		if bound["username"] != account.Login {
			bound["username"] = account.Login
			if err = update(ctx, conn, "github_binding", bound); err != nil {
				return err
			}
		}
		credentials, err := entityList(ctx, conn, "credential")
		if err != nil {
			return err
		}
		var cred map[string]any
		for _, c := range credentials {
			if c["kind"] == "github" && c["source"] == "github" && c["generation"] == bound["generation"] && stringValue(c, "revokedAt") == "" {
				cred = c
				break
			}
		}
		if cred == nil {
			id, err := entityID("credential")
			if err != nil {
				return err
			}
			cred = map[string]any{"kind": "github", "source": "github", "generation": bound["generation"], "label": "GitHub browser sign-in", "fingerprint": "github:" + account.ID.String()}
			if err = insert(ctx, conn, "credential", id, cred); err != nil {
				return err
			}
		}
		cred["lastUsedAt"] = timestamp(time.Now())
		if err = update(ctx, conn, "credential", cred); err != nil {
			return err
		}
		return insert(ctx, conn, "session", tokenID(token), map[string]any{"credentialId": cred["id"], "browser": true, "authenticatedAt": timestamp(time.Now()), "expiresAt": timestamp(time.Now().Add(24 * time.Hour))})
	})
	if err == nil {
		_ = g.Sync(ctx)
	}
	return token, err
}
func revokeGithubSessions(ctx context.Context, conn *sql.Conn, ids map[string]bool) error {
	sessions, err := entityList(ctx, conn, "session")
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if ids == nil || ids[stringValue(session, "credentialId")] {
			session["revokedAt"] = timestamp(time.Now())
			if err = update(ctx, conn, "session", session); err != nil {
				return err
			}
		}
	}
	return nil
}
func (g *Github) invalidate(ctx context.Context, conn *sql.Conn) error {
	credentials, err := entityList(ctx, conn, "credential")
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, cred := range credentials {
		if cred["source"] == "github" {
			ids[stringValue(cred, "id")] = true
			cred["revokedAt"] = timestamp(time.Now())
			if err = update(ctx, conn, "credential", cred); err != nil {
				return err
			}
		}
	}
	return revokeGithubSessions(ctx, conn, ids)
}
