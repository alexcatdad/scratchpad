package backend_test

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

var binary, mcpBinary, serviceBinary, integrationBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "scratchpad-go-tests-")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "scratchpad-server")
	build := exec.Command("go", "build", "-race", "-o", binary, "./cmd/scratchpad-server")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		os.RemoveAll(dir)
		os.Exit(1)
	}
	mcpBinary, serviceBinary, integrationBinary = filepath.Join(dir, "scratchpad-mcp"), filepath.Join(dir, "scratchpad-service"), filepath.Join(dir, "integration")
	for _, target := range []struct{ output, pkg string }{{mcpBinary, "./cmd/scratchpad-mcp"}, {serviceBinary, "./cmd/scratchpad-service"}, {integrationBinary, "./integration"}} {
		build := exec.Command("go", "build", "-race", "-o", target.output, target.pkg)
		build.Dir = "../mcp"
		build.Stdout, build.Stderr = os.Stdout, os.Stderr
		if err := build.Run(); err != nil {
			os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestFreshSQLiteMigrationIsRepeatable(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "memory.sqlite")
	for range 2 {
		cmd := exec.Command(binary, "migrate")
		cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL=", "SCRATCHPAD_DATABASE_PATH="+path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("migration failed: %v %s", err, out)
		}
		var report struct {
			Database      string `json:"database"`
			SchemaVersion int    `json:"schemaVersion"`
		}
		if err := json.Unmarshal(out, &report); err != nil {
			t.Fatal(err)
		}
		if report.Database != "sqlite" || report.SchemaVersion != 3 {
			t.Fatalf("unexpected report: %s", out)
		}
	}
}

func TestSchemaOneUpgrade(t *testing.T) {
	path := legacyFixture(t)
	cmd := migrationCommand(path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("upgrade failed: %v %s", err, out)
	}
	var report struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 3 {
		t.Fatalf("supported schema 1 was not upgraded: %s", out)
	}
}

func migrationCommand(path string) *exec.Cmd {
	cmd := exec.Command(binary, "migrate")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL=", "SCRATCHPAD_DATABASE_PATH="+path)
	return cmd
}

