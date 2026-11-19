// Command finance is the household finance helper: a small web app that works out
// each person's monthly transfer to the shared expense account.
//
// Usage:
//
//	finance serve         run the web server
//	finance healthcheck   exit 0 if the local server's /healthz answers ok (for Docker)
//
// Configuration comes from the environment:
//
//	FINANCE_ADDR  listen address (default :8080)
//	FINANCE_DB    SQLite database path (default /data/finance.db)
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Embed the zoneinfo database so TZ=Europe/Oslo works in a distroless image.
	_ "time/tzdata"

	"github.com/HaakonSvane/homelab/finance/internal/store"
	"github.com/HaakonSvane/homelab/finance/internal/web"
)

const usage = `usage: finance <command>

commands:
  serve         run the web server
  healthcheck   check that the local server answers /healthz
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve()
	case "healthcheck":
		err = healthcheck()
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "finance:", err)
		os.Exit(1)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbPath := env("FINANCE_DB", "/data/finance.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	srv := &http.Server{
		Addr:              env("FINANCE_ADDR", ":8080"),
		Handler:           web.New(st),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", srv.Addr, "db", dbPath)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func healthcheck() error {
	_, port, err := net.SplitHostPort(env("FINANCE_ADDR", ":8080"))
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: %s: %s", resp.Status, body)
	}
	return nil
}
