package localcapture

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	databaseName = "captures.sqlite"
	envelopeV1   = 1
)

// Store keeps only keyed lookup digests and scheduling metadata in SQLite.
// Every capture, configuration, receipt detail and project binding is inside
// the AES-GCM envelope.
type Store struct {
	db     *sql.DB
	aead   cipher.AEAD
	macKey []byte
	mu     sync.Mutex
}

type storedEnvelope struct {
	Version           int                   `json:"version"`
	Receipt           Receipt               `json:"receipt"`
	Request           *CaptureRequest       `json:"request,omitempty"`
	Configuration     *ServiceConfiguration `json:"configuration,omitempty"`
	CredentialID      string                `json:"credentialId,omitempty"`
	RequestDigest     string                `json:"requestDigest,omitempty"`
	DeliveryAttempted bool                  `json:"deliveryAttempted,omitempty"`
	ProjectResolved   bool                  `json:"projectResolved,omitempty"`
	TombstoneUntil    string                `json:"tombstoneUntil,omitempty"`
	DeliveryPaused    bool                  `json:"deliveryPaused,omitempty"`
	ServiceStopped    bool                  `json:"serviceStopped,omitempty"`
	Connection        ConnectionStatus      `json:"connection,omitempty"`
	LastDelivery      *DeliverySummary      `json:"lastDelivery,omitempty"`
}

func OpenStore(path string, keys KeyProvider) (*Store, error) {
	if path == "" || keys == nil {
		return nil, ErrStorageFailedSentinel
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrStorageFailedSentinel
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, ErrStorageFailedSentinel
	}
	if err := securePrivateDirectory(dir); err != nil {
		return nil, ErrStorageFailedSentinel
	}
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		key, keyErr := keys.Create(ctx)
		if keyErr != nil {
			return nil, classifyKeyError(keyErr)
		}
		if len(key) != 32 {
			clear(key)
			return nil, ErrKeyUnavailableSentinel
		}
		f, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if createErr != nil {
			clear(key)
			return nil, ErrStorageFailedSentinel
		}
		f.Close()
		store, openErr := openWithKey(path, key)
		clear(key)
		if openErr != nil {
			_ = os.Remove(path)
			return nil, openErr
		}
		return store, nil
	} else if err != nil {
		return nil, ErrStorageFailedSentinel
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() || fi.Mode().Perm()&0077 != 0 {
		return nil, ErrStorageFailedSentinel
	}
	if !ownedByCurrentUser(fi) {
		return nil, ErrStorageFailedSentinel
	}
	if fi.Size() == 0 {
		return nil, ErrKeyUnavailableSentinel
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key, err := keys.Load(ctx)
	if err != nil {
		return nil, classifyKeyError(err)
	}
	if len(key) != 32 {
		clear(key)
		return nil, ErrKeyUnavailableSentinel
	}
	store, err := openWithKey(path, key)
	clear(key)
	if err != nil {
		return nil, err
	}
	return store, nil
}

func classifyKeyError(err error) error {
	if errors.Is(err, ErrKeyUnlockRequiredSentinel) {
		return ErrKeyUnlockRequiredSentinel
	}
	return ErrKeyUnavailableSentinel
}

// InspectSchemaVersion performs a read-only PRAGMA for the native updater; it
// does not request or load the encryption key and never reads queue rows.
func InspectSchemaVersion(path string) (int, error) {
	if path == "" {
		return 0, ErrStorageFailedSentinel
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	} else if err != nil {
		return 0, ErrStorageFailedSentinel
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, ErrStorageFailedSentinel
	}
	dsn := (&url.URL{Scheme: "file", Path: abs}).String() + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, ErrStorageFailedSentinel
	}
	defer db.Close()
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version < 0 {
		return 0, ErrStorageFailedSentinel
	}
	return version, nil
}