// A supported installation written in the existing TypeScript schema. Opening
// SQL here creates historical input; assertions use the operational CLI/HTTP.
func legacyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "memory.sqlite")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
 CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL);
 INSERT INTO schema_migrations VALUES(1,'2026-10-01T00:00:00.000Z');
 CREATE TABLE entities(kind TEXT NOT NULL,id TEXT NOT NULL,data TEXT NOT NULL,version INTEGER NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(kind,id));
 CREATE TABLE identities(identity TEXT PRIMARY KEY,project_id TEXT NOT NULL);
 CREATE TABLE retries(scope TEXT NOT NULL,key TEXT NOT NULL,request TEXT NOT NULL,response TEXT NOT NULL,PRIMARY KEY(scope,key));
 CREATE VIRTUAL TABLE record_search USING fts5(record_id UNINDEXED,title,content);`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUnsupportedSchemaIsRefusedWithoutChangingBackup(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations VALUES(999,'2026-10-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := migrationCommand(path).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
		t.Fatalf("newer schema accepted: %v %s", err, out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("refused database was modified")
	}
}

func TestMalformedSchemaIsNotRepaired(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE entities; CREATE TABLE entities(kind TEXT,id TEXT,data TEXT,version INTEGER,created_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, _ := os.ReadFile(path)
	out, err := migrationCommand(path).CombinedOutput()
	if err == nil {
		t.Fatalf("malformed installation accepted: %s", out)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("malformed installation was repaired")
	}
}

func startServer(t *testing.T, path, databaseURL string, listenAddress ...string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	if len(listenAddress) > 0 {
		address = listenAddress[0]
	}
	publicOrigin := "http://" + address
	if len(listenAddress) > 1 {
		publicOrigin = listenAddress[1]
	}
	cmd := exec.Command(binary, "serve")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_PATH="+path, "SCRATCHPAD_DATABASE_URL="+databaseURL, "SCRATCHPAD_LISTEN_ADDR="+address, "SCRATCHPAD_PUBLIC_URL="+publicOrigin)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(waited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-waited:
			if waitErr != nil && !strings.Contains(waitErr.Error(), "signal: terminated") {
				t.Errorf("server exited unsuccessfully: %v", waitErr)
			}
		// Allow the server's five-second graceful deadline plus race-runtime exit.
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-waited
			t.Error("server did not shut down")
		}
	})
	line := make(chan []byte, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			line <- scanner.Bytes()
		} else {
			line <- nil
		}
	}()
	select {
	case data := <-line:
		var report struct {
			Address string `json:"address"`
		}
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatalf("startup report unavailable: %s", data)
		}
		return "http://" + report.Address
	case <-time.After(10 * time.Second):
		t.Fatal("server startup timed out")
	}
	return ""
}

// startServerWithStop is used by the cross-process tray acceptance test to
// create a genuine backend outage and restart the server on the same origin.
func startServerWithStop(t *testing.T, path, databaseURL, address string, publicOrigins ...string) (string, func()) {
	t.Helper()
	if address == "" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address = listener.Addr().String()
		listener.Close()
	}
	publicOrigin := "http://" + address
	if len(publicOrigins) > 0 && publicOrigins[0] != "" {
		publicOrigin = publicOrigins[0]
	}
	cmd := exec.Command(binary, "serve")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_PATH="+path, "SCRATCHPAD_DATABASE_URL="+databaseURL, "SCRATCHPAD_LISTEN_ADDR="+address, "SCRATCHPAD_PUBLIC_URL="+publicOrigin)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(waited) }()
	var stopOnce sync.Once
	var stopErr error
	stop := func() {
		stopOnce.Do(func() {
			select {
			case <-waited:
				stopErr = waitErr
			default:
				_ = cmd.Process.Signal(syscall.SIGTERM)
				select {
				case <-waited:
					stopErr = waitErr
				case <-time.After(10 * time.Second):
					_ = cmd.Process.Kill()
					<-waited
					stopErr = waitErr
				}
			}
		})
		if stopErr != nil && !strings.Contains(stopErr.Error(), "signal: terminated") {
			t.Errorf("server exited unsuccessfully: %v", stopErr)
		}
	}
	t.Cleanup(stop)
	line := make(chan []byte, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			line <- append([]byte(nil), scanner.Bytes()...)
		} else {
			line <- nil
		}
	}()
	select {
	case data := <-line:
		var report struct {
			Address string `json:"address"`
		}
		if err := json.Unmarshal(data, &report); err != nil || report.Address == "" {
			t.Fatalf("startup report unavailable: %s; logs=%s", data, logs.String())
		}
		if report.Address != address {
			t.Fatalf("server listened on unexpected address %q, want %q", report.Address, address)
		}
		return publicOrigin, stop
	case <-time.After(10 * time.Second):
		stop()
		t.Fatalf("server startup timed out; logs=%s", logs.String())
	}
	return "", stop
}

type committedResponseDropProxy struct {
	server  *http.Server
	origin  string
	armDrop atomic.Bool
	dropped atomic.Int32
}

// newCommittedResponseDropProxy forwards real requests to the actual backend,
// but can discard one successful capture response after the backend commits it.
func newCommittedResponseDropProxy(t *testing.T, backendOrigin string) *committedResponseDropProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &committedResponseDropProxy{origin: "http://" + listener.Addr().String()}
	transport := &http.Transport{Proxy: nil}
	proxy.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		upstream := request.Clone(request.Context())
		upstream.URL.Scheme = "http"
		upstream.URL.Host = strings.TrimPrefix(backendOrigin, "http://")
		upstream.Host = upstream.URL.Host
		upstream.RequestURI = ""
		response, requestErr := transport.RoundTrip(upstream)
		if requestErr != nil {
			http.Error(w, "synthetic upstream unavailable", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		isCapture := request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/api/v1/records")
		if isCapture && response.StatusCode >= 200 && response.StatusCode < 300 && proxy.armDrop.CompareAndSwap(true, false) {
			// Drain the committed upstream response, then close the downstream
			// socket without an HTTP response to model a lost central ACK.
			_, _ = io.Copy(io.Discard, response.Body)
			proxy.dropped.Add(1)
			if hijacker, ok := w.(http.Hijacker); ok {
				connection, _, hijackErr := hijacker.Hijack()
				if hijackErr == nil {
					_ = connection.Close()
					return
				}
			}
			return
		}
		for name, values := range response.Header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	})}
	go func() { _ = proxy.server.Serve(listener) }()
	t.Cleanup(func() {
		_ = proxy.server.Close()
		transport.CloseIdleConnections()
	})
	return proxy
}

func (p *committedResponseDropProxy) dropNextCaptureResponse() { p.armDrop.Store(true) }

func TestGoServerReadiness(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	origin := startServer(t, filepath.Join(dir, "memory.sqlite"), "")
	response, err := http.Get(origin + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var report struct {
		Status   string `json:"status"`
		Database string `json:"database"`
	}
	if err := json.NewDecoder(response.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || report.Status != "ready" || report.Database != "sqlite" {
		t.Fatalf("unexpected readiness: %d %+v", response.StatusCode, report)
	}
}

func TestRealMCPDurableServiceToScratchpadSQLiteRestartAndReceipts(t *testing.T) {
	work := t.TempDir()
	if err := os.Chmod(work, 0700); err != nil {
		t.Fatal(err)
	}
	databasePath := legacyFixture(t)
	privateKey, _ := enrolledSSHKey(t, databasePath)
	publicKeyPath := privateKey + ".pub"
	fingerprintOutput, err := exec.Command("ssh-keygen", "-lf", publicKeyPath).Output()
	if err != nil {
		t.Fatal(err)
	}
	fingerprintFields := strings.Fields(string(fingerprintOutput))
	if len(fingerprintFields) < 2 || !strings.HasPrefix(fingerprintFields[1], "SHA256:") {
		t.Fatalf("synthetic key fingerprint unavailable: %q", fingerprintOutput)
	}
	fingerprint := fingerprintFields[1]

	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	backendAddress := backendListener.Addr().String()
	_ = backendListener.Close()
	proxy := newCommittedResponseDropProxy(t, "http://"+backendAddress)
	origin, stopBackend := startServerWithStop(t, databasePath, "", backendAddress, proxy.origin)
	if origin != proxy.origin {
		t.Fatalf("backend public origin %s did not match test proxy %s", origin, proxy.origin)
	}
	serviceSocket, stopService := startDurableService(t, work)
	defer stopService()
	configureService(t, origin, serviceSocket, publicKeyPath, privateKey, fingerprint, work)
	workspace := filepath.Join(work, "repo")
	createDisposableGitRepo(t, workspace, "git@github.com:synthetic-integration/full-flow.git")
	statePath := filepath.Join(work, "captures.json")
	runMCPPhase(t, origin, serviceSocket, workspace, privateKey, statePath, "capture", "")
	assertStoredTrayCaptures(t, databasePath, 8, 8)
	runMCPPhase(t, origin, serviceSocket, workspace, privateKey, statePath, "lost-response-capture", "")
	if count := countRecords(t, databasePath); count != 10 {
		t.Fatalf("lost-ack intake did not create exactly one central record: got %d", count)
	}
	runMCPPhase(t, origin, serviceSocket, workspace, privateKey, statePath, "lost-response-retry", "")
	if count := countRecords(t, databasePath); count != 10 {
		t.Fatalf("retry after discarded acknowledgement duplicated a central record: got %d", count)
	}
	proxy.dropNextCaptureResponse()
	runMCPPhase(t, origin, serviceSocket, workspace, privateKey, statePath, "central-response-drop", "")
	if dropped := proxy.dropped.Load(); dropped != 1 {
		t.Fatalf("expected one committed central capture response to be dropped, got %d", dropped)
	}
	centralSavedAt := storedClientSavedAtByTitle(t, databasePath, "Synthetic central response lost after commit")
	if centralSavedAt == "" || countRecords(t, databasePath) != 11 {
		t.Fatal("backend did not commit exactly one timestamped record before dropping its response")
	}
	runMCPPhase(t, origin, serviceSocket, workspace, privateKey, statePath, "central-response-retry", "")
	if after := storedClientSavedAtByTitle(t, databasePath, "Synthetic central response lost after commit"); after != centralSavedAt {
		t.Fatalf("central retry changed the original clientSavedAt: before=%q after=%q", centralSavedAt, after)
	}
	if count := countRecords(t, databasePath); count != 11 {
		t.Fatalf("central response retry duplicated the committed record: got %d records", count)
	}
	// A fully delivered item remains replayable after a process restart even
	// though its transient full server response is no longer cached in memory.
	stopService()
	serviceSocket, stopService = startDurableService(t, work)
	defer stopService()
	runMCPPhase(t, origin, serviceSocket, workspace, privateKey, statePath, "central-response-retry", "")
	if count := countRecords(t, databasePath); count != 11 {
		t.Fatalf("same-request replay after delivered-service restart duplicated a central record: got %d", count)
	}
	secondWorkspace := filepath.Join(work, "repo-second")
	createDisposableGitRepo(t, secondWorkspace, "git@github.com:synthetic-integration/second-scope.git")
	runMCPPhase(t, origin, serviceSocket, workspace, privateKey, statePath, "scope", "-second-workspace", secondWorkspace)

	// Capture while the actual backend process is down. The result must be a
	// local pending receipt, then survive stopping and restarting the service.
	stopBackend()
	runMCPPhase(t, origin, serviceSocket, workspace, privateKey, statePath, "outage-capture", "")
	stopService()
	_, stopBackend = startServerWithStop(t, databasePath, "", backendAddress, proxy.origin)
	defer stopBackend()
	serviceSocket, stopService = startDurableService(t, work)
	defer stopService()
	runMCPPhase(t, origin, serviceSocket, workspace, privateKey, statePath, "receipt", "")
	assertStoredTrayCaptures(t, databasePath, 8, 8)
	if count := countRecords(t, databasePath); count != 14 { // restored record + eight types + lost-ack + committed-response retry + two scopes + outage capture
		t.Fatalf("expected 14 centrally persisted records after receipt replay, got %d", count)
	}

	// A live central server must not become a direct-capture fallback when the
	// local intake process is stopped.
	stopService()
	before := countRecords(t, databasePath)
	runMCPPhase(t, origin, serviceSocket, workspace, privateKey, statePath, "service-unavailable", "")
	if after := countRecords(t, databasePath); after != before {
		t.Fatalf("capture bypassed unavailable local service: record count %d -> %d", before, after)
	}
}

func startDurableService(t *testing.T, base string) (string, func()) {
	return startDurableServiceWithEnv(t, base)
}

func startDurableServiceWithEnv(t *testing.T, base string, extraEnv ...string) (string, func()) {
	t.Helper()
	socketRoot := "/private/tmp"
	if _, err := os.Stat(socketRoot); err != nil {
		socketRoot = os.TempDir()
	}
	privateDir, err := os.MkdirTemp(socketRoot, "spq-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(privateDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(privateDir) })
	socket := filepath.Join(privateDir, "capture.sock")
	keyPath := filepath.Join(base, "synthetic-queue-key")
	if _, err := os.Stat(keyPath); errors.Is(err, os.ErrNotExist) {
		queueKey := make([]byte, 32)
		if _, err := rand.Read(queueKey); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keyPath, queueKey, 0600); err != nil {
			t.Fatal(err)
		}
		clear(queueKey)
	} else if err != nil {
		t.Fatal(err)
	}
	helperPath := filepath.Join(privateDir, "synthetic-key-helper")
	helper := "#!/bin/sh\nexec /bin/cat \"$SCRATCHPAD_TEST_QUEUE_KEY\"\n"
	if err := os.WriteFile(helperPath, []byte(helper), 0700); err != nil {
		t.Fatal(err)
	}
	checkHelper := exec.Command(helperPath, "get", "synthetic-test")
	checkHelper.Env = append(os.Environ(), "SCRATCHPAD_TEST_QUEUE_KEY="+keyPath)
	if output, err := checkHelper.Output(); err != nil || len(output) != 32 {
		t.Fatalf("synthetic helper contract failed (output length %d): %v", len(output), err)
	}
	command := exec.Command(serviceBinary, "--database", filepath.Join(base, "queue.sqlite"), "--socket", socket, "--key-helper", helperPath, "--key-identifier", "synthetic-test")
	command.Env = append(os.Environ(), extraEnv...)
	command.Env = append(command.Env, "SCRATCHPAD_TEST_QUEUE_KEY="+keyPath)
	var serviceLogs bytes.Buffer
	command.Stdout, command.Stderr = &serviceLogs, &serviceLogs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	var waitErr error
	go func() { waitErr = command.Wait(); close(waited) }()
	var stopOnce sync.Once
	var stopErr error
	stop := func() {
		stopOnce.Do(func() {
			select {
			case <-waited:
				stopErr = waitErr
			default:
				_ = command.Process.Signal(syscall.SIGTERM)
				select {
				case <-waited:
					stopErr = waitErr
				case <-time.After(10 * time.Second):
					_ = command.Process.Kill()
					<-waited
					stopErr = waitErr
				}
			}
		})
		if stopErr != nil && !strings.Contains(stopErr.Error(), "signal: terminated") {
			t.Errorf("durable service exited unsuccessfully: %v", stopErr)
		}
	}
	t.Cleanup(stop)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(socket); err == nil && info.Mode()&os.ModeSocket != 0 {
			return socket, stop
		}
		select {
		case <-waited:
			_, databaseErr := os.Stat(filepath.Join(base, "queue.sqlite"))
			_, socketErr := os.Stat(socket)
			t.Fatalf("durable service exited during startup: exit=%v queueDBCreated=%t socketRemaining=%t output=%s", waitErr, databaseErr == nil, socketErr == nil, serviceLogs.String())
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	t.Fatal("durable service did not create its private socket")
	return "", stop
}

func configureService(t *testing.T, origin, socket, publicKey, signingKey, fingerprint, workspace string) {
	t.Helper()
	command := exec.Command(integrationBinary, "-url", origin, "-public-key", publicKey, "-signing-key", signingKey, "-workspace", workspace, "-binary", mcpBinary, "-service-socket", socket, "-fingerprint", fingerprint, "-phase", "configure-service")
	out, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "PASS configure-service") {
		t.Fatalf("service configuration against actual backend failed: %v %s", err, out)
	}
}

func startConfiguredDurableService(t *testing.T, origin, base, workspace, privateKey string, serviceEnv ...string) (string, func()) {
	t.Helper()
	fingerprintOutput, err := exec.Command("ssh-keygen", "-lf", privateKey+".pub").Output()
	if err != nil {
		t.Fatal("synthetic enrolled SSH fingerprint unavailable")
	}
	fields := strings.Fields(string(fingerprintOutput))
	if len(fields) < 2 || !strings.HasPrefix(fields[1], "SHA256:") {
		t.Fatal("synthetic enrolled SSH fingerprint malformed")
	}
	socket, stop := startDurableServiceWithEnv(t, base, serviceEnv...)
	configureService(t, origin, socket, privateKey+".pub", privateKey, fields[1], workspace)
	return socket, stop
}

func createDisposableGitRepo(t *testing.T, path, remote string) {
	t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"remote", "add", "origin", remote}} {
		command := exec.Command("git", args...)
		command.Dir = path
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("create synthetic Git fixture: %v %s", err, out)
		}
	}
}

func runMCPPhase(t *testing.T, origin, socket, workspace, signingKey, statePath, phase string, extraArgs ...string) {
	t.Helper()
	publicKey := signingKey + ".pub"
	args := []string{"-url", origin, "-public-key", publicKey, "-signing-key", signingKey, "-workspace", workspace, "-binary", mcpBinary, "-service-socket", socket, "-state", statePath, "-phase", phase}
	args = append(args, extraArgs...)
	command := exec.Command(integrationBinary, args...)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("actual service/MCP/backend phase %s failed: %v %s", phase, err, out)
	}
	want := "PASS " + phase
	if !strings.Contains(string(out), want) {
		t.Fatalf("actual service/MCP/backend phase %s did not complete: %s", phase, out)
	}
}

func assertStoredTrayCaptures(t *testing.T, path string, expectedCaptures, expectedTypes int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT data FROM entities WHERE kind='record'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types := map[string]bool{}
	captures := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var record struct {
			Title         string `json:"title"`
			Type          string `json:"type"`
			ClientSavedAt string `json:"clientSavedAt"`
		}
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(record.Title, "Integration ") {
			captures++
			if record.ClientSavedAt == "" {
				t.Fatalf("central record %q omitted original clientSavedAt", record.Title)
			}
			types[record.Type] = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if captures != expectedCaptures || len(types) != expectedTypes {
		t.Fatalf("backend SQLite stored %d tray captures across %d types, expected %d captures / %d types: %v", captures, len(types), expectedCaptures, expectedTypes, types)
	}
}

func storedClientSavedAtByTitle(t *testing.T, path, title string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT data FROM entities WHERE kind='record'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var savedAt string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var record struct {
			Title         string `json:"title"`
			ClientSavedAt string `json:"clientSavedAt"`
		}
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			t.Fatal(err)
		}
		if record.Title == title {
			if savedAt != "" {
				t.Fatalf("backend contains duplicate records titled %q", title)
			}
			savedAt = record.ClientSavedAt
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return savedAt
}

func countRecords(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM entities WHERE kind='record'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

const restoredRecord = `{"id":"rec_restored","createdAt":"2026-10-01T00:00:00.000Z","version":1,"projectId":"proj_restored","type":"finding","title":"Preserve original evidence","authority":"observed","confidence":"high","payload":{"finding":"A synthetic restored capture.","custom":[1,true,"evidence"]},"sourceContext":{"branch":"main","commit":"abc123"}}`
const restoredToken = "synthetic_existing_agent_token"

func seedRestoredHistory(t *testing.T, db *sql.DB) {
	t.Helper()
	digest := sha256.Sum256([]byte(restoredToken))
	inputs := []struct{ kind, id, data string }{
		{"record", "rec_restored", restoredRecord},
		{"credential", "credential_restored", `{"id":"credential_restored","kind":"ssh","source":"local","createdAt":"2026-10-01T00:00:00Z","version":1}`},
		{"session", hex.EncodeToString(digest[:]), `{"credentialId":"credential_restored","expiresAt":"2099-01-01T00:00:00Z","browser":false}`},
		{"metadata", "rec_restored", `{"id":"rec_restored","tags":["migration"],"archived":false,"version":1}`},
		{"revision", "revision_restored", `{"id":"revision_restored","recordId":"rec_restored","changes":{"tags":["migration"]}}`},
		{"evidence", "evidence_restored", `{"id":"evidence_restored","recordId":"rec_restored","url":"https://example.com/evidence"}`},
		{"audit", "audit_restored", `{"id":"audit_restored","entityId":"rec_restored","action":"record.created"}`},
		// Schema migration must work independently of legacy provider-secret conversion.
		{"settings", "global", `{"apiKey":"synthetic_legacy_secret"}`},
	}
	for _, input := range inputs {
		if _, err := db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES($1,$2,$3,1,'2026-10-01T00:00:00Z')`, input.kind, input.id, input.data); err != nil {
			t.Fatal(err)
		}
	}
}
func fetchRecord(t *testing.T, origin, token string) (int, map[string]any) {
	t.Helper()
	request, err := http.NewRequest("GET", origin+"/api/v1/records/rec_restored", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var value map[string]any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatalf("invalid JSON response (status %d)", response.StatusCode)
	}
	return response.StatusCode, value
}
func TestRestoredSQLiteRecordSurvivesUpgradeAndRestart(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	seedRestoredHistory(t, db)
	db.Close()
	for range 2 {
		t.Run("startup", func(t *testing.T) {
			origin := startServer(t, path, "")
			status, value := fetchRecord(t, origin, restoredToken)
			if status != 200 {
				t.Fatalf("restored session could not read history: %d %+v", status, value)
			}
			var expected map[string]any
			json.Unmarshal([]byte(restoredRecord), &expected)
			if !reflect.DeepEqual(value["record"], expected) {
				t.Fatal("immutable record changed")
			}
			if len(value["revisions"].([]any)) != 1 || len(value["evidence"].([]any)) != 1 || len(value["audit"].([]any)) != 1 {
				t.Fatal("related history was lost")
			}
		})
	}
}

