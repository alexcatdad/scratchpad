package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alexcatdad/scratchpad/backend/internal/persistence"
)

var bearerPattern = regexp.MustCompile(`^Bearer ([A-Za-z0-9_-]+)$`)
var errUnauthorized = errors.New("invalid session")
var errAuthRequired = errors.New("missing session")

type session struct {
	CredentialID string `json:"credentialId"`
	ExpiresAt    string `json:"expiresAt"`
	RevokedAt    string `json:"revokedAt"`
	Browser      bool   `json:"browser"`
}
type credential struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Source     string          `json:"source"`
	PublicKey  string          `json:"publicKey"`
	RevokedAt  string          `json:"revokedAt"`
	Generation json.RawMessage `json:"generation"`
}
type binding struct {
	Generation json.RawMessage `json:"generation"`
	LastSync   string          `json:"lastSuccessfulSyncAt"`
	Keys       []struct {
		PublicKey string `json:"publicKey"`
	} `json:"keys"`
}

type detail struct {
	Record        json.RawMessage   `json:"record"`
	Metadata      json.RawMessage   `json:"metadata,omitempty"`
	Revisions     []json.RawMessage `json:"revisions"`
	Relationships []json.RawMessage `json:"relationships"`
	Evidence      []json.RawMessage `json:"evidence"`
	Mirror        json.RawMessage   `json:"mirror,omitempty"`
	Audit         []json.RawMessage `json:"audit"`
}