func openWithKey(path string, key []byte) (*Store, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrKeyUnavailableSentinel
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrKeyUnavailableSentinel
	}
	derived := hmac.New(sha256.New, key)
	_, _ = derived.Write([]byte("scratchpad/localcapture/index/v1"))
	macKey := derived.Sum(nil)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, ErrStorageFailedSentinel
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	var schemaVersion int
	if err = db.QueryRow("PRAGMA user_version").Scan(&schemaVersion); err != nil || schemaVersion > QueueSchemaVersion {
		db.Close()
		return nil, ErrStorageFailedSentinel
	}
	for _, pragma := range []string{"PRAGMA journal_mode=DELETE", "PRAGMA synchronous=FULL", "PRAGMA secure_delete=ON", "PRAGMA foreign_keys=ON"} {
		if _, err = db.Exec(pragma); err != nil {
			db.Close()
			return nil, ErrStorageFailedSentinel
		}
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS capture_items (
    receipt_key TEXT PRIMARY KEY,
    request_key TEXT UNIQUE NOT NULL,
    created_ns INTEGER NOT NULL,
    state TEXT NOT NULL,
    next_attempt_ns INTEGER NOT NULL,
    attempts INTEGER NOT NULL,
    envelope BLOB NOT NULL
)`); err != nil {
		db.Close()
		return nil, ErrStorageFailedSentinel
	}
	if schemaVersion == 0 {
		if _, err = db.Exec("PRAGMA user_version=1"); err != nil {
			db.Close()
			return nil, ErrStorageFailedSentinel
		}
	}
	if err = os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, ErrStorageFailedSentinel
	}
	if err = verifyEncryptedDatabase(db, aead, macKey); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, aead: aead, macKey: macKey}, nil
}

func verifyEncryptedDatabase(db *sql.DB, aead cipher.AEAD, macKey []byte) error {
	rows, err := db.Query("SELECT receipt_key,request_key,state,next_attempt_ns,attempts,created_ns,envelope FROM capture_items")
	if err != nil {
		return ErrStorageFailedSentinel
	}
	defer rows.Close()
	for rows.Next() {
		var id, requestKey, state string
		var nextAttempt, attempts, created int64
		var blob []byte
		if rows.Scan(&id, &requestKey, &state, &nextAttempt, &attempts, &created, &blob) != nil || id == "" || requestKey == "" || nextAttempt < 0 || attempts < 0 || created <= 0 {
			return fmt.Errorf("%w: invalid queue row metadata", ErrStorageFailedSentinel)
		}
		plain, decryptErr := decrypt(aead, id, blob)
		if decryptErr != nil {
			return ErrKeyUnavailableSentinel
		}
		var env storedEnvelope
		decodeErr := json.Unmarshal(plain, &env)
		clear(plain)
		if decodeErr != nil || env.Version != envelopeV1 {
			return fmt.Errorf("%w: invalid encrypted envelope", ErrStorageFailedSentinel)
		}
		switch state {
		case "configuration":
			if env.Configuration == nil || keyedDigest(macKey, "configuration") != requestKey {
				return fmt.Errorf("%w: invalid configuration row", ErrStorageFailedSentinel)
			}
		case "settings":
			if keyedDigest(macKey, "settings") != requestKey {
				return fmt.Errorf("%w: invalid settings row", ErrStorageFailedSentinel)
			}
		default:
			if env.Receipt.ReceiptID == "" || string(env.Receipt.State) != state || keyedDigest(macKey, env.Receipt.ReceiptID) != id {
				return fmt.Errorf("%w: invalid receipt row", ErrStorageFailedSentinel)
			}
			if env.Request != nil {
				if env.Request.RequestID == "" || keyedDigest(macKey, env.Request.RequestID) != requestKey {
					return fmt.Errorf("%w: invalid request index", ErrStorageFailedSentinel)
				}
			} else if state != string(ReceiptDelivered) && state != string(ReceiptDiscarded) {
				return fmt.Errorf("%w: unexpected retained payload state", ErrStorageFailedSentinel)
			}
		}
	}
	if rows.Err() != nil {
		return ErrStorageFailedSentinel
	}
	return nil
}

func verifyPrivateDirectory(path string) error {
	fi, err := os.Lstat(path)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || fi.Mode().Perm()&0077 != 0 || !ownedByCurrentUser(fi) {
		return ErrStorageFailedSentinel
	}
	return nil
}

// securePrivateDirectory refuses a symlink or foreign/non-directory final
// component before chmod can follow it and mutate a target outside the store.
func securePrivateDirectory(path string) error {
	fi, err := os.Lstat(path)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(fi) {
		return ErrStorageFailedSentinel
	}
	if err = os.Chmod(path, 0700); err != nil {
		return ErrStorageFailedSentinel
	}
	return verifyPrivateDirectory(path)
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) digest(value string) string {
	return keyedDigest(s.macKey, value)
}

func keyedDigest(key []byte, value string) string {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (s *Store) seal(id string, env storedEnvelope) ([]byte, error) {
	plain, err := json.Marshal(env)
	if err != nil {
		return nil, ErrStorageFailedSentinel
	}
	defer clear(plain)
	return encrypt(s.aead, id, plain)
}

func (s *Store) open(id string, blob []byte) (storedEnvelope, error) {
	plain, err := decrypt(s.aead, id, blob)
	if err != nil {
		return storedEnvelope{}, ErrKeyUnavailableSentinel
	}
	defer clear(plain)
	var env storedEnvelope
	if json.Unmarshal(plain, &env) != nil || env.Version != envelopeV1 {
		return storedEnvelope{}, ErrStorageFailedSentinel
	}
	return env, nil
}

func encrypt(aead cipher.AEAD, id string, plain []byte) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, ErrStorageFailedSentinel
	}
	return aead.Seal(nonce, nonce, plain, []byte("scratchpad/localcapture/v1/"+id)), nil
}

func decrypt(aead cipher.AEAD, id string, blob []byte) ([]byte, error) {
	if len(blob) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("invalid envelope")
	}
	nonce := blob[:aead.NonceSize()]
	return aead.Open(nil, nonce, blob[aead.NonceSize():], []byte("scratchpad/localcapture/v1/"+id))
}

func safeID() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", ErrStorageFailedSentinel
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func (s *Store) insert(env storedEnvelope) error {
	id, err := safeID()
	if err != nil {
		return err
	}
	env.Version = envelopeV1
	blob, err := s.seal(id, env)
	if err != nil {
		return err
	}
	requestKey := ""
	if env.Request != nil {
		requestKey = s.digest(env.Request.RequestID)
	}
	if requestKey == "" {
		requestKey = s.digest("receipt/" + id)
	}
	created, _ := time.Parse(time.RFC3339Nano, env.Receipt.AcceptedAt)
	_, err = s.db.Exec(`INSERT INTO capture_items(receipt_key,request_key,created_ns,state,next_attempt_ns,attempts,envelope) VALUES(?,?,?,?,?,?,?)`, id, requestKey, created.UnixNano(), env.Receipt.State, 0, 0, blob)
	if err != nil {
		return ErrStorageFailedSentinel
	}
	return nil
}

func (s *Store) accept(req CaptureRequest, receipt Receipt) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	requestKey := s.digest(req.RequestID)
	requestDigest := s.requestDigest(req)
	var id string
	var blob []byte
	err := s.db.QueryRow("SELECT receipt_key,envelope FROM capture_items WHERE request_key=?", requestKey).Scan(&id, &blob)
	if err == nil {
		existing, openErr := s.open(id, blob)
		if openErr != nil {
			return Receipt{}, openErr
		}
		matches := existing.RequestDigest != "" && hmac.Equal([]byte(existing.RequestDigest), []byte(requestDigest))
		if existing.RequestDigest == "" && existing.Request != nil {
			matches = sameRequest(*existing.Request, req)
		}
		if !matches {
			return Receipt{}, ErrConflictSentinel
		}
		return existing.Receipt, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Receipt{}, ErrStorageFailedSentinel
	}
	env := storedEnvelope{Version: envelopeV1, Receipt: receipt, Request: &req, RequestDigest: requestDigest}
	receiptID, err := safeID()
	if err != nil {
		return Receipt{}, err
	}
	receipt.ReceiptID = receiptID
	env.Receipt = receipt
	id = s.digest(receiptID)
	blob, err = s.seal(id, env)
	if err != nil {
		return Receipt{}, err
	}
	created, _ := time.Parse(time.RFC3339Nano, receipt.AcceptedAt)
	_, err = s.db.Exec(`INSERT INTO capture_items(receipt_key,request_key,created_ns,state,next_attempt_ns,attempts,envelope) VALUES(?,?,?,?,?,?,?)`, id, requestKey, created.UnixNano(), receipt.State, 0, 0, blob)
	if err != nil {
		return Receipt{}, ErrStorageFailedSentinel
	}
	return receipt, nil
}

func (s *Store) requestDigest(req CaptureRequest) string {
	generated := req.ClientSavedAtGenerated
	if req.ClientSavedAtGenerated {
		// The service-generated timestamp is stable receipt metadata, but it was
		// not part of caller input and must not make an omitted-time retry differ.
		req.ClientSavedAt = ""
	}
	identity := struct {
		Request   CaptureRequest `json:"request"`
		Generated bool           `json:"generatedClientSavedAt"`
	}{Request: req, Generated: generated}
	data, err := json.Marshal(identity)
	if err != nil {
		return ""
	}
	defer clear(data)
	return s.digest(string(data))
}

func sameRequest(a, b CaptureRequest) bool {
	if b.ClientSavedAtGenerated {
		b.ClientSavedAt = a.ClientSavedAt
	}
	a.ClientSavedAtGenerated = false
	b.ClientSavedAtGenerated = false
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return hmac.Equal(aa, bb)
}

func (s *Store) updateEnvelope(id string, env storedEnvelope, next time.Time, attempts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.update(id, env, next, attempts)
}

func (s *Store) loadReceipt(receiptID string) (string, storedEnvelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.findByReceipt(receiptID)
}

func (s *Store) configuration() (ServiceConfiguration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getConfiguration()
}

func (s *Store) saveConfiguration(config ServiceConfiguration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setConfiguration(config)
}

func (s *Store) settings() (storedEnvelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var id string
	var blob []byte
	err := s.db.QueryRow("SELECT receipt_key,envelope FROM capture_items WHERE state='settings' LIMIT 1").Scan(&id, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return storedEnvelope{}, nil
	}
	if err != nil {
		return storedEnvelope{}, ErrStorageFailedSentinel
	}
	return s.open(id, blob)
}

func (s *Store) saveSettings(settings storedEnvelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings.Version = envelopeV1
	var id string
	var old []byte
	err := s.db.QueryRow("SELECT receipt_key,envelope FROM capture_items WHERE state='settings' LIMIT 1").Scan(&id, &old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ErrStorageFailedSentinel
	}
	newRow := errors.Is(err, sql.ErrNoRows)
	if newRow {
		id, err = safeID()
		if err != nil {
			return err
		}
	}
	blob, err := s.seal(id, settings)
	if err != nil {
		return err
	}
	if newRow {
		_, err = s.db.Exec("INSERT INTO capture_items(receipt_key,request_key,created_ns,state,next_attempt_ns,attempts,envelope) VALUES(?,?,?,?,?,?,?)", id, s.digest("settings"), time.Now().UnixNano(), "settings", 0, 0, blob)
	} else {
		_, err = s.db.Exec("UPDATE capture_items SET envelope=? WHERE receipt_key=?", blob, id)
	}
	if err != nil {
		return ErrStorageFailedSentinel
	}
	return nil
}

func (s *Store) markAttempt(id string, env storedEnvelope, request CaptureRequest, credentialID string) error {
	env.Request = &request
	env.CredentialID = credentialID
	env.DeliveryAttempted = true
	return s.updateEnvelope(id, env, time.Time{}, 0)
}

func (s *Store) due(now time.Time, limit int) ([]struct {
	id       string
	env      storedEnvelope
	attempts int
}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listDue(now, limit)
}

func (s *Store) counts() (map[ReceiptState]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.countStates()
}

// wakeRecoverable makes captures blocked on a temporarily mismatched remote
// identity eligible as soon as that exact server/fingerprint is restored.
func (s *Store) wakeRecoverable(binding ServiceConfiguration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT receipt_key,envelope,attempts FROM capture_items WHERE state='blocked'")
	if err != nil {
		return ErrStorageFailedSentinel
	}
	type blocked struct {
		id       string
		env      storedEnvelope
		attempts int
	}
	var eligible []blocked
	for rows.Next() {
		var item blocked
		var blob []byte
		if rows.Scan(&item.id, &blob, &item.attempts) != nil {
			rows.Close()
			return ErrStorageFailedSentinel
		}
		item.env, err = s.open(item.id, blob)
		if err != nil {
			rows.Close()
			return err
		}
		if item.env.Request != nil && recoverableDeliveryCode(item.env.Receipt.DeliveryCode) && sameBinding(item.env.Request.Binding, binding) {
			eligible = append(eligible, item)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return ErrStorageFailedSentinel
	}
	if err = rows.Close(); err != nil {
		return ErrStorageFailedSentinel
	}
	for _, item := range eligible {
		item.env.Receipt.NextAttemptAt = ""
		if err = s.update(item.id, item.env, time.Now(), item.attempts); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) receipts(req ListReceiptsRequest) (ListReceiptsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listReceipts(req)
}

func (s *Store) discard(receiptID string) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, env, err := s.findByReceipt(receiptID)
	if err != nil {
		return Receipt{}, err
	}
	if env.Receipt.State == ReceiptDelivered || env.Request == nil {
		return Receipt{}, ErrDiscardNotAllowedSentinel
	}
	env.Receipt.State = ReceiptDiscarded
	env.Receipt.Content = nil
	env.Receipt.DeliveryCode = ""
	env.Receipt.NextAttemptAt = ""
	env.Request = nil
	env.TombstoneUntil = time.Now().Add(ReceiptRetention).UTC().Format(time.RFC3339Nano)
	if err = s.update(id, env, time.Time{}, 0); err != nil {
		return Receipt{}, err
	}
	return env.Receipt, nil
}

func (s *Store) prune(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT receipt_key,envelope,created_ns FROM capture_items WHERE state IN ('delivered','discarded')")
	if err != nil {
		return ErrStorageFailedSentinel
	}
	type expiredRow struct{ id string }
	var expired []expiredRow
	for rows.Next() {
		var id string
		var blob []byte
		var created int64
		if rows.Scan(&id, &blob, &created) != nil {
			rows.Close()
			return ErrStorageFailedSentinel
		}
		env, e := s.open(id, blob)
		if e != nil {
			rows.Close()
			return e
		}
		until := time.Unix(0, created).Add(ReceiptRetention)
		if env.TombstoneUntil != "" {
			if parsed, parseErr := time.Parse(time.RFC3339Nano, env.TombstoneUntil); parseErr == nil {
				until = parsed
			}
		}
		if !now.Before(until) {
			expired = append(expired, expiredRow{id: id})
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return ErrStorageFailedSentinel
	}
	rows.Close()
	for _, row := range expired {
		if _, err = s.db.Exec("DELETE FROM capture_items WHERE receipt_key=?", row.id); err != nil {
			return ErrStorageFailedSentinel
		}
	}
	return nil
}

func (s *Store) findByReceipt(receiptID string) (string, storedEnvelope, error) {
	key := s.digest(receiptID)
	var storedKey string
	var blob []byte
	err := s.db.QueryRow("SELECT receipt_key,envelope FROM capture_items WHERE receipt_key=?", key).Scan(&storedKey, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return "", storedEnvelope{}, ErrReceiptNotFoundSentinel
	}
	if err != nil {
		return "", storedEnvelope{}, ErrStorageFailedSentinel
	}
	env, err := s.open(storedKey, blob)
	return storedKey, env, err
}

func (s *Store) update(id string, env storedEnvelope, next time.Time, attempts int) error {
	blob, err := s.seal(id, env)
	if err != nil {
		return err
	}
	nextAttempt := int64(0)
	if !next.IsZero() {
		nextAttempt = next.UnixNano()
	}
	res, err := s.db.Exec("UPDATE capture_items SET state=?,next_attempt_ns=?,attempts=?,envelope=? WHERE receipt_key=?", env.Receipt.State, nextAttempt, attempts, blob, id)
	if err != nil {
		return ErrStorageFailedSentinel
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrStorageFailedSentinel
	}
	return nil
}

func (s *Store) getConfiguration() (ServiceConfiguration, error) {
	var id string
	var blob []byte
	err := s.db.QueryRow("SELECT receipt_key,envelope FROM capture_items WHERE state='configuration' LIMIT 1").Scan(&id, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return ServiceConfiguration{}, nil
	}
	if err != nil {
		return ServiceConfiguration{}, ErrStorageFailedSentinel
	}
	env, err := s.open(id, blob)
	if err != nil {
		return ServiceConfiguration{}, err
	}
	if env.Configuration == nil {
		return ServiceConfiguration{}, ErrStorageFailedSentinel
	}
	return *env.Configuration, nil
}

func (s *Store) setConfiguration(config ServiceConfiguration) error {
	rows, err := s.db.Query("SELECT receipt_key,envelope FROM capture_items WHERE state='configuration'")
	if err != nil {
		return ErrStorageFailedSentinel
	}
	var ids []string
	for rows.Next() {
		var id string
		var blob []byte
		if rows.Scan(&id, &blob) != nil {
			rows.Close()
			return ErrStorageFailedSentinel
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if _, err = s.db.Exec("DELETE FROM capture_items WHERE receipt_key=?", id); err != nil {
			return ErrStorageFailedSentinel
		}
	}
	id, err := safeID()
	if err != nil {
		return err
	}
	env := storedEnvelope{Version: envelopeV1, Configuration: &config}
	blob, err := s.seal(id, env)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO capture_items(receipt_key,request_key,created_ns,state,next_attempt_ns,attempts,envelope) VALUES(?,?,?,?,?,?,?)", id, s.digest("configuration"), time.Now().UnixNano(), "configuration", 0, 0, blob)
	if err != nil {
		return ErrStorageFailedSentinel
	}
	return nil
}

func (s *Store) listDue(now time.Time, limit int) ([]struct {
	id       string
	env      storedEnvelope
	attempts int
}, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 64 {
		limit = 64
	}
	rows, err := s.db.Query("SELECT receipt_key,envelope,attempts FROM capture_items WHERE state IN ('pending','blocked') AND next_attempt_ns<=? ORDER BY created_ns LIMIT ?", now.UnixNano(), limit)
	if err != nil {
		return nil, ErrStorageFailedSentinel
	}
	defer rows.Close()
	var out []struct {
		id       string
		env      storedEnvelope
		attempts int
	}
	for rows.Next() {
		var id string
		var blob []byte
		var n int
		if rows.Scan(&id, &blob, &n) != nil {
			return nil, ErrStorageFailedSentinel
		}
		env, e := s.open(id, blob)
		if e != nil {
			return nil, e
		}
		if env.Request != nil {
			out = append(out, struct {
				id       string
				env      storedEnvelope
				attempts int
			}{id, env, n})
		}
	}
	return out, rows.Err()
}

func (s *Store) countStates() (map[ReceiptState]int, error) {
	rows, err := s.db.Query("SELECT state,count(*) FROM capture_items WHERE state NOT IN ('configuration','settings') GROUP BY state")
	if err != nil {
		return nil, ErrStorageFailedSentinel
	}
	defer rows.Close()
	out := map[ReceiptState]int{}
	for rows.Next() {
		var st string
		var n int
		if rows.Scan(&st, &n) != nil {
			return nil, ErrStorageFailedSentinel
		}
		out[ReceiptState(st)] = n
	}
	return out, rows.Err()
}

func (s *Store) listReceipts(req ListReceiptsRequest) (ListReceiptsResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	query := "SELECT receipt_key,envelope FROM capture_items WHERE state NOT IN ('configuration','settings')"
	args := []any{}
	if req.Cursor != "" {
		cursorKey := s.digest(req.Cursor)
		var cursorCreated int64
		if err := s.db.QueryRow("SELECT created_ns FROM capture_items WHERE receipt_key=? AND state NOT IN ('configuration','settings')", cursorKey).Scan(&cursorCreated); errors.Is(err, sql.ErrNoRows) {
			return ListReceiptsResponse{}, ErrReceiptNotFoundSentinel
		} else if err != nil {
			return ListReceiptsResponse{}, ErrStorageFailedSentinel
		}
		query += " AND (created_ns < ? OR (created_ns = ? AND receipt_key < ?))"
		args = append(args, cursorCreated, cursorCreated, cursorKey)
	}
	if req.State != "" {
		query += " AND state=?"
		args = append(args, req.State)
	}
	query += " ORDER BY created_ns DESC,receipt_key DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return ListReceiptsResponse{}, ErrStorageFailedSentinel
	}
	defer rows.Close()
	var envs []storedEnvelope
	for rows.Next() {
		var id string
		var blob []byte
		if rows.Scan(&id, &blob) != nil {
			return ListReceiptsResponse{}, ErrStorageFailedSentinel
		}
		env, e := s.open(id, blob)
		if e != nil {
			return ListReceiptsResponse{}, e
		}
		envs = append(envs, env)
	}
	if err = rows.Err(); err != nil {
		return ListReceiptsResponse{}, ErrStorageFailedSentinel
	}
	more := len(envs) > limit
	if more {
		envs = envs[:limit]
	}
	result := ListReceiptsResponse{Receipts: make([]Receipt, 0, len(envs))}
	for _, env := range envs {
		// A listing is deliberately a bounded index, not a payload transfer.
		// Large queued captures are available through lookup_receipt one at a
		// time, preventing a valid queue from producing an oversized IPC frame.
		receipt := env.Receipt
		receipt.Content = nil
		result.Receipts = append(result.Receipts, receipt)
	}
	if more && len(result.Receipts) > 0 {
		result.NextCursor = result.Receipts[len(result.Receipts)-1].ReceiptID
	}
	return result, nil
}

func validateConfiguration(c ServiceConfiguration) error {
	if strings.TrimSpace(c.ServerURL) == "" || strings.TrimSpace(c.CredentialFingerprint) == "" || strings.TrimSpace(c.PublicKeyPath) == "" || strings.TrimSpace(c.SigningKeyPath) == "" || len(c.ServerURL) > 2048 || len(c.CredentialFingerprint) > 256 || len(c.PublicKeyPath) > 4096 || len(c.SigningKeyPath) > 4096 {
		return ErrStorageFailedSentinel
	}
	return nil
}

func canonicalURL(raw string) string { return strings.TrimRight(strings.TrimSpace(raw), "/") }
