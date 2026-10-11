package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/alexcatdad/scratchpad/backend/internal/persistence"
)

const sshNamespace = "scratchpad-auth-v2"

var keyPattern = regexp.MustCompile(`^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp\d+|sk-ssh-ed25519@openssh.com|sk-ecdsa-sha2-nistp256@openssh.com) [A-Za-z0-9+/=]+(?: [^\r\n]*)?$`)

type protocolError struct {
	code, message string
	status        int
}

func (e *protocolError) Error() string        { return e.code }
func invalidProof(code, message string) error { return &protocolError{code, message, 401} }
func normalizedKey(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if len(value) < 20 || len(value) > 16384 || !keyPattern.MatchString(value) {
		return "", &protocolError{"VALIDATION_ERROR", "Use a single OpenSSH public key.", 400}
	}
	return strings.Join(strings.Fields(value)[:2], " "), nil
}
func randomToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}
func entityID(prefix string) (string, error) {
	value, err := randomToken()
	return prefix + "_" + value, err
}
func timestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }
func insert(ctx context.Context, conn *sql.Conn, kind, id string, value map[string]any) error {
	value["id"] = id
	value["version"] = 1
	at := timestamp(time.Now())
	value["createdAt"] = at
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO entities(kind,id,data,version,created_at) VALUES($1,$2,$3,1,$4)`, kind, id, string(data), at)
	return err
}

type SSH struct {
	Store    *persistence.Store
	Origin   string
	mu       sync.Mutex
	attempts map[string]struct {
		minute int64
		count  int
	}
	active        map[string]int
	verifications int
}

func NewSSH(store *persistence.Store, origin string) *SSH {
	return &SSH{Store: store, Origin: origin, attempts: make(map[string]struct {
		minute int64
		count  int
	}), active: make(map[string]int)}
}
func (s *SSH) admission(client string, verification bool) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	minute := time.Now().Unix() / 60
	entry := s.attempts[client]
	if entry.minute != minute {
		entry.minute, entry.count = minute, 0
	}
	if entry.count >= 120 {
		return nil, &protocolError{"RATE_LIMITED", "Too many authentication attempts. Try again in a minute.", 429}
	}
	if _, exists := s.attempts[client]; !exists && len(s.attempts) >= 4096 {
		for key, value := range s.attempts {
			if value.minute != minute {
				delete(s.attempts, key)
			}
		}
		if len(s.attempts) >= 4096 {
			return nil, &protocolError{"RATE_LIMITED", "Authentication admission is busy. Try again shortly.", 429}
		}
	}
	entry.count++
	s.attempts[client] = entry
	if !verification {
		return func() {}, nil
	}
	if s.active[client] >= 2 || s.verifications >= 8 {
		return nil, &protocolError{"RATE_LIMITED", "Authentication verification is busy. Try again shortly.", 429}
	}
	s.active[client]++
	s.verifications++
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.verifications--
		s.active[client]--
		if s.active[client] == 0 {
			delete(s.active, client)
		}
	}, nil
}
func (s *SSH) Handler(verify bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		client, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			client = r.RemoteAddr
		}
		release, err := s.admission(client, verify)
		if err != nil {
			s.fail(w, err)
			return
		}
		defer release()
		var request struct {
			PublicKey   string `json:"publicKey"`
			ChallengeID string `json:"challengeId"`
			Signature   string `json:"signature"`
			Label       string `json:"label"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		if err := decoder.Decode(&request); err != nil {
			Error(w, 400, "VALIDATION_ERROR", "Invalid authentication request.")
			return
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			Error(w, 400, "VALIDATION_ERROR", "Invalid authentication request.")
			return
		}
		key, err := normalizedKey(request.PublicKey)
		if err != nil {
			s.fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if verify {
			if request.ChallengeID == "" || len(request.ChallengeID) > 500 || len(request.Signature) > 32768 {
				Error(w, 400, "VALIDATION_ERROR", "Invalid authentication request.")
				return
			}
			result, err := s.verify(ctx, request.ChallengeID, key, request.Signature)
			if err != nil {
				s.fail(w, err)
				return
			}
			JSON(w, 200, result)
			return
		}
		result, err := s.challenge(ctx, key)
		if err != nil {
			s.fail(w, err)
			return
		}
		JSON(w, 200, result)
	}
}
func (s *SSH) fail(w http.ResponseWriter, err error) {
	var e *protocolError
	if errors.As(err, &e) {
		Error(w, e.status, e.code, e.message)
	} else {
		Error(w, 503, "DATABASE_NOT_READY", "Authentication persistence is unavailable.")
	}
}

