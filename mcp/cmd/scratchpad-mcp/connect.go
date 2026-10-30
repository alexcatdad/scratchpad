package main

// connect and status configure and inspect the local capture service from a
// terminal or agent, without the tray app. They use the same discovery
// sources, server eligibility check and possession proof as the app.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
)

const connectUsage = `Usage:
  scratchpad-mcp connect --server <https-origin> [--public-key <file.pub>] [--agent-socket <path>]
  scratchpad-mcp status`

type keyCandidate struct {
	publicKey   string // "<type> <base64>", no comment
	fingerprint string
	source      string
	agentSocket string
}

func runConnect(args []string) int {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	server := fs.String("server", "", "")
	publicKeyFile := fs.String("public-key", "", "")
	agentSocket := fs.String("agent-socket", "", "")
	if err := fs.Parse(args); err != nil || *server == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, connectUsage)
		return 2
	}
	origin, err := serverOrigin(*server)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	sockets := agentSockets(*agentSocket)
	var candidates []keyCandidate
	if *publicKeyFile != "" {
		key, err := readPublicKeyFile(*publicKeyFile, firstOr(sockets, ""))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		candidates = []keyCandidate{key}
	} else {
		candidates = discoverKeys(sockets)
	}
	if len(candidates) == 0 {
		fmt.Fprintln(os.Stderr, "No SSH public keys found in the SSH agent or ~/.ssh/*.pub. Load your key with ssh-add and retry.")
		return 1
	}
	fmt.Printf("Checking %d local key(s) with %s\n", len(candidates), origin)
	eligible, err := eligibleKeys(origin, candidates)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(eligible) == 0 {
		fmt.Fprintln(os.Stderr, "The server accepted none of these keys. Link GitHub in the dashboard (Settings) and wait for key sync, or enroll a key manually.")
		return 1
	}
	key := eligible[0]
	if len(eligible) > 1 {
		fmt.Printf("%d keys accepted; using %s (%s). Pass --public-key to choose another.\n", len(eligible), key.fingerprint, key.source)
	}
	cfg, err := keyConfiguration(origin, key)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	socketPath := os.Getenv("SCRATCHPAD_SERVICE_SOCKET")
	if err := ensureCollector(ctx, socketPath, launchCollector); err != nil {
		fmt.Fprintln(os.Stderr, "Scratchpad capture service is not running. Start it from the tray app, then retry.")
		return 1
	}
	fmt.Printf("Proving possession of %s; approve or unlock your SSH agent if asked.\n", key.fingerprint)
	result, err := localcapture.NewClient(socketPath).VerifyConfiguration(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Connection failed: %v\n", err)
		return 1
	}
	fmt.Printf("Connected and saved.\n  server:     %s\n  credential: %s\n  key:        %s\n  verified:   %s\n", result.ServerURL, result.CredentialID, key.fingerprint, result.VerifiedAt)
	return 0
}

func runStatus() int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := localcapture.NewClient(os.Getenv("SCRATCHPAD_SERVICE_SOCKET"))
	status, err := client.Status(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Capture service unavailable: %v\n", err)
		return 1
	}
	cfg, err := client.GetConfiguration(ctx)
	out := map[string]any{"service": status}
	if err == nil {
		out["configuration"] = cfg
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	return 0
}

func serverOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	invalid := errors.New("server must be an HTTPS origin (or loopback HTTP), without path, query or credentials")
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", invalid
	}
	host := strings.ToLower(u.Hostname())
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", invalid
	}
	return u.Scheme + "://" + u.Host, nil
}

func agentSockets(preferred string) []string {
	var out []string
	add := func(s string) {
		if s == "" {
			return
		}
		for _, existing := range out {
			if existing == s {
				return
			}
		}
		if _, err := os.Stat(s); err == nil {
			out = append(out, s)
		}
	}
	add(preferred)
	add(os.Getenv("SSH_AUTH_SOCK"))
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, "Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock"))
	}
	return out
}

