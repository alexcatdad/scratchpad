// Package scratchpad implements Scratchpad's local MCP integration.
package scratchpad

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Config struct {
	URL, PublicKeyPath, SigningKeyPath, WorkingDirectory, MirrorPath string
	Mirror                                                           bool
}
type Client struct {
	Config  Config
	HTTP    *http.Client
	mu      sync.Mutex
	token   string
	expires time.Time
}
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }
func NewClient(c Config) (*Client, error) {
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("SCRATCHPAD_URL must be an absolute HTTP(S) URL")
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
		return nil, errors.New("remote Scratchpad connections require HTTPS")
	}
	if c.WorkingDirectory == "" {
		c.WorkingDirectory, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	if c.MirrorPath == "" {
		c.MirrorPath = "scratchpad/decisions.jsonl"
	}
	return &Client{Config: c, HTTP: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) raw(ctx context.Context, method, path string, body any, key, token string, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Config.URL, "/")+"/api/v1"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err = io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var envelope struct {
			Error APIError `json:"error"`
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.Error.Code != "" {
			return &envelope.Error
		}
		return fmt.Errorf("Scratchpad HTTP %d", res.StatusCode)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}
func (c *Client) authenticate(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expires.Add(-time.Minute)) {
		return c.token, nil
	}
	pub, err := os.ReadFile(c.Config.PublicKeyPath)
	if err != nil {
		return "", fmt.Errorf("read enrolled public key: %w", err)
	}
	var challenge struct {
		ChallengeID string `json:"challengeId"`
		Nonce       string `json:"nonce"`
		Namespace   string `json:"namespace"`
	}
	if err = c.raw(ctx, "POST", "/auth/mcp/challenge", map[string]string{"publicKey": strings.TrimSpace(string(pub))}, "", "", &challenge); err != nil {
		return "", err
	}
	if challenge.Namespace != "scratchpad-auth" || challenge.Nonce == "" {
		return "", errors.New("invalid signing challenge")
	}
	key := c.Config.SigningKeyPath
	if key == "" {
		key = c.Config.PublicKeyPath
	}
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-Y", "sign", "-f", key, "-n", "scratchpad-auth")
	cmd.Stdin = strings.NewReader(challenge.Nonce)
	signature, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("sign challenge (unlock your SSH agent/key): %w", err)
	}
	var session struct {
		Token   string    `json:"accessToken"`
		Expires time.Time `json:"expiresAt"`
	}
	if err = c.raw(ctx, "POST", "/auth/mcp/verify", map[string]string{"challengeId": challenge.ChallengeID, "publicKey": strings.TrimSpace(string(pub)), "signature": string(signature)}, "", "", &session); err != nil {
		return "", err
	}
	if session.Token == "" || !session.Expires.After(time.Now()) {
		return "", errors.New("invalid session response")
	}
	c.token = session.Token
	c.expires = session.Expires
	return c.token, nil
}
func (c *Client) request(ctx context.Context, method, path string, body any, key string, out any) error {
	token, err := c.authenticate(ctx)
	if err != nil {
		return err
	}
	err = c.raw(ctx, method, path, body, key, token, out)
	var apiErr *APIError
	if errors.As(err, &apiErr) && (apiErr.Code == "AUTH_REQUIRED" || apiErr.Code == "AUTH_INVALID") {
		c.mu.Lock()
		if c.token == token {
			c.token = ""
		}
		c.mu.Unlock()
	}
	return err
}
