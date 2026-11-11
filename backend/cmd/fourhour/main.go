// Command fourhour is the FourHour backend service.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fourhour/backend/internal/api"
	"fourhour/backend/internal/artifacts"
	"fourhour/backend/internal/config"
	"fourhour/backend/internal/db"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], config.OSLookup, os.Stderr, nil)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fourhour: %v\n", err)
		os.Exit(1)
	}
}

// run starts the service and blocks until ctx is cancelled. It validates the
// configuration before touching the database or the network, so a
// non-sandbox PAYPAL_BASE_URL has no side effects. ready, if set, receives
// the listening address.
func run(ctx context.Context, args []string, lookup config.LookupFunc, logOut io.Writer, ready func(addr string)) error {
	fs := flag.NewFlagSet("fourhour", flag.ContinueOnError)
	fs.SetOutput(logOut)
	addr := fs.String("addr", "", "listen address (default \":$PORT\", PORT defaults to 8080)")
	envFile := fs.String("env-file", "", "optional KEY=VALUE file read before the process environment (process environment wins)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(logOut, nil))

	cfg, err := config.Load(lookup, *envFile)
	if err != nil {
		return err
	}
	log.Info("config loaded", "paypal_base_url", cfg.PayPalBaseURL, "database_url", cfg.DatabaseURL, "artifacts_dir", cfg.ArtifactsDir)
	if m := cfg.Missing(); len(m) > 0 {
		log.Warn("environment variables not set; they become required at task B1", "missing", m)
	}
	if *addr == "" {
		*addr = ":" + cfg.Port
	}

	database, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := database.Close(); cerr != nil {
			log.Error("close database", "err", cerr)
		}
	}()
	applied, err := db.Migrate(ctx, database)
	if err != nil {
		return err
	}
	log.Info("migrations", "applied", applied)

	report := artifacts.Load(cfg.ArtifactsDir, log)

	srv := &http.Server{
		Handler:           api.NewHandler(api.Deps{DB: database, Log: log, ArtifactsLoaded: report.LoadedNames()}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: the SSE stream (B3) is long-lived.
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", *addr, err)
	}
	log.Info("listening", "addr", ln.Addr().String())
	if ready != nil {
		ready(ln.Addr().String())
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