func discoverKeys(sockets []string) []keyCandidate {
	var candidates []keyCandidate
	for _, socket := range sockets {
		cmd := exec.Command("ssh-add", "-L")
		cmd.Env = append(os.Environ(), "SSH_AUTH_SOCK="+socket)
		output, err := cmd.Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(output), "\n") {
			if key, ok := parsePublicKey(line, "SSH agent "+socket, socket); ok {
				candidates = append(candidates, key)
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		files, _ := filepath.Glob(filepath.Join(home, ".ssh", "*.pub"))
		sort.Strings(files)
		for _, file := range files {
			if key, err := readPublicKeyFile(file, firstOr(sockets, "")); err == nil {
				candidates = append(candidates, key)
			}
		}
	}
	seen := map[string]bool{}
	var unique []keyCandidate
	for _, key := range candidates {
		if !seen[key.fingerprint] {
			seen[key.fingerprint] = true
			unique = append(unique, key)
		}
	}
	if len(unique) > 16 {
		unique = unique[:16]
	}
	return unique
}

func readPublicKeyFile(path, socket string) (keyCandidate, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32768 {
		return keyCandidate{}, fmt.Errorf("%s is not a readable public key file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return keyCandidate{}, err
	}
	key, ok := parsePublicKey(string(data), filepath.Base(path), socket)
	if !ok {
		return keyCandidate{}, fmt.Errorf("%s is not an OpenSSH public key", path)
	}
	return key, nil
}

func parsePublicKey(line, source, socket string) (keyCandidate, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "ssh-") && !strings.HasPrefix(fields[0], "ecdsa-") && !strings.HasPrefix(fields[0], "sk-") {
		return keyCandidate{}, false
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil || len(blob) == 0 {
		return keyCandidate{}, false
	}
	sum := sha256.Sum256(blob)
	return keyCandidate{
		publicKey:   fields[0] + " " + fields[1],
		fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:]),
		source:      source,
		agentSocket: socket,
	}, true
}

func eligibleKeys(origin string, candidates []keyCandidate) ([]keyCandidate, error) {
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var eligible []keyCandidate
	for _, key := range candidates {
		body, _ := json.Marshal(map[string]string{"publicKey": key.publicKey})
		resp, err := client.Post(origin+"/api/v1/auth/mcp/challenge", "application/json", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("server unreachable: %v", err)
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
		resp.Body.Close()
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		switch {
		case resp.StatusCode == http.StatusOK:
			var challenge struct {
				Version   int    `json:"version"`
				Recipient string `json:"recipient"`
			}
			if json.Unmarshal(data, &challenge) != nil || challenge.Version != 2 || challenge.Recipient != origin {
				return nil, errors.New("server returned an unexpected challenge; check the server URL")
			}
			eligible = append(eligible, key)
		case resp.StatusCode == http.StatusUnauthorized && failure.Error.Code == "AUTH_INVALID":
		case resp.StatusCode == http.StatusTooManyRequests:
			return nil, errors.New("server rate-limited key checks; wait a minute and retry")
		default:
			return nil, fmt.Errorf("server rejected the key check (HTTP %d)", resp.StatusCode)
		}
	}
	return eligible, nil
}

// keyConfiguration stores the public key where the app does and references it
// as both public key and signer; the agent holds the private key.
func keyConfiguration(origin string, key keyCandidate) (localcapture.ServiceConfiguration, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return localcapture.ServiceConfiguration{}, err
	}
	dir := filepath.Join(base, "Scratchpad", "public-keys")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return localcapture.ServiceConfiguration{}, err
	}
	sum := sha256.Sum256([]byte(key.publicKey))
	path := filepath.Join(dir, hex.EncodeToString(sum[:])+".pub")
	if err := os.WriteFile(path, []byte(key.publicKey+"\n"), 0o600); err != nil {
		return localcapture.ServiceConfiguration{}, err
	}
	return localcapture.ServiceConfiguration{
		ServerURL:             origin,
		CredentialFingerprint: key.fingerprint,
		PublicKeyPath:         path,
		SigningKeyPath:        path,
		SSHAgentSocket:        key.agentSocket,
	}, nil
}

func firstOr(values []string, fallback string) string {
	if len(values) > 0 {
		return values[0]
	}
	return fallback
}