// Eligibility is read again inside the transaction that consumes the proof.
// Local key blocking applies to independent keys as well as GitHub-managed ones.
func (s *SSH) eligible(ctx context.Context, q entityReader, key string) (map[string]any, json.RawMessage, error) {
	blob, err := base64.StdEncoding.DecodeString(strings.Fields(key)[1])
	if err != nil {
		return nil, nil, invalidProof("AUTH_INVALID", "Invalid public key.")
	}
	fingerprint := sha256.Sum256(blob)
	block, err := optional(ctx, q, "github_block", "SHA256:"+base64.RawStdEncoding.EncodeToString(fingerprint[:]))
	if err != nil {
		return nil, nil, err
	}
	if block != nil {
		return nil, nil, invalidProof("AUTH_INVALID", "This key is locally blocked.")
	}
	field := `json_extract(data,'$.publicKey')`
	kind := `json_extract(data,'$.kind')`
	revoked := `json_extract(data,'$.revokedAt')`
	if s.Store.Engine == "postgres" {
		field, kind, revoked = "data->>'publicKey'", "data->>'kind'", "data->>'revokedAt'"
	}
	var raw []byte
	err = q.QueryRowContext(ctx, `SELECT data FROM entities WHERE kind='credential' AND `+field+`=$1 AND `+kind+`='ssh' AND COALESCE(`+revoked+`,'')='' ORDER BY created_at,id LIMIT 1`, key).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, err
	}
	var c map[string]any
	if raw != nil {
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, nil, err
		}
	}
	bindingRaw, err := optional(ctx, q, "github_binding", "owner")
	if err != nil {
		return nil, nil, err
	}
	var b binding
	if bindingRaw != nil {
		if err := json.Unmarshal(bindingRaw, &b); err != nil {
			return nil, nil, err
		}
	}
	if c != nil && c["source"] != "github" {
		return c, b.Generation, nil
	}
	if c != nil {
		generation, err := json.Marshal(c["generation"])
		if err != nil {
			return nil, nil, err
		}
		if string(generation) != string(b.Generation) {
			return nil, nil, invalidProof("AUTH_INVALID", "GitHub access changed.")
		}
	}
	synced, err := time.Parse(time.RFC3339Nano, b.LastSync)
	age := time.Since(synced)
	if err != nil || age < 0 || age >= 24*time.Hour {
		return nil, nil, invalidProof("AUTH_INVALID", "Public key is not enrolled or cached authorization expired.")
	}
	for _, published := range b.Keys {
		if published.PublicKey == key {
			return c, b.Generation, nil
		}
	}
	return nil, nil, invalidProof("AUTH_INVALID", "Public key is not enrolled.")
}

