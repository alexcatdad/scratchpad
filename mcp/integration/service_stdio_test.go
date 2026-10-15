package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexcatdad/scratchpad/mcp/internal/delivery"
	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
	"github.com/alexcatdad/scratchpad/mcp/internal/scratchpad"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type integrationKey struct{ key []byte }

func (k integrationKey) Load(context.Context) ([]byte, error) {
	return append([]byte(nil), k.key...), nil
}
func (k integrationKey) Create(context.Context) ([]byte, error) {
	return append([]byte(nil), k.key...), nil
}

type integrationChallenge struct {
	publicKey string
	nonce     string
	recipient string
	expires   string
}

func TestDurableCaptureStdioHelper(t *testing.T) {
	if os.Getenv("SCRATCHPAD_DURABLE_MCP_HELPER") != "1" {
		t.Skip("stdio child helper")
	}
	client, err := scratchpad.NewClient(scratchpad.Config{
		URL: os.Getenv("SCRATCHPAD_TEST_URL"), PublicKeyPath: os.Getenv("SCRATCHPAD_TEST_PUBLIC_KEY"),
		SigningKeyPath: os.Getenv("SCRATCHPAD_TEST_SIGNING_KEY"), Mirror: os.Getenv("SCRATCHPAD_TEST_MIRROR") == "true",
		MirrorPath: os.Getenv("SCRATCHPAD_TEST_MIRROR_PATH"),
	})
	if err != nil {
		t.Fatal(err)
	}
	client.CaptureService = localcapture.NewClient(os.Getenv("SCRATCHPAD_TEST_SOCKET"))
	if err = scratchpad.NewServer(client).Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func TestRealStdioMCPToDurableServiceToSSHHTTP(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "synthetic-owner")
	if output, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", keyPath).CombinedOutput(); err != nil {
		t.Fatalf("create synthetic SSH key: %v (%s)", err, output)
	}
	publicKey, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(publicKey))
	if len(fields) < 2 {
		t.Fatal("synthetic public key is malformed")
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(blob)
	fingerprint := "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:])
	allowedSigners := filepath.Join(t.TempDir(), "allowed_signers")
	if err = os.WriteFile(allowedSigners, []byte("synthetic-owner namespaces=\"scratchpad-auth-v2\" "+strings.Join(fields[:2], " ")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	challenges := map[string]integrationChallenge{}
	created := map[string]map[string]any{}
	requestIDs := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/auth/mcp/challenge" && r.Method == http.MethodPost:
			var input struct {
				PublicKey string `json:"publicKey"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || strings.TrimSpace(input.PublicKey) != strings.TrimSpace(string(publicKey)) {
				http.Error(w, "invalid synthetic owner", http.StatusUnauthorized)
				return
			}
			id := fmt.Sprintf("challenge-%d", time.Now().UnixNano())
			expires := time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
			challenge := integrationChallenge{publicKey: input.PublicKey, nonce: id + "-nonce", recipient: "http://" + r.Host, expires: expires}
			mu.Lock()
			challenges[id] = challenge
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"challengeId": id, "nonce": challenge.nonce, "namespace": "scratchpad-auth-v2", "version": 2, "recipient": challenge.recipient, "purpose": "ssh_login", "expiresAt": expires})
		case r.URL.Path == "/api/v1/auth/mcp/verify" && r.Method == http.MethodPost:
			var input struct {
				ChallengeID string `json:"challengeId"`
				PublicKey   string `json:"publicKey"`
				Signature   string `json:"signature"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				http.Error(w, "invalid synthetic proof", http.StatusUnauthorized)
				return
			}
			mu.Lock()
			challenge, ok := challenges[input.ChallengeID]
			delete(challenges, input.ChallengeID)
			mu.Unlock()
			if !ok || strings.TrimSpace(input.PublicKey) != strings.TrimSpace(challenge.publicKey) {
				http.Error(w, "unknown synthetic challenge", http.StatusUnauthorized)
				return
			}
			proof, _ := json.Marshal([]any{"scratchpad-ssh-proof", 2, challenge.recipient, "ssh_login", input.ChallengeID, challenge.nonce, strings.Join(fields[:2], " "), challenge.expires})
			if err := verifySyntheticSignature(allowedSigners, input.Signature, proof); err != nil {
				http.Error(w, "invalid synthetic SSH signature", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "synthetic-session", "expiresAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), "credentialId": "credential-synthetic", "captureCapabilities": []string{localcapture.CapabilityClientSavedAtV1}})
		case r.URL.Path == "/api/v1/projects/resolve" && r.Method == http.MethodPost:
			var input struct {
				Context struct {
					Git scratchpad.GitContext `json:"git"`
				} `json:"context"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.Context.Git.RepositoryIdentity != "github.com/synthetic/durable-service" {
				http.Error(w, "invalid synthetic project identity", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"project": map[string]any{"id": "project-synthetic", "name": "Synthetic project"}})
		case strings.HasPrefix(r.URL.Path, "/api/v1/records/") && strings.HasSuffix(r.URL.Path, "/mirror") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/v1/records" && r.Method == http.MethodPost:
			if r.Header.Get("Authorization") != "Bearer synthetic-session" {
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			var input map[string]any
			decoder := json.NewDecoder(r.Body)
			decoder.UseNumber()
			if decoder.Decode(&input) != nil {
				http.Error(w, "invalid synthetic capture", http.StatusBadRequest)
				return
			}
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				http.Error(w, "missing retry key", http.StatusBadRequest)
				return
			}
			mu.Lock()
			recordID := requestIDs[key]
			if recordID == "" {
				recordID = fmt.Sprintf("record-%d", len(created)+1)
				requestIDs[key] = recordID
				record := input["record"].(map[string]any)
				record["id"], record["projectId"] = recordID, input["projectId"]
				if git, present := input["gitContext"]; present {
					record["gitContext"] = git
				}
				record["recordedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
				created[recordID] = record
			}
			record := created[recordID]
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"record": record, "mirror": map[string]any{"eligible": true}})
		case r.URL.Path == "/api/v1/projects" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"projects": []any{map[string]any{"id": "project-synthetic", "name": "Synthetic project"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := localcapture.OpenStore(filepath.Join(t.TempDir(), "queue.sqlite"), integrationKey{key: []byte("0123456789abcdef0123456789abcdef")})
	if err != nil {
		t.Fatal(err)
	}
	service, err := localcapture.NewRuntime(ctx, localcapture.ServiceOptions{Store: store, Adapter: delivery.NewAdapter(), RetryMin: 10 * time.Millisecond, RetryMax: 25 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	configuration := localcapture.ServiceConfiguration{ServerURL: server.URL, CredentialFingerprint: fingerprint, PublicKeyPath: keyPath + ".pub", SigningKeyPath: keyPath}
	if err = service.SetConfiguration(ctx, configuration); err != nil {
		t.Fatal(err)
	}
	socketDirectory, err := os.MkdirTemp("/private/tmp", "sp-")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(socketDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	socketPath := filepath.Join(socketDirectory, "service.sock")
	ipc, err := localcapture.ListenIPC(socketPath, service)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ipc.Serve(ctx) }()

	workspace := t.TempDir()
	runIntegrationGit(t, workspace, "init", "-b", "main")
	runIntegrationGit(t, workspace, "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "commit", "--allow-empty", "-m", "initial")
	runIntegrationGit(t, workspace, "remote", "add", "origin", "git@github.com:synthetic/durable-service.git")

	processContext, processCancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer processCancel()
	command := exec.CommandContext(processContext, os.Args[0], "-test.run=^TestDurableCaptureStdioHelper$")
	command.Dir = workspace
	command.Env = append(os.Environ(), "SCRATCHPAD_DURABLE_MCP_HELPER=1", "SCRATCHPAD_TEST_URL="+server.URL, "SCRATCHPAD_TEST_PUBLIC_KEY="+keyPath+".pub", "SCRATCHPAD_TEST_SIGNING_KEY="+keyPath, "SCRATCHPAD_TEST_SOCKET="+socketPath)
	command.Stderr = os.Stderr
	session, err := mcp.NewClient(&mcp.Implementation{Name: "durable-integration", Version: "test"}, nil).Connect(processContext, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("start real MCP stdio process: %v", err)
	}
	defer session.Close()
	listed, err := session.ListTools(processContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 21 {
		t.Fatalf("MCP tools count %d, expected 21", len(listed.Tools))
	}
	fieldsByType := map[string]map[string]any{
		"decision": {"decision": "Preserve immutable receipts"}, "adr": {"decision": "Use durable local intake"},
		"business_decision": {"decision": "Keep central authority"}, "finding": {"finding": "Offline capture remains queued"},
		"qa": {"question": "Was the local receipt retained?"}, "failure": {"observed": "Synthetic temporary outage"},
		"constraint": {"constraint": "No key bytes enter the queue"}, "project_state": {"state": "active"},
	}
	var retryArguments map[string]any
	var retryRecordID string
	for kind, payload := range fieldsByType {
		arguments := map[string]any{"requestId": "integration-" + kind, "title": "Synthetic " + kind, "authority": "observed", "confidence": "high", "workingDirectory": workspace}
		for key, value := range payload {
			arguments[key] = value
		}
		result, callErr := session.CallTool(processContext, &mcp.CallToolParams{Name: "record_" + kind, Arguments: arguments})
		if callErr != nil || result.IsError {
			t.Fatalf("record_%s failed: %+v %v", kind, result, callErr)
		}
		var output struct {
			Outcome string `json:"outcome"`
			Receipt struct {
				ReceiptID string `json:"receiptId"`
				State     string `json:"state"`
			} `json:"receipt"`
			Record  map[string]any `json:"record"`
			Project map[string]any `json:"project"`
		}
		encoded, _ := json.Marshal(result.StructuredContent)
		if err = json.Unmarshal(encoded, &output); err != nil {
			t.Fatal(err)
		}
		if output.Outcome != string(localcapture.OutcomeDelivered) || output.Record["clientSavedAt"] == nil || output.Record["projectId"] != "project-synthetic" || output.Project["id"] != "project-synthetic" {
			t.Fatalf("record_%s omitted its delivered central receipt: %s", kind, encoded)
		}
		if kind == "decision" {
			retryArguments = arguments
			retryRecordID, _ = output.Record["id"].(string)
		}
		receipt, receiptErr := session.CallTool(processContext, &mcp.CallToolParams{Name: "get_capture_receipt", Arguments: map[string]any{"receiptId": output.Receipt.ReceiptID}})
		if receiptErr != nil || receipt.IsError {
			t.Fatalf("receipt lookup for %s failed: %+v %v", kind, receipt, receiptErr)
		}
		if receipt.StructuredContent.(map[string]any)["state"] != string(localcapture.ReceiptDelivered) {
			t.Fatalf("receipt lookup for %s did not report delivered: %+v", kind, receipt.StructuredContent)
		}
	}
	retried, err := session.CallTool(processContext, &mcp.CallToolParams{Name: "record_decision", Arguments: retryArguments})
	if err != nil || retried.IsError {
		t.Fatalf("stable MCP request retry failed: %+v %v", retried, err)
	}
	var retryOutput struct {
		Record map[string]any `json:"record"`
	}
	encodedRetry, _ := json.Marshal(retried.StructuredContent)
	if err = json.Unmarshal(encodedRetry, &retryOutput); err != nil {
		t.Fatal(err)
	}
	if retryOutput.Record["id"] != retryRecordID {
		t.Fatalf("stable request retry changed central record: %s", encodedRetry)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "scratchpad", "decisions.jsonl")); !os.IsNotExist(statErr) {
		t.Fatalf("client with mirroring disabled wrote a repository mirror: %v", statErr)
	}

	mirrorWorkspace := t.TempDir()
	runIntegrationGit(t, mirrorWorkspace, "init", "-b", "main")
	runIntegrationGit(t, mirrorWorkspace, "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "commit", "--allow-empty", "-m", "initial")
	runIntegrationGit(t, mirrorWorkspace, "remote", "add", "origin", "git@github.com:synthetic/durable-service.git")
	mirrorCommand := exec.CommandContext(processContext, os.Args[0], "-test.run=^TestDurableCaptureStdioHelper$")
	mirrorCommand.Dir = mirrorWorkspace
	mirrorCommand.Env = append(os.Environ(), "SCRATCHPAD_DURABLE_MCP_HELPER=1", "SCRATCHPAD_TEST_URL="+server.URL, "SCRATCHPAD_TEST_PUBLIC_KEY="+keyPath+".pub", "SCRATCHPAD_TEST_SIGNING_KEY="+keyPath, "SCRATCHPAD_TEST_SOCKET="+socketPath, "SCRATCHPAD_TEST_MIRROR=true")
	mirrorCommand.Stderr = os.Stderr
	mirrorSession, err := mcp.NewClient(&mcp.Implementation{Name: "durable-mirror-integration", Version: "test"}, nil).Connect(processContext, &mcp.CommandTransport{Command: mirrorCommand}, nil)
	if err != nil {
		t.Fatalf("start mirror-enabled stdio MCP process: %v", err)
	}
	defer mirrorSession.Close()
	mirrored, err := mirrorSession.CallTool(processContext, &mcp.CallToolParams{Name: "record_decision", Arguments: map[string]any{
		"requestId": "integration-explicit-mirror", "title": "Synthetic opted-in mirror", "authority": "explicit", "confidence": "high", "decision": "Only the originating MCP consented.", "workingDirectory": mirrorWorkspace,
	}})
	if err != nil || mirrored.IsError {
		t.Fatalf("mirror-enabled capture failed: %+v %v", mirrored, err)
	}
	var mirrorOutput struct {
		Mirror map[string]any `json:"mirror"`
	}
	encodedMirror, _ := json.Marshal(mirrored.StructuredContent)
	if err = json.Unmarshal(encodedMirror, &mirrorOutput); err != nil {
		t.Fatal(err)
	}
	if mirrorOutput.Mirror["attempted"] != true || mirrorOutput.Mirror["succeeded"] != true {
		t.Fatalf("originating MCP's explicit repository consent was not honored: %s", encodedMirror)
	}
	line, err := os.ReadFile(filepath.Join(mirrorWorkspace, "scratchpad", "decisions.jsonl"))
	if err != nil || !strings.Contains(string(line), "Synthetic opted-in mirror") {
		t.Fatalf("consenting workspace did not receive its central record: %s %v", line, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(created) != 9 {
		t.Fatalf("central server committed %d records, expected nine", len(created))
	}
}

func verifySyntheticSignature(allowed, signature string, proof []byte) error {
	dir, err := os.MkdirTemp("", "scratchpad-signature-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "proof.sig")
	if err = os.WriteFile(path, []byte(signature), 0600); err != nil {
		return err
	}
	command := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowed, "-I", "synthetic-owner", "-n", "scratchpad-auth-v2", "-s", path)
	command.Stdin = strings.NewReader(string(proof))
	return command.Run()
}

func runIntegrationGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, output)
	}
}
