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
	"fourhour/backend/internal/paypal"
	"fourhour/backend/internal/replay"
	"fourhour/backend/internal/service"
	"fourhour/backend/internal/stream"
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
	if len(args) > 0 && args[0] == "seed" {
		return runSeed(ctx, args[1:], lookup, logOut)
	}
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
		log.Warn("optional environment variables not set", "missing", m)
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
	log.Info("artifacts at startup", "loaded", report.LoadedNames())

	pp, err := newPayPal(cfg, log)
	if err != nil {
		return err
	}
	svc := service.New(service.Options{DB: database, PayPal: pp, Log: log, Hub: stream.NewHub(1024), Config: cfg, ArtifactsDir: cfg.ArtifactsDir})
	defer svc.Close()
	eng := replay.New(replay.Options{Service: svc, Log: log, ArtifactsDir: cfg.ArtifactsDir})
	defer eng.Reset()
	if err := svc.ResumePayouts(ctx); err != nil {
		log.Error("resume payouts", "err", err)
	}
	startupDemo(ctx, cfg, svc, log)

	reqCtx, cancelReqs := context.WithCancel(context.Background())
	defer cancelReqs()
	srv := &http.Server{
		Handler:           api.NewHandler(api.Deps{DB: database, Log: log, Service: svc, Engine: eng, ArtifactsDir: cfg.ArtifactsDir, FrontendURL: cfg.FrontendURL}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: the SSE stream is long-lived. Request contexts
		// derive from reqCtx, cancelled at shutdown so streams end.
		BaseContext: func(net.Listener) context.Context { return reqCtx },
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
	cancelReqs()
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

// startupDemo creates the demo policy and charges its first premium when
// DEMO_VAULT_TOKEN is set (CONTRACTS.md section 3, "Demo policy"). Every
// failure is logged, with the PayPal debug_id when there is one, and never
// stops the server. On a new database this makes a real sandbox charge.
func startupDemo(ctx context.Context, cfg config.Config, svc *service.Service, log *slog.Logger) {
	if cfg.DemoVaultToken == "" {
		return
	}
	dctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	res, err := svc.EnsureDemoPolicy(dctx, service.DemoInput{MemberEmail: cfg.SandboxMemberEmail, PayoutEmail: cfg.SandboxPayoutEmail, VaultToken: cfg.DemoVaultToken})
	if res.Policy.ID != "" {
		attrs := []any{"policy_id", res.Policy.ID, "created", res.Created, "fips", res.Policy.FIPS, "cover_cents", res.Policy.CoverCents, "premium_cents", res.Policy.PremiumCents, "model_version", res.Policy.ModelVersion}
		if res.Charge != nil {
			attrs = append(attrs, "charge_id", res.Charge.ID, "period", res.Charge.Period, "charge_status", res.Charge.Status)
		}
		log.Info("demo policy", attrs...)
	}
	if err != nil {
		log.Error("demo policy failed; the server continues", "err", err, "paypal_debug_id", service.ErrDebugID(err))
	}
}

// newPayPal builds the PayPal client, or returns nil (with a warning) when
// the credentials are not set: the service then runs, and money endpoints
// answer 503 paypal_unavailable.
func newPayPal(cfg config.Config, log *slog.Logger) (*paypal.Client, error) {
	if cfg.PayPalClientID == "" || cfg.PayPalClientSecret == "" {
		log.Warn("PAYPAL_CLIENT_ID or PAYPAL_CLIENT_SECRET not set; PayPal calls are disabled")
		return nil, nil
	}
	pp, err := paypal.NewClient(paypal.Config{BaseURL: cfg.PayPalBaseURL, ClientID: cfg.PayPalClientID, ClientSecret: cfg.PayPalClientSecret,
		OnResponse: service.LogPayPalResponse(log)})
	if err != nil {
		return nil, fmt.Errorf("paypal client: %w", err)
	}
	return pp, nil
}

// runSeed creates (or finds) the demo member and active test-cover policy
// from DEMO_VAULT_TOKEN and SANDBOX_MEMBER_EMAIL, and with -charge also
// charges the current month's 100-cent premium. It refuses to run if either
// variable is missing. It is idempotent: a second run finds the same
// policy, and a second -charge for the same month charges nothing.
func runSeed(ctx context.Context, args []string, lookup config.LookupFunc, out io.Writer) error {
	fs := flag.NewFlagSet("fourhour seed", flag.ContinueOnError)
	fs.SetOutput(out)
	envFile := fs.String("env-file", "", "optional KEY=VALUE file read before the process environment")
	fips := fs.String("fips", "48201", "county FIPS for the test policy")
	charge := fs.Bool("charge", false, "also charge the current month's premium (100 cents) with the saved wallet")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(lookup, *envFile)
	if err != nil {
		return err
	}
	var missing []string
	if cfg.DemoVaultToken == "" {
		missing = append(missing, "DEMO_VAULT_TOKEN")
	}
	if cfg.SandboxMemberEmail == "" {
		missing = append(missing, "SANDBOX_MEMBER_EMAIL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("seed refused: %v not set", missing)
	}
	payee := cfg.SandboxPayoutEmail
	if payee == "" {
		payee = cfg.SandboxMemberEmail
	}
	log := slog.New(slog.NewTextHandler(out, nil))
	database, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()
	if _, err := db.Migrate(ctx, database); err != nil {
		return err
	}
	var pp *paypal.Client
	if *charge {
		if pp, err = newPayPal(cfg, log); err != nil {
			return err
		}
		if pp == nil {
			return fmt.Errorf("seed -charge needs PAYPAL_CLIENT_ID and PAYPAL_CLIENT_SECRET")
		}
	}
	svc := service.New(service.Options{DB: database, PayPal: pp, Log: log, Config: cfg, ArtifactsDir: cfg.ArtifactsDir})
	defer svc.Close()
	res, err := svc.Seed(ctx, service.SeedInput{MemberEmail: cfg.SandboxMemberEmail, PayoutEmail: payee, VaultToken: cfg.DemoVaultToken, FIPS: *fips})
	if err != nil {
		return err
	}
	state := "existing"
	if res.Created {
		state = "created"
	}
	fmt.Fprintf(out, "seed: %s member=%s policy=%s fips=%s cover_cents=%d premium_cents=%d model_version=%s payout_email=%s\n",
		state, res.Member.ID, res.Policy.ID, res.Policy.FIPS, res.Policy.CoverCents, res.Policy.PremiumCents, res.Policy.ModelVersion, res.Member.PayPalEmail)
	if *charge {
		ch, err := svc.ChargePremium(ctx, res.Policy.ID, svc.CurrentPeriod())
		if err != nil {
			return fmt.Errorf("seed charge: %w", err)
		}
		fmt.Fprintf(out, "seed: charge=%s period=%s status=%s amount_cents=%d order_id=%s\n", ch.ID, ch.Period, ch.Status, ch.AmountCents, deref(ch.PayPalOrderID))
	}
	return nil
}

func deref(p *string) string {
	if p == nil {
		return "<null>"
	}
	return *p
}
