// Package api wires HTTP routes to the application's dependencies.
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/pipeprobe/pipeprobe/internal/store"
)

// NewRouter builds the HTTP hadler. Uses GO 1.22 method-aware routing
// ("GET /path") so wrong methods get 405 automatically.
func NewRouter(st *store.Store) http.Handler {
	mux := http.NewServeMux()

	// Liveness: the process is up and serving. Does NOT touch the DB, so it
	// stays green even during a transient DB blip (you don't want the
	// orchestrator to kill a healthy process just because the DB hiccuped).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, `{"status":"ok"}`)
	})

	// Readiness: can we actually serve traffic right now? This re-checks the
	// DB on every call, so the orchestrator stops routing to us if it drops.
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := st.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, `{"status":"db_unavailable"}`)
			return
		}
		writeJSON(w, http.StatusOK, `{"staus":"ready"}`)
	})

	return mux
}

func writeJSON(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}
