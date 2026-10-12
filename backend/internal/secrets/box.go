package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
)

type Box struct {
	aead  cipher.AEAD
	issue string
}
type Envelope struct {
	Version    int    `json:"version"`
	Algorithm  string `json:"algorithm"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// Follow projected Kubernetes secret symlinks, checking the opened regular file.
// A missing/invalid key affects secret operations, never ordinary memory startup.
func Load(path string) *Box {
	b := &Box{issue: "KEY_MISSING"}
	if path == "" {
		return b
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			b.issue = "KEY_INVALID"
		}
		return b
	}
	defer file.Close()
	b.issue = "KEY_INVALID"
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0027 != 0 {
		return b
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return b
	}
	key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(key) != 32 {
		return b
	}
	block, err := aes.NewCipher(key)
	for i := range key {
		key[i] = 0
	}
	if err != nil {
		return b
	}
	b.aead, err = cipher.NewGCM(block)
	if err != nil {
		return b
	}
	b.issue = ""
	return b
}
func (b *Box) Available() bool { return b.aead != nil }
func associated(kind, id, field string) []byte {
	return []byte("scratchpad.secret.v1\x00" + kind + "\x00" + id + "\x00" + field)
}
func (b *Box) Seal(value, kind, id, field string) (any, error) {
	if value == "" {
		return "", nil
	}
	if b.aead == nil {
		return nil, errors.New(b.issue)
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.New("SECRET_INVALID")
	}
	ciphertext := b.aead.Seal(nil, nonce, []byte(value), associated(kind, id, field))
	return Envelope{1, "AES-256-GCM", base64.RawURLEncoding.EncodeToString(nonce), base64.RawURLEncoding.EncodeToString(ciphertext)}, nil
}
func (b *Box) Open(value any, kind, id, field string) (string, error) {
	if value == nil || value == "" {
		return "", nil
	}
	if _, legacy := value.(string); legacy {
		return "", errors.New("LEGACY_PENDING")
	}
	if b.aead == nil {
		return "", errors.New(b.issue)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("SECRET_INVALID")
	}
	var envelope Envelope
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || envelope.Version != 1 || envelope.Algorithm != "AES-256-GCM" {
		return "", errors.New("SECRET_INVALID")
	}
	nonce, err := base64.RawURLEncoding.Strict().DecodeString(envelope.Nonce)
	if err != nil || len(nonce) != b.aead.NonceSize() {
		return "", errors.New("SECRET_INVALID")
	}
	ciphertext, err := base64.RawURLEncoding.Strict().DecodeString(envelope.Ciphertext)
	if err != nil {
		return "", errors.New("SECRET_INVALID")
	}
	plain, err := b.aead.Open(nil, nonce, ciphertext, associated(kind, id, field))
	if err != nil {
		return "", errors.New("SECRET_INVALID")
	}
	return string(plain), nil
}
func (b *Box) Protection(value any) map[string]any {
	if value == nil || value == "" {
		return map[string]any{"status": "not_required"}
	}
	if _, legacy := value.(string); legacy {
		return map[string]any{"status": "pending_conversion"}
	}
	if _, err := b.Open(value, "ai_settings", "global", "apiKey"); err != nil {
		return map[string]any{"status": "unavailable", "error": err.Error()}
	}
	return map[string]any{"status": "protected"}
}
