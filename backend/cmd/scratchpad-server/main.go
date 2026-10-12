package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alexcatdad/scratchpad/backend/internal/httpapi"
	"github.com/alexcatdad/scratchpad/backend/internal/persistence"
	"github.com/alexcatdad/scratchpad/backend/internal/secrets"
)

func run() error {
	if len(os.Args) == 3 && os.Args[1] == "backup" {
		stop, cleanup := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cleanup()
		ctx, cancel := context.WithTimeout(stop, 60*time.Second)
		defer cancel()
		version, err := persistence.Backup(ctx, os.Getenv("SCRATCHPAD_DATABASE_PATH"), os.Getenv("SCRATCHPAD_DATABASE_URL"), os.Args[2])
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"database": "sqlite", "schemaVersion": version, "destination": os.Args[2]})
	}
	if len(os.Args) != 2 || (os.Args[1] != "migrate" && os.Args[1] != "serve" && os.Args[1] != "setup" && os.Args[1] != "recover") {
		return errors.New("Usage: scratchpad-server migrate|serve|setup|recover|backup <absolute-destination>")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	store, err := persistence.Open(ctx, os.Getenv("SCRATCHPAD_DATABASE_PATH"), os.Getenv("SCRATCHPAD_DATABASE_URL"))
	cancel()
	if err != nil {
		return errors.New("DATABASE_NOT_READY: persistence is unavailable, unsafe, or its schema is unsupported.")
	}
	defer store.Close()
	if os.Args[1] == "setup" || os.Args[1] == "recover" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		token, err := httpapi.AuthorizeSetup(ctx, store, os.Args[1] == "recover")
		if err != nil {
			return fmt.Errorf("%s: owner authorization failed", err)
		}
		fmt.Fprintln(os.Stdout, "One-use owner setup or recovery token (expires in 15 minutes):")
		fmt.Fprintln(os.Stdout, token)
		return nil
	}
	if os.Args[1] == "migrate" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"database": store.Engine, "schemaVersion": store.Version})
	}
	address := os.Getenv("SCRATCHPAD_LISTEN_ADDR")
	if address == "" {
		address = "127.0.0.1:3000"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return errors.New("LISTEN_FAILED: could not bind application listener.")
	}
	defer listener.Close()
	publicURL := os.Getenv("SCRATCHPAD_PUBLIC_URL")
	if publicURL == "" {
		publicURL = "http://localhost:3000"
	}
	public, err := url.Parse(publicURL)
	if err != nil {
		return errors.New("CONFIG_INVALID: public URL must be an HTTPS origin or a loopback HTTP origin.")
	}
	public.Scheme = strings.ToLower(public.Scheme)
	hostname := strings.ToLower(public.Hostname())
	if public.User != nil || public.RawQuery != "" || public.Fragment != "" || (public.Path != "" && public.Path != "/") || public.Host == "" || (public.Scheme != "https" && (public.Scheme != "http" || (hostname != "localhost" && hostname != "127.0.0.1" && hostname != "::1"))) {
		return errors.New("CONFIG_INVALID: public URL must be an HTTPS origin or a loopback HTTP origin.")
	}
	port := public.Port()
	if (public.Scheme == "https" && port == "443") || (public.Scheme == "http" && port == "80") {
		port = ""
	}
	public.Host = hostname
	if port != "" {
		public.Host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		public.Host = "[" + hostname + "]"
	}
	auth := httpapi.NewSSH(store, public.Scheme+"://"+public.Host)
	passkeys, err := httpapi.NewPasskeys(auth, hostname)
	if err != nil && net.ParseIP(hostname) == nil {
		return errors.New("CONFIG_INVALID: passkey relying party configuration is invalid.")
	}
	github, err := httpapi.NewGithub(auth, passkeys, os.Getenv("SCRATCHPAD_GITHUB_CLIENT_ID"), os.Getenv("SCRATCHPAD_GITHUB_CLIENT_SECRET"))
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	ragSetting := os.Getenv("SCRATCHPAD_RAG_ENABLED")
	if ragSetting != "" && ragSetting != "true" && ragSetting != "false" {
		return errors.New("CONFIG_INVALID: SCRATCHPAD_RAG_ENABLED must be true or false.")
	}
	mux.HandleFunc("GET /api/v1/rag/status", auth.RAGStatus(ragSetting == "true"))
	ai := httpapi.NewAI(auth, secrets.Load(os.Getenv("SCRATCHPAD_ENCRYPTION_KEY_FILE")), ragSetting == "true")
	if ragSetting == "true" {
		ragContext, ragCancel := context.WithTimeout(context.Background(), 10*time.Second)
		ragErr := store.InitRAG(ragContext)
		ragCancel()
		if ragErr != nil && !errors.Is(ragErr, persistence.ErrRAGUnavailable) {
			return errors.New("DATABASE_NOT_READY: RAG projection schema is unavailable or unsupported.")
		}
	}
	mux.HandleFunc("POST /api/v1/rag/index", ai.RAGIndex())
	mux.HandleFunc("POST /api/v1/rag/retrieve", ai.RAGRetrieve())
	conversionContext, conversionCancel := context.WithTimeout(context.Background(), 10*time.Second)
	conversionPending := ai.ConvertLegacy(conversionContext) != nil
	conversionCancel()
	mux.HandleFunc("GET /api/v1/ai/settings", ai.Settings(false))
	mux.HandleFunc("PATCH /api/v1/ai/settings", ai.Settings(true))
	mux.HandleFunc("POST /api/v1/ai/test", ai.TestProvider())
	mux.HandleFunc("GET /api/v1/settings", ai.GlobalSettings(false))
	mux.HandleFunc("PATCH /api/v1/settings", ai.GlobalSettings(true))
	mux.HandleFunc("GET /api/v1/ai/jobs", ai.Jobs("list"))
	mux.HandleFunc("POST /api/v1/ai/jobs", ai.Jobs("enqueue"))
	mux.HandleFunc("POST /api/v1/summaries/export", ai.Jobs("document"))
	mux.HandleFunc("POST /api/v1/ai/jobs/{id}/retry", ai.Jobs("retry"))
	mux.HandleFunc("POST /api/v1/search/semantic", ai.SemanticSearch())
	mux.HandleFunc("GET /api/v1/suggestions", ai.Suggestions())
	mux.HandleFunc("POST /api/v1/suggestions/{id}/accept", ai.ReviewSuggestion(true))
	mux.HandleFunc("POST /api/v1/suggestions/{id}/reject", ai.ReviewSuggestion(false))
	mux.HandleFunc("POST /api/v1/export", auth.Export())
	mux.HandleFunc("POST /api/v1/import", auth.Import())
	mux.HandleFunc("GET /api/v1/profile", auth.Profile("get"))
	mux.HandleFunc("PATCH /api/v1/profile", auth.Profile("name"))
	mux.HandleFunc("POST /api/v1/profile/github", auth.Profile("github"))
	mux.HandleFunc("DELETE /api/v1/profile/github", auth.Profile("unlink"))
	mux.HandleFunc("POST /api/v1/records", auth.Capture())
	mux.HandleFunc("PATCH /api/v1/records/{id}/metadata", auth.Curation("metadata"))
	mux.HandleFunc("POST /api/v1/records/{id}/revisions", auth.Curation("revisions"))
	mux.HandleFunc("POST /api/v1/records/{id}/evidence", auth.Curation("evidence"))
	mux.HandleFunc("POST /api/v1/records/{id}/mirror", auth.Curation("mirror"))
	mux.HandleFunc("POST /api/v1/relationships", auth.Curation("relationship-create"))
	mux.HandleFunc("POST /api/v1/relationships/{id}/accept", auth.Curation("relationship-accept"))
	mux.HandleFunc("POST /api/v1/relationships/{id}/reject", auth.Curation("relationship-reject"))
	mux.HandleFunc("GET /api/v1/records", auth.Search(false))
	mux.HandleFunc("POST /api/v1/search", auth.Search(true))
	mux.HandleFunc("GET /api/v1/projects", auth.Projects("list"))
	mux.HandleFunc("POST /api/v1/projects/resolve-explicit", auth.Projects("explicit"))
	mux.HandleFunc("POST /api/v1/projects/resolve", auth.Projects("remote"))
	mux.HandleFunc("GET /api/v1/projects/{id}", auth.Projects("detail"))
	mux.HandleFunc("PATCH /api/v1/projects/{id}", auth.Projects("update"))
	mux.HandleFunc("GET /api/v1/projects/{id}/settings", auth.Projects("settings"))
	mux.HandleFunc("GET /api/v1/projects/{id}/context", auth.ProjectContext())
	mux.HandleFunc("GET /api/v1/projects/{id}/context/compact", auth.CompactProjectContext())
	mux.HandleFunc("PATCH /api/v1/projects/{id}/settings", auth.Projects("settings-update"))
	mux.HandleFunc("POST /api/v1/auth/github/options", github.Options())
	mux.HandleFunc("GET /api/v1/auth/github/callback", github.Callback())
	mux.HandleFunc("GET /api/v1/auth/github", github.Status())
	mux.HandleFunc("POST /api/v1/auth/github/sync", github.ManualSync())
	mux.HandleFunc("POST /api/v1/auth/github/block", github.Management(false))
	mux.HandleFunc("DELETE /api/v1/auth/github", github.Management(true))
	mux.HandleFunc("GET /api/v1/auth/credentials", passkeys.Credentials())
	mux.HandleFunc("POST /api/v1/auth/credentials/challenge", auth.Enrollment(false))
	mux.HandleFunc("POST /api/v1/auth/credentials/verify", auth.Enrollment(true))
	mux.HandleFunc("DELETE /api/v1/auth/credentials/{id}", auth.Revoke(github.Configured()))
	mux.HandleFunc("POST /api/v1/auth/logout", passkeys.Logout())
	for _, operation := range []string{"register/options", "register/verify", "login/options", "login/verify"} {
		mux.HandleFunc("POST /api/v1/auth/"+operation, passkeys.Handler(operation))
	}
	mux.HandleFunc("GET /api/v1/auth/status", httpapi.AuthStatus(store, github.Configured()))
	mux.HandleFunc("POST /api/v1/auth/mcp/challenge", auth.Handler(false))
	mux.HandleFunc("POST /api/v1/auth/mcp/verify", auth.Handler(true))
	mux.HandleFunc("GET /api/v1/records/{id}", httpapi.Records(store))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := store.Ready(ctx); err != nil {
			writeJSON(w, 503, map[string]any{"error": map[string]string{"code": "DATABASE_NOT_READY", "message": "Persistence is unavailable or unsupported."}})
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ready", "database": store.Engine})
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024}
	stop, cleanup := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cleanup()
	workerContext, cancelWorker := context.WithCancel(stop)
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); github.Run(workerContext) }()
	defer func() { cancelWorker(); <-workerDone }()
	aiWorkerDone := make(chan struct{})
	go func() { defer close(aiWorkerDone); ai.Run(workerContext) }()
	defer func() { cancelWorker(); <-aiWorkerDone }()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"address": listener.Addr().String(), "database": store.Engine, "schemaVersion": store.Version, "secretConversionFailed": conversionPending}); err != nil {
		server.Close()
		<-done
		return errors.New("STARTUP_FAILED")
	}
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("SERVER_FAILED")
		}
		return nil
	case <-stop.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			server.Close()
		}
		<-done
		return nil
	}
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
