package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type githubKey struct {
	PublicKey  string   `json:"publicKey"`
	Categories []string `json:"categories"`
}

var githubLinkPattern = regexp.MustCompile(`^\s*<([^<>]+)>\s*;\s*rel\s*=\s*(?:"([a-z]+(?: [a-z]+)*)"|([a-z]+))\s*$`)

func githubPaginationPage(raw, path string) (int, error) {
	target, err := url.Parse(raw)
	if err != nil || target.Scheme != "https" || target.Host != "api.github.com" || target.Path != path || target.User != nil || target.Fragment != "" {
		return 0, githubUnavailable()
	}
	query, err := url.ParseQuery(target.RawQuery)
	if err != nil || len(query["per_page"]) != 1 || query.Get("per_page") != "100" || len(query["page"]) != 1 {
		return 0, githubUnavailable()
	}
	page, err := strconv.Atoi(query.Get("page"))
	if err != nil || page < 1 || page > 100 || query.Get("page") != strconv.Itoa(page) {
		return 0, githubUnavailable()
	}
	return page, nil
}

func githubFingerprint(key string) string {
	parts := strings.Fields(key)
	if len(parts) < 2 {
		return ""
	}
	blob, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:])
}
func githubValidateKey(ctx context.Context, key string) error {
	dir, err := os.MkdirTemp("", "scratchpad-github-key-")
	if err != nil {
		return githubUnavailable()
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "public-key")
	if os.WriteFile(path, []byte(key+"\n"), 0600) != nil {
		return githubUnavailable()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if exec.CommandContext(ctx, "ssh-keygen", "-l", "-f", path).Run() != nil {
		return githubUnavailable()
	}
	return nil
}
func (g *Github) fetchKeys(ctx context.Context, bound map[string]any) ([]githubKey, string, error) {
	account, err := g.account(ctx, "https://api.github.com/user/"+url.PathEscape(stringValue(bound, "accountId")), "")
	if err != nil {
		return nil, "", err
	}
	if account.ID.String() != bound["accountId"] {
		return nil, "", githubUnavailable()
	}
	keys := []githubKey{}
	indexes := map[string]int{}
	for _, category := range []string{"keys", "ssh_signing_keys"} {
		path := "/users/" + account.Login + "/" + category
		next := "https://api.github.com" + path + "?per_page=100&page=1"
		lastPage := 1
		for page := 1; next != ""; page++ {
			if page > 100 {
				return nil, "", githubUnavailable()
			}
			data, link, err := g.request(ctx, next, nil, "")
			if err != nil {
				return nil, "", err
			}
			var entries []struct {
				Key string `json:"key"`
			}
			data = bytes.TrimSpace(data)
			if len(data) == 0 || data[0] != '[' || json.Unmarshal(data, &entries) != nil || len(entries) > 100 {
				return nil, "", githubUnavailable()
			}
			for _, entry := range entries {
				key, err := normalizedKey(entry.Key)
				if err != nil {
					return nil, "", githubUnavailable()
				}
				index, ok := indexes[key]
				if !ok {
					if err = githubValidateKey(ctx, key); err != nil {
						return nil, "", err
					}
					index = len(keys)
					indexes[key] = index
					keys = append(keys, githubKey{key, []string{}})
				}
				label := "authentication"
				if category == "ssh_signing_keys" {
					label = "signing"
				}
				found := false
				for _, value := range keys[index].Categories {
					if value == label {
						found = true
					}
				}
				if !found {
					keys[index].Categories = append(keys[index].Categories, label)
				}
			}
			next = ""
			if link != "" {
				for _, entry := range strings.Split(link, ",") {
					match := githubLinkPattern.FindStringSubmatch(entry)
					if match == nil {
						return nil, "", githubUnavailable()
					}
					rel := match[2]
					if rel == "" {
						rel = match[3]
					}
					for _, relation := range strings.Split(rel, " ") {
						switch relation {
						case "prev", "first":
						case "last":
							last, err := githubPaginationPage(match[1], path)
							if err != nil || last < page {
								return nil, "", githubUnavailable()
							}
							if last > lastPage {
								lastPage = last
							}
						case "next":
							if next != "" {
								return nil, "", githubUnavailable()
							}
							next = match[1]
						default:
							return nil, "", githubUnavailable()
						}
					}
				}
			}
			if next == "" && lastPage > page {
				return nil, "", githubUnavailable()
			}
			if next != "" {
				nextPage, err := githubPaginationPage(next, path)
				if err != nil || nextPage != page+1 {
					return nil, "", githubUnavailable()
				}
			}
		}
	}
	confirmation, err := g.account(ctx, "https://api.github.com/users/"+account.Login, "")
	if err != nil {
		return nil, "", err
	}
	if confirmation.ID.String() != bound["accountId"] {
		return nil, "", githubUnavailable()
	}
	return keys, account.Login, nil
}
func (g *Github) Sync(ctx context.Context) error {
	// A complete fetch has one total deadline in addition to per-request limits.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := entityID("sync")
	if err != nil {
		return err
	}
	var bound map[string]any
	err = g.auth.Store.Write(ctx, func(conn *sql.Conn) error {
		raw, err := optional(ctx, conn, "github_binding", "owner")
		if err != nil || raw == nil {
			return err
		}
		if err = json.Unmarshal(raw, &bound); err != nil {
			return err
		}
		bound["syncRequest"], bound["lastAttemptAt"] = request, timestamp(time.Now())
		return update(ctx, conn, "github_binding", bound)
	})
	if err != nil || bound == nil {
		return err
	}
	keys, username, fetchErr := g.fetchKeys(ctx, bound)
	// Persist a safe failure even if the fetch deadline expired; never extend cache freshness.
	writeCtx, writeCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer writeCancel()
	err = g.auth.Store.Write(writeCtx, func(conn *sql.Conn) error {
		raw, err := optional(writeCtx, conn, "github_binding", "owner")
		if err != nil || raw == nil {
			return err
		}
		var current map[string]any
		if err = json.Unmarshal(raw, &current); err != nil {
			return err
		}
		if current["generation"] != bound["generation"] || current["syncRequest"] != request {
			return nil
		}
		current["lastAttemptAt"] = timestamp(time.Now())
		if fetchErr != nil {
			code := "GITHUB_UNAVAILABLE"
			var protocol *protocolError
			if errors.As(fetchErr, &protocol) {
				code = protocol.code
			}
			current["lastSyncError"] = code
			return update(writeCtx, conn, "github_binding", current)
		}
		published := map[string]bool{}
		for _, key := range keys {
			published[key.PublicKey] = true
		}
		credentials, err := entityList(writeCtx, conn, "credential")
		if err != nil {
			return err
		}
		removed := map[string]bool{}
		for _, cred := range credentials {
			if cred["source"] == "github" && cred["kind"] == "ssh" && !published[stringValue(cred, "publicKey")] {
				removed[stringValue(cred, "id")] = true
			}
		}
		if err = revokeGithubSessions(writeCtx, conn, removed, g.auth.Store.Engine); err != nil {
			return err
		}
		current["username"], current["keys"], current["lastSuccessfulSyncAt"], current["lastSyncError"] = username, keys, timestamp(time.Now()), nil
		if err = update(writeCtx, conn, "github_binding", current); err != nil {
			return err
		}
		return passkeyAudit(writeCtx, conn, "github.synchronized", "owner", "owner", map[string]any{"kind": "system"})
	})
	if err != nil {
		return err
	}
	return fetchErr
}
func (g *Github) status(ctx context.Context, r *http.Request) (map[string]any, error) {
	tx, err := g.auth.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	session, cred, err := githubBrowser(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	raw, err := optional(ctx, tx, "github_binding", "owner")
	if err != nil {
		return nil, err
	}
	result := map[string]any{"configured": g.Configured(), "binding": nil, "lastSuccessfulSyncAt": nil, "cacheExpiresAt": nil, "cacheValid": false, "lastSyncError": nil, "keys": []any{}, "freshAuthenticationRequired": freshGithub(session, cred, false) != nil}
	if raw == nil {
		return result, nil
	}
	var bound map[string]any
	if err = json.Unmarshal(raw, &bound); err != nil {
		return nil, err
	}
	result["binding"] = map[string]any{"accountId": bound["accountId"], "username": bound["username"]}
	result["lastSuccessfulSyncAt"], result["lastSyncError"] = bound["lastSuccessfulSyncAt"], bound["lastSyncError"]
	synced, err := time.Parse(time.RFC3339Nano, stringValue(bound, "lastSuccessfulSyncAt"))
	if err == nil {
		result["cacheExpiresAt"] = timestamp(synced.Add(24 * time.Hour))
		result["cacheValid"] = time.Since(synced) >= 0 && time.Since(synced) < 24*time.Hour
	}
	var keys []githubKey
	encoded, _ := json.Marshal(bound["keys"])
	if err = json.Unmarshal(encoded, &keys); err != nil {
		return nil, err
	}
	public := []any{}
	for _, key := range keys {
		fingerprint := githubFingerprint(key.PublicKey)
		blocked, err := optional(ctx, tx, "github_block", fingerprint)
		if err != nil {
			return nil, err
		}
		public = append(public, map[string]any{"publicKey": key.PublicKey, "categories": key.Categories, "fingerprint": fingerprint, "blocked": blocked != nil})
	}
	result["keys"] = public
	return result, nil
}
func (g *Github) Status() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		result, err := g.status(ctx, r)
		if err != nil {
			g.auth.fail(w, err)
			return
		}
		JSON(w, 200, result)
	}
}

