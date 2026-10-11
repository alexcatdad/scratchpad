package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexcatdad/scratchpad/backend/internal/httpapi"
	"github.com/alexcatdad/scratchpad/backend/internal/persistence"
)

func run() error {
	if len(os.Args) != 2 || (os.Args[1] != "migrate" && os.Args[1] != "serve") {
		return errors.New("Usage: scratchpad-server migrate|serve")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	store, err := persistence.Open(ctx, os.Getenv("SCRATCHPAD_DATABASE_PATH"), os.Getenv("SCRATCHPAD_DATABASE_URL"))
	cancel()
	if err != nil {
		return errors.New("DATABASE_NOT_READY: persistence is unavailable, unsafe, or its schema is unsupported.")
	}
	defer store.Close()
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
	mux := http.NewServeMux()
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
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"address": listener.Addr().String(), "database": store.Engine, "schemaVersion": store.Version}); err != nil {
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