func (s *SSH) challenge(ctx context.Context, key string) (map[string]any, error) {
	id, err := entityID("challenge")
	if err != nil {
		return nil, err
	}
	nonce, err := randomToken()
	if err != nil {
		return nil, err
	}
	expiry := timestamp(time.Now().Add(2 * time.Minute))
	result := map[string]any{"challengeId": id, "version": 2, "recipient": s.Origin, "purpose": "ssh_login", "nonce": nonce, "namespace": sshNamespace, "expiresAt": expiry}
	err = s.Store.Write(ctx, func(conn *sql.Conn) error {
		_, generation, err := s.eligible(ctx, conn, key)
		if err != nil {
			return err
		}
		field := `json_extract(data,'$.expiresAt')`
		if s.Store.Engine == "postgres" {
			field = "data->>'expiresAt'"
		}
		if _, err := conn.ExecContext(ctx, `DELETE FROM entities WHERE kind='challenge' AND `+field+` < $1`, timestamp(time.Now())); err != nil {
			return err
		}
		value := map[string]any{"kind": "ssh", "proofVersion": 2, "recipient": s.Origin, "publicKey": key, "nonce": nonce, "expiresAt": expiry, "label": "MCP key"}
		if generation != nil {
			value["bindingGeneration"] = generation
		}
		return insert(ctx, conn, "challenge", id, value)
	})
	return result, err
}
func (s *SSH) loadChallenge(ctx context.Context, q entityReader, id, key string) (map[string]any, error) {
	raw, err := get(ctx, q, "challenge", id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, invalidProof("AUTH_INVALID", "Unknown challenge.")
	}
	if err != nil {
		return nil, err
	}
	var c map[string]any
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, invalidProof("AUTH_INVALID", "Invalid challenge.")
	}
	if c["kind"] != "ssh" || c["proofVersion"] != float64(2) || c["recipient"] != s.Origin || c["publicKey"] != key {
		return nil, invalidProof("AUTH_INVALID", "Challenge context does not match.")
	}
	if consumed, _ := c["consumedAt"].(string); consumed != "" {
		return nil, invalidProof("AUTH_CHALLENGE_USED", "Challenge has already been consumed.")
	}
	expiry, _ := c["expiresAt"].(string)
	at, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil || !at.After(time.Now()) {
		return nil, invalidProof("AUTH_CHALLENGE_EXPIRED", "Challenge expired.")
	}
	return c, nil
}
func (s *SSH) verify(ctx context.Context, id, key, signature string) (map[string]any, error) {
	c, err := s.loadChallenge(ctx, s.Store.DB, id, key)
	if err != nil {
		return nil, err
	}
	proof, err := json.Marshal([]any{"scratchpad-ssh-proof", 2, s.Origin, "ssh_login", id, c["nonce"], key, c["expiresAt"]})
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "scratchpad-go-verify-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "allowed"), []byte("owner "+key+"\n"), 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "signature"), []byte(signature), 0600); err != nil {
		return nil, err
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	verifier := exec.CommandContext(verifyCtx, "ssh-keygen", "-Y", "verify", "-f", filepath.Join(dir, "allowed"), "-I", "owner", "-n", sshNamespace, "-s", filepath.Join(dir, "signature"))
	verifier.Stdin = bytes.NewReader(proof)
	verifier.Stdout = io.Discard
	verifier.Stderr = io.Discard
	if err := verifier.Run(); err != nil {
		return nil, invalidProof("AUTH_INVALID", "Signature verification failed.")
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	expires := timestamp(time.Now().Add(24 * time.Hour))
	err = s.Store.Write(ctx, func(conn *sql.Conn) error {
		challenge, err := s.loadChallenge(ctx, conn, id, key)
		if err != nil {
			return err
		}
		credential, generation, err := s.eligible(ctx, conn, key)
		if err != nil {
			return err
		}
		if credential == nil || credential["source"] == "github" {
			expected, err := json.Marshal(challenge["bindingGeneration"])
			if err != nil {
				return err
			}
			if string(expected) != string(generation) {
				return invalidProof("AUTH_INVALID", "GitHub authorization changed during key proof.")
			}
		}
		if credential == nil {
			credentialID, err := entityID("credential")
			if err != nil {
				return err
			}
			blob, _ := base64.StdEncoding.DecodeString(strings.Fields(key)[1])
			fingerprint := sha256.Sum256(blob)
			credential = map[string]any{"kind": "ssh", "publicKey": key, "source": "github", "generation": generation, "label": "GitHub-managed key", "fingerprint": "SHA256:" + base64.RawStdEncoding.EncodeToString(fingerprint[:])}
			if err := insert(ctx, conn, "credential", credentialID, credential); err != nil {
				return err
			}
		}
		challenge["consumedAt"] = timestamp(time.Now())
		if err := update(ctx, conn, "challenge", challenge); err != nil {
			return err
		}
		credential["lastUsedAt"] = timestamp(time.Now())
		if err := update(ctx, conn, "credential", credential); err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(token))
		return insert(ctx, conn, "session", hex.EncodeToString(digest[:]), map[string]any{"credentialId": credential["id"], "browser": false, "expiresAt": expires, "authenticatedAt": timestamp(time.Now())})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"accessToken": token, "expiresAt": expires}, nil
}
func update(ctx context.Context, conn *sql.Conn, kind string, value map[string]any) error {
	version, ok := value["version"].(float64)
	if !ok {
		if n, ok := value["version"].(int); ok {
			version = float64(n)
		} else {
			return errors.New("invalid entity version")
		}
	}
	value["version"] = int(version) + 1
	value["updatedAt"] = timestamp(time.Now())
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	result, err := conn.ExecContext(ctx, `UPDATE entities SET data=$1,version=$2 WHERE kind=$3 AND id=$4 AND version=$5`, string(data), int(version)+1, kind, value["id"], int(version))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("entity conflict")
	}
	return nil
}