func (g *Github) ManualSync() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != g.auth.Origin {
			Error(w, 403, "AUTH_INVALID", "Invalid request origin.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		if _, _, err := githubBrowser(ctx, g.auth.Store.DB, r); err != nil {
			g.auth.fail(w, err)
			return
		}
		client, _, _ := net.SplitHostPort(r.RemoteAddr)
		release, err := g.auth.admission(client, true)
		if err != nil {
			g.auth.fail(w, err)
			return
		}
		defer release()
		if err = g.Sync(ctx); err != nil {
			g.auth.fail(w, err)
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

// Run owns the worker lifetime. Persisted attempts govern retries across restart;
// failures are recorded safely by Sync and never refresh complete-cache expiry.
func (g *Github) Run(ctx context.Context) {
	tick := func() {
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		raw, err := optional(readCtx, g.auth.Store.DB, "github_binding", "owner")
		if err != nil || raw == nil {
			return
		}
		var bound map[string]any
		if json.Unmarshal(raw, &bound) != nil {
			return
		}
		at, err := time.Parse(time.RFC3339Nano, stringValue(bound, "lastAttemptAt"))
		if err != nil || time.Since(at) >= 5*time.Minute {
			_ = g.Sync(ctx)
		}
	}
	tick()
	timer := time.NewTicker(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			tick()
		}
	}
}
