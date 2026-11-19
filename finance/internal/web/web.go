// Package web holds the HTTP handlers and templates.
package web

import (
	"context"
	"net/http"
	"time"
)

// Pinger is the part of the store /healthz needs.
type Pinger interface {
	Ping(ctx context.Context) error
}

// New returns the application's root handler.
func New(db Pinger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err := db.Ping(ctx); err != nil {
			http.Error(w, "database: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok\n"))
	})
	return mux
}