func TestRecordRequiresAuthentication(t *testing.T) {
	path := legacyFixture(t)
	origin := startServer(t, path, "")
	status, value := fetchRecord(t, origin, "")
	if status != 401 || value["error"].(map[string]any)["code"] != "AUTH_REQUIRED" {
		t.Fatalf("wrong authentication failure: %d %+v", status, value)
	}
}

func TestCompetingSQLiteStartups(t *testing.T) {
	path := legacyFixture(t)
	commands := []*exec.Cmd{migrationCommand(path), migrationCommand(path), migrationCommand(path)}
	outputs := make([]bytes.Buffer, len(commands))
	for i, cmd := range commands {
		cmd.Stdout = &outputs[i]
		cmd.Stderr = &outputs[i]
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Errorf("competing startup failed: %v %s", err, outputs[i].Bytes())
		}
	}
	out, err := migrationCommand(path).CombinedOutput()
	if err != nil {
		t.Fatalf("repeat after competing startups failed: %v %s", err, out)
	}
}

func TestFailedSQLiteDDLCanBeRetried(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE entities_created(id TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if out, err := migrationCommand(path).CombinedOutput(); err == nil {
		t.Fatalf("conflicting migration unexpectedly succeeded: %s", out)
	}
	// Operator removes the known synthetic conflict; the migration must have
	// rolled back both its DDL and marker, so an ordinary retry can succeed.
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE entities_created`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if out, err := migrationCommand(path).CombinedOutput(); err != nil {
		t.Fatalf("retry after failed migration failed: %v %s", err, out)
	}
}

func TestSQLiteRejectsUnsafeFiles(t *testing.T) {
	for _, variant := range []string{"permissions", "symlink", "hardlink", "sidecar"} {
		t.Run(variant, func(t *testing.T) {
			path := legacyFixture(t)
			switch variant {
			case "permissions":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				link := filepath.Join(filepath.Dir(path), "linked.sqlite")
				if err := os.Symlink(path, link); err != nil {
					t.Fatal(err)
				}
				path = link
			case "hardlink":
				if err := os.Link(path, filepath.Join(filepath.Dir(path), "linked.sqlite")); err != nil {
					t.Fatal(err)
				}
			case "sidecar":
				if err := os.WriteFile(path+"-wal", []byte("synthetic unsafe sidecar"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			out, err := migrationCommand(path).CombinedOutput()
			if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
				t.Fatalf("unsafe storage accepted: %v %s", err, out)
			}
		})
	}
}

func TestRestoredAuthenticationIsRechecked(t *testing.T) {
	inputs := []struct{ name, statement string }{
		{"revoked credential", `UPDATE entities SET data=json_set(data,'$.revokedAt','2026-10-01T00:00:00Z') WHERE kind='credential'`},
		{"revoked session", `UPDATE entities SET data=json_set(data,'$.revokedAt','2026-10-01T00:00:00Z') WHERE kind='session'`},
		{"expired session", `UPDATE entities SET data=json_set(data,'$.expiresAt','2000-01-01T00:00:00Z') WHERE kind='session'`},
		{"browser session on bearer", `UPDATE entities SET data=json_set(data,'$.browser',json('true')) WHERE kind='session'`},
		{"unlinked GitHub credential", `UPDATE entities SET data=json_set(data,'$.source','github') WHERE kind='credential'`},
	}
	for _, input := range inputs {
		t.Run(input.name, func(t *testing.T) {
			path := legacyFixture(t)
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			seedRestoredHistory(t, db)
			if _, err := db.Exec(input.statement); err != nil {
				t.Fatal(err)
			}
			db.Close()
			origin := startServer(t, path, "")
			status, value := fetchRecord(t, origin, restoredToken)
			if status != 401 || value["error"].(map[string]any)["code"] != "AUTH_INVALID" {
				t.Fatalf("invalid restored credential accepted: %d %+v", status, value)
			}
		})
	}
}

func TestSQLiteBackupRestorePreservesHistory(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	seedRestoredHistory(t, db)
	db.Close()
	// This fixture is closed, so a single-file copy is an independent schema-1
	// backup. Live installations must use the online backup command instead.
	snapshot, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := migrationCommand(path).CombinedOutput(); err != nil {
		t.Fatalf("upgrade failed: %v %s", err, out)
	}
	restored := filepath.Join(filepath.Dir(path), "restored.sqlite")
	if err := os.WriteFile(restored, snapshot, 0600); err != nil {
		t.Fatal(err)
	}
	origin := startServer(t, restored, "")
	status, value := fetchRecord(t, origin, restoredToken)
	var expected map[string]any
	json.Unmarshal([]byte(restoredRecord), &expected)
	if status != 200 || !reflect.DeepEqual(value["record"], expected) {
		t.Fatal("restored backup did not preserve authenticated history")
	}
}

func TestInterruptedSQLiteStartupCanBeRetried(t *testing.T) {
	path := legacyFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	cmd := migrationCommand(path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	cmd.Process.Kill()
	if err := cmd.Wait(); err == nil {
		t.Fatal("startup did not wait for the migration lock")
	}
	if _, err := db.Exec(`ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if out, err := migrationCommand(path).CombinedOutput(); err != nil {
		t.Fatalf("restart after interruption failed: %v %s", err, out)
	}
}

func TestStartupErrorsDoNotDiscloseDatabaseCredentials(t *testing.T) {
	cmd := exec.Command(binary, "migrate")
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_URL=postgres://synthetic_owner:synthetic_secret_must_not_leak@127.0.0.1:1/synthetic_database")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") || strings.Contains(string(out), "synthetic_secret_must_not_leak") || strings.Contains(string(out), "postgres://") {
		t.Fatalf("unsafe startup failure: %v %s", err, out)
	}
}

