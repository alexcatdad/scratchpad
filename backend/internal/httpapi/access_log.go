package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

type loggedResponse struct {
	http.ResponseWriter
	status int
}

func (w *loggedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *loggedResponse) WriteHeader(status int) {
	if status >= 200 && w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *loggedResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

// RequestLog records only response metadata. Unwrap preserves deadline, flush
// and hijack controls used by imports and the retained dashboard reverse proxy.
func RequestLog(next http.Handler, output io.Writer) http.Handler {
	var lock sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		response := &loggedResponse{ResponseWriter: w}
		defer func() {
			failure := recover()
			status := response.status
			if status == 0 {
				status = http.StatusOK
				if failure != nil {
					status = http.StatusInternalServerError
				}
			}
			entry := map[string]any{"method": r.Method, "path": r.URL.Path, "status": status, "durationMs": time.Since(started).Milliseconds()}
			if failure != nil {
				entry["error"] = "HTTP_REQUEST_FAILED"
			}
			lock.Lock()
			_ = json.NewEncoder(output).Encode(entry)
			lock.Unlock()
			if failure != nil {
				// Preserve the server's connection-abort behavior without printing
				// arbitrary panic values or stacks containing private inputs.
				panic(http.ErrAbortHandler)
			}
		}()
		next.ServeHTTP(response, r)
	})
}
