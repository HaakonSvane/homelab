// Command finance is the household finance helper: a small web app that works out
// each person's monthly transfer to the shared expense account.
//
// Usage:
//
//	finance serve                           run the web server
//	finance add-user <name> <display name>  create one of the two users
//	finance rename-user <name> <display name>  change the name shown in the app
//	finance enroll <name>                   print a one-time link to register a passkey
//	finance reset-passkeys <name>           remove a user's passkeys and sessions (recovery)
//	finance backup                          write a backup now (also runs nightly in serve)
//	finance seed <db path> [year]           fill an EMPTY database with fictional dev data
//	finance healthcheck                     exit 0 if the local /healthz answers ok (for Docker)
//
// Configuration comes from the environment:
//
//	FINANCE_ADDR    listen address (default :8080)
//	FINANCE_DB      SQLite database path (default /data/finance.db)
//	FINANCE_BACKUP_DIR  where backups go (default /data/backups); 30 are kept
//	FINANCE_ORIGIN  public origin, e.g. https://finans.home.arpa (serve, enroll).
//	                Its host is the passkey relying party ID: never change it.
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
	"strconv"
	"strings"
	"syscall"
	"time"

	// Embed the zoneinfo database so TZ=Europe/Oslo works in a distroless image.
	_ "time/tzdata"

	"github.com/HaakonSvane/homelab/finance/internal/auth"
	"github.com/HaakonSvane/homelab/finance/internal/month"
	"github.com/HaakonSvane/homelab/finance/internal/store"
	"github.com/HaakonSvane/homelab/finance/internal/web"
)

const usage = `usage: finance <command> [arguments]

commands:
  serve                           run the web server
  add-user <name> <display name>  create a user (name: lowercase login handle, e.g. kari)
  rename-user <name> <display name>  change the name shown in the app
  enroll <name>                   print a one-time link (30 min) to register a passkey
  reset-passkeys <name>           remove a user's passkeys and sessions (account recovery)
  backup                          write a backup now (serve also does this nightly at 03:30)
  seed <db path> [year]           fill an empty database with fictional dev data
  healthcheck                     check that the local server answers /healthz
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
	case "add-user":
		err = addUser(os.Args[2:])
	case "rename-user":
		err = renameUser(os.Args[2:])
	case "enroll":
		err = enroll(os.Args[2:])
	case "reset-passkeys":
		err = resetPasskeys(os.Args[2:])
	case "backup":
		err = backupNow()
	case "seed":
		err = seed(os.Args[2:])
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

	st, err := store.Open(ctx, dbPath())
	if err != nil {
		return err
	}
	defer st.Close()
	a, err := auth.New(st, os.Getenv("FINANCE_ORIGIN"))
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              env("FINANCE_ADDR", ":8080"),
		Handler:           web.New(st, a),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go nightlyBackups(ctx, st, backupDir())

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", srv.Addr, "db", dbPath(), "origin", a.Origin())

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

func dbPath() string { return env("FINANCE_DB", "/data/finance.db") }

func addUser(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: finance add-user <name> <display name>")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dbPath())
	if err != nil {
		return err
	}
	defer st.Close()
	u, err := st.CreateUser(ctx, args[0], strings.Join(args[1:], " "))
	if err != nil {
		return err
	}
	fmt.Printf("Created user %q (%s), id %d.\n", u.Name, u.DisplayName, u.ID)
	return nil
}

// seed takes the database path as an explicit argument (not FINANCE_DB), so fake
// data can't land in the real database by accident. Store.Seed also refuses any
// database that already has users.
func seed(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: finance seed <db path> [year]")
	}
	year := month.Of(time.Now()).Year()
	if len(args) == 2 {
		y, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("year: %w", err)
		}
		year = y
	}
	ctx := context.Background()
	st, err := store.Open(ctx, args[0])
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Seed(ctx, year); err != nil {
		return err
	}
	fmt.Printf("Seeded %s with the fictional Nordmann household for %d.\n", args[0], year)
	return nil
}

func renameUser(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: finance rename-user <name> <display name>")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dbPath())
	if err != nil {
		return err
	}
	defer st.Close()
	u, err := st.RenameUser(ctx, args[0], strings.Join(args[1:], " "))
	if err != nil {
		return err
	}
	fmt.Printf("User %q is now shown as %s.\n", u.Name, u.DisplayName)
	return nil
}

func enroll(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: finance enroll <name>")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dbPath())
	if err != nil {
		return err
	}
	defer st.Close()
	a, err := auth.New(st, os.Getenv("FINANCE_ORIGIN"))
	if err != nil {
		return err
	}
	link, expires, err := a.EnrollmentLink(ctx, args[0])
	if err != nil {
		return err
	}
	fmt.Printf("Open this link on the device that should get a passkey for %q.\n", args[0])
	fmt.Printf("It works once and expires at %s.\n\n%s\n", expires.In(time.Local).Format("15:04"), link)
	return nil
}

func resetPasskeys(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: finance reset-passkeys <name>")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dbPath())
	if err != nil {
		return err
	}
	defer st.Close()
	u, err := st.UserByName(ctx, args[0])
	if err != nil {
		return fmt.Errorf("user %q: %w", args[0], err)
	}
	n, err := st.ResetPasskeys(ctx, u.ID)
	if err != nil {
		return err
	}
	fmt.Printf("Removed %d passkey(s) for %q and logged them out everywhere.\n", n, u.Name)
	fmt.Printf("Run `finance enroll %s` to register a new one.\n", u.Name)
	return nil
}

func backupDir() string { return env("FINANCE_BACKUP_DIR", "/data/backups") }

func backupNow() error {
	ctx := context.Background()
	st, err := store.Open(ctx, dbPath())
	if err != nil {
		return err
	}
	defer st.Close()
	path, err := st.Backup(ctx, backupDir(), store.BackupKeep)
	if err != nil {
		return err
	}
	fmt.Printf("Backup written to %s (the newest %d are kept).\n", path, store.BackupKeep)
	return nil
}

// nightlyBackups backs the database up every night at 03:30 local time. If the
// newest backup is more than a day old when the server starts (the Pi was off at
// 03:30, say), it takes one right away.
func nightlyBackups(ctx context.Context, st *store.Store, dir string) {
	run := func() {
		if path, err := st.Backup(ctx, dir, store.BackupKeep); err != nil {
			slog.Error("backup failed", "err", err)
		} else {
			slog.Info("backup written", "path", path)
		}
	}
	if latest, err := store.LatestBackup(dir); err != nil {
		slog.Error("backup check", "err", err)
	} else if time.Since(latest) > 24*time.Hour {
		run()
	}
	for {
		next := store.NextBackup(time.Now(), 3, 30)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			run()
		}
	}
}