func TestContextHeaderMigrationConflictRetriesAndPreservesHistory(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := legacyFixture(t), ""
			driver, dsn := "sqlite", path
			if engine == "postgres" {
				path = ""
				databaseURL = legacyPostgres(t)
				driver, dsn = "pgx", databaseURL
			}
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			seedRestoredHistory(t, db)
			if _, err = db.Exec(`INSERT INTO entities(kind,id,data,version,created_at) VALUES('project','proj_restored','{"id":"proj_restored","name":"Historical context","settings":{"aiProcessing":false,"crossProjectAnalysis":false,"enabledRecordTypes":["finding"],"repoMirroring":{"enabled":false,"recordTypes":["finding"]}}}',1,'2026-10-01T00:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			// Historical schema 2 input, with an operator-created naming conflict.
			if _, err = db.Exec(`CREATE INDEX entities_created ON entities(kind,created_at,id); INSERT INTO schema_migrations VALUES(2,'2026-10-02T00:00:00Z'); CREATE TABLE context_headers(id TEXT)`); err != nil {
				t.Fatal(err)
			}
			command := func() *exec.Cmd {
				if engine == "postgres" {
					return postgresCommand(databaseURL)
				}
				return migrationCommand(path)
			}
			if out, err := command().CombinedOutput(); err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
				t.Fatalf("conflicting projection migration accepted: %v %s", err, out)
			}
			// Removing only the synthetic conflict must permit a normal atomic retry.
			if _, err = db.Exec(`DROP TABLE context_headers`); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				out, err := command().CombinedOutput()
				if err != nil {
					t.Fatalf("projection migration retry failed: %v %s", err, out)
				}
				var report map[string]any
				if json.Unmarshal(out, &report) != nil || report["schemaVersion"] != float64(3) {
					t.Fatalf("schema 2 did not advance sequentially to 3: %s", out)
				}
			}
			for range 2 {
				t.Run("restart", func(t *testing.T) {
					origin := startServer(t, path, databaseURL)
					status, detail := fetchRecord(t, origin, restoredToken)
					var original map[string]any
					json.Unmarshal([]byte(restoredRecord), &original)
					if status != 200 || !reflect.DeepEqual(detail["record"], original) {
						t.Fatal("projection upgrade changed original authenticated history")
					}
					status, response := curationRequest(t, origin, "GET", "/projects/proj_restored/context", nil)
					if status != 200 || response["counts"].(map[string]any)["total"] != float64(1) {
						t.Fatalf("upgraded historical record missing from context: status=%d counts=%v", status, response["counts"])
					}
				})
			}
			trigger := "context_headers_insert"
			if engine == "postgres" {
				trigger = "context_headers_write ON entities"
			}
			if _, err = db.Exec("DROP TRIGGER " + trigger); err != nil {
				t.Fatal(err)
			}
			if out, err := command().CombinedOutput(); err == nil || !strings.Contains(string(out), "DATABASE_NOT_READY") {
				t.Fatalf("missing projection maintenance was silently repaired: %v %s", err, out)
			}
		})
	}
}