func get(ctx context.Context, tx *sql.Tx, kind, id string) (json.RawMessage, error) {
	var data []byte
	err := tx.QueryRowContext(ctx, `SELECT data FROM entities WHERE kind=$1 AND id=$2`, kind, id).Scan(&data)
	if err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, errors.New("invalid entity")
	}
	return data, nil
}
func optional(ctx context.Context, tx *sql.Tx, kind, id string) (json.RawMessage, error) {
	data, err := get(ctx, tx, kind, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return data, err
}
func authorize(ctx context.Context, tx *sql.Tx, r *http.Request) error {
	token := ""
	bearer := false
	if matches := bearerPattern.FindStringSubmatch(r.Header.Get("Authorization")); len(matches) == 2 {
		token, bearer = matches[1], true
	}
	if token == "" {
		if cookie, err := r.Cookie("scratchpad_session"); err == nil {
			token = cookie.Value
		}
	}
	if token == "" {
		return errAuthRequired
	}
	digest := sha256.Sum256([]byte(token))
	data, err := get(ctx, tx, "session", hex.EncodeToString(digest[:]))
	if errors.Is(err, sql.ErrNoRows) {
		return errUnauthorized
	}
	if err != nil {
		return err
	}
	var s session
	if err := json.Unmarshal(data, &s); err != nil {
		return errUnauthorized
	}
	expiry, err := time.Parse(time.RFC3339Nano, s.ExpiresAt)
	if err != nil || !expiry.After(time.Now()) || s.RevokedAt != "" || s.Browser == bearer {
		return errUnauthorized
	}
	data, err = get(ctx, tx, "credential", s.CredentialID)
	if errors.Is(err, sql.ErrNoRows) {
		return errUnauthorized
	}
	if err != nil {
		return err
	}
	var c credential
	if err := json.Unmarshal(data, &c); err != nil || c.ID != s.CredentialID || c.RevokedAt != "" {
		return errUnauthorized
	}
	if c.PublicKey != "" {
		fields := strings.Fields(c.PublicKey)
		if len(fields) < 2 {
			return errUnauthorized
		}
		blob, err := base64.StdEncoding.DecodeString(fields[1])
		if err != nil {
			return errUnauthorized
		}
		digest := sha256.Sum256(blob)
		block, err := optional(ctx, tx, "github_block", "SHA256:"+base64.RawStdEncoding.EncodeToString(digest[:]))
		if err != nil {
			return err
		}
		if block != nil {
			return errUnauthorized
		}
	}
	if c.Source != "github" {
		return nil
	}
	data, err = get(ctx, tx, "github_binding", "owner")
	if errors.Is(err, sql.ErrNoRows) {
		return errUnauthorized
	}
	if err != nil {
		return err
	}
	var b binding
	if err := json.Unmarshal(data, &b); err != nil || len(b.Generation) == 0 || string(b.Generation) != string(c.Generation) {
		return errUnauthorized
	}
	if c.Kind != "ssh" {
		return nil
	}
	synced, err := time.Parse(time.RFC3339Nano, b.LastSync)
	age := time.Since(synced)
	if err != nil || age < 0 || age >= 24*time.Hour {
		return errUnauthorized
	}
	for _, key := range b.Keys {
		if key.PublicKey == c.PublicKey {
			return nil
		}
	}
	return errUnauthorized
}

func related(ctx context.Context, tx *sql.Tx, engine, kind, id string) ([]json.RawMessage, error) {
	field := `json_extract(data,'$.recordId')`
	from := `json_extract(data,'$.fromRecordId')`
	to := `json_extract(data,'$.toRecordId')`
	if engine == "postgres" {
		field, from, to = "data->>'recordId'", "data->>'fromRecordId'", "data->>'toRecordId'"
	}
	predicate := field + "=$2"
	if kind == "relationship" {
		predicate = "(" + from + "=$2 OR " + to + "=$2)"
	}
	rows, err := tx.QueryContext(ctx, `SELECT data FROM entities WHERE kind=$1 AND `+predicate+` ORDER BY created_at,id`, kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if !json.Valid(raw) {
			return nil, errors.New("invalid entity")
		}
		values = append(values, json.RawMessage(raw))
	}
	return values, rows.Err()
}
func recordDetail(ctx context.Context, tx *sql.Tx, engine, id string) (detail, error) {
	d := detail{}
	var err error
	if d.Record, err = get(ctx, tx, "record", id); err != nil {
		return d, err
	}
	if d.Metadata, err = optional(ctx, tx, "metadata", id); err != nil {
		return d, err
	}
	if d.Mirror, err = optional(ctx, tx, "mirror", id); err != nil {
		return d, err
	}
	if d.Revisions, err = related(ctx, tx, engine, "revision", id); err != nil {
		return d, err
	}
	if d.Relationships, err = related(ctx, tx, engine, "relationship", id); err != nil {
		return d, err
	}
	if d.Evidence, err = related(ctx, tx, engine, "evidence", id); err != nil {
		return d, err
	}
	ids := []string{id}
	for _, items := range [][]json.RawMessage{d.Relationships, d.Evidence} {
		for _, item := range items {
			var v struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(item, &v); err != nil {
				return d, err
			}
			ids = append(ids, v.ID)
		}
	}
	field := `json_extract(data,'$.entityId')`
	if engine == "postgres" {
		field = "data->>'entityId'"
	}
	args := []any{"audit"}
	placeholders := []string{}
	for _, id := range ids {
		args = append(args, id)
		placeholders = append(placeholders, "$"+strconv.Itoa(len(args)))
	}
	rows, err := tx.QueryContext(ctx, `SELECT data FROM entities WHERE kind=$1 AND `+field+` IN (`+strings.Join(placeholders, ",")+`) ORDER BY created_at,id`, args...)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	d.Audit = []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return d, err
		}
		if !json.Valid(raw) {
			return d, errors.New("invalid audit")
		}
		d.Audit = append(d.Audit, json.RawMessage(raw))
	}
	return d, rows.Err()
}

func Records(store *persistence.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		tx, err := store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			Error(w, 503, "DATABASE_NOT_READY", "Persistence is unavailable.")
			return
		}
		defer tx.Rollback()
		if err := authorize(ctx, tx, r); err != nil {
			if errors.Is(err, errAuthRequired) {
				Error(w, 401, "AUTH_REQUIRED", "Sign in to Scratchpad.")
			} else if errors.Is(err, errUnauthorized) {
				Error(w, 401, "AUTH_INVALID", "Session expired or revoked.")
			} else {
				Error(w, 503, "DATABASE_NOT_READY", "Persistence is unavailable.")
			}
			return
		}
		d, err := recordDetail(ctx, tx, store.Engine, r.PathValue("id"))
		if errors.Is(err, sql.ErrNoRows) {
			Error(w, 404, "NOT_FOUND", "Record not found.")
			return
		}
		if err != nil {
			Error(w, 503, "DATABASE_NOT_READY", "Persistence is unavailable.")
			return
		}
		if err := tx.Commit(); err != nil {
			Error(w, 503, "DATABASE_NOT_READY", "Persistence is unavailable.")
			return
		}
		JSON(w, 200, d)
	}
}
func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
