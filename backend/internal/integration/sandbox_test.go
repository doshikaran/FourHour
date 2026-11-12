// Package integration holds the opt-in test that runs the core loop
// against the real PayPal sandbox:
//
//	seed -> premium charge -> fixture replay -> rule R2 -> payout -> confirmed by polling
//
// It is OFF by default. It runs only when FOURHOUR_SANDBOX_IT=1, reads the
// repo-root .env through package config, and refuses anything but the
// sandbox base URL. Money limits: the seeded test cover charges 100 cents
// and R2 pays 100 × 0.6 × 0.5 = 30 cents; the test checks both amounts
// before the calls that move them. Every paypal-debug-id is printed. The
// client secret, access token and DEMO_VAULT_TOKEN are never printed.
package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fourhour/backend/internal/config"
	"fourhour/backend/internal/db"
	"fourhour/backend/internal/model"
	"fourhour/backend/internal/paypal"
	"fourhour/backend/internal/replay"
	"fourhour/backend/internal/service"
	"fourhour/backend/internal/stream"
)

// Hard money limits for a run (the humans' brief: at most $1.00 each).
const (
	maxChargeCents = 100
	maxPayoutCents = 100
	wantPayout     = 30
)

func repoEnvFile(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "CONTRACTS.md")); err == nil {
			return filepath.Join(dir, ".env")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root (CONTRACTS.md) not found")
		}
		dir = parent
	}
}

func TestSandboxLoop(t *testing.T) {
	if !config.IntegrationEnabled(config.OSLookup) {
		t.Skip("sandbox integration test is off; set " + config.IntegrationFlag + "=1 to run it against the real PayPal sandbox")
	}
	cfg, err := config.Load(config.OSLookup, repoEnvFile(t))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.PayPalBaseURL != paypal.SandboxBaseURL {
		t.Fatal("not the sandbox") // config.Load already refuses; belt and braces
	}
	if cfg.DemoVaultToken == "" || cfg.SandboxMemberEmail == "" || cfg.SandboxPayoutEmail == "" || cfg.PayPalClientID == "" || cfg.PayPalClientSecret == "" {
		t.Fatal("need PAYPAL_CLIENT_ID, PAYPAL_CLIENT_SECRET, DEMO_VAULT_TOKEN, SANDBOX_MEMBER_EMAIL and SANDBOX_PAYOUT_EMAIL")
	}

	var mu sync.Mutex
	var debugIDs []string
	pp, err := paypal.NewClient(paypal.Config{
		BaseURL: cfg.PayPalBaseURL, ClientID: cfg.PayPalClientID, ClientSecret: cfg.PayPalClientSecret,
		OnResponse: func(r paypal.ResponseInfo) {
			line := fmt.Sprintf("%s %s -> HTTP %d paypal-debug-id=%s", r.Method, r.Path, r.StatusCode, r.DebugID)
			mu.Lock()
			debugIDs = append(debugIDs, line)
			mu.Unlock()
			t.Log("PAYPAL " + line)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tmp := t.TempDir()
	d, err := db.Open(ctx, filepath.Join(tmp, "it.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := db.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelInfo}))
	artDir := filepath.Join(tmp, "artifacts") // empty: fixture trigger params and scenario, SPEC schedule
	svc := service.New(service.Options{DB: d, PayPal: pp, Log: log, Hub: stream.NewHub(4096), Config: cfg, ArtifactsDir: artDir})
	defer svc.Close()
	eng := replay.New(replay.Options{Service: svc, Log: log, ArtifactsDir: artDir})
	defer eng.Reset()
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		t.Log("ALL PAYPAL CALLS:")
		for _, l := range debugIDs {
			t.Log("  " + l)
		}
	}()

	// 1. Seed: no PayPal call.
	seed, err := svc.Seed(ctx, service.SeedInput{MemberEmail: cfg.SandboxMemberEmail, PayoutEmail: cfg.SandboxPayoutEmail, VaultToken: cfg.DemoVaultToken, FIPS: "48201"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Logf("STEP seed: member=%s policy=%s cover_cents=%d premium_cents=%d", seed.Member.ID, seed.Policy.ID, seed.Policy.CoverCents, seed.Policy.PremiumCents)
	if seed.Policy.PremiumCents > maxChargeCents || seed.Policy.CoverCents != 100 {
		t.Fatalf("refusing: premium %d or cover %d over the test limit", seed.Policy.PremiumCents, seed.Policy.CoverCents)
	}

	// 2. Premium charge (P4).
	ch, err := svc.ChargePremium(ctx, seed.Policy.ID, svc.CurrentPeriod())
	if err != nil {
		t.Fatalf("STOP premium charge failed: %v\n%s", err, errorBody(err, cfg.DemoVaultToken))
	}
	t.Logf("STEP charge: charge=%s status=%s amount_cents=%d order_id=%s capture_id=%s request_id=%s", ch.ID, ch.Status, ch.AmountCents, deref(ch.PayPalOrderID), deref(ch.PayPalCaptureID), ch.RequestID)
	if ch.Status != model.ChargeCompleted {
		t.Fatalf("STOP premium charge status %s, want completed", ch.Status)
	}

	// 3. Replay the fixture; R2 fires; the payout is sent and polled.
	sub, _, err := eng.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Hub().Unsubscribe(sub)
	if _, err := eng.Start("fixture-outage-harris", 900*20); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(4 * time.Minute)
	var claim model.Claim
	var sent, confirmed *model.Payout
	for confirmed == nil {
		select {
		case m, ok := <-sub.C:
			if !ok {
				t.Fatal("stream subscriber dropped")
			}
			switch m.Event {
			case model.EvClaimDecided:
				var b model.ClaimDecided
				if err := json.Unmarshal(m.Data, &b); err != nil {
					t.Fatal(err)
				}
				claim = b.Claim
				t.Logf("STEP claim_decided: claim=%s rule=%s decision=%s amount_cents=%d sim_time=%s", claim.ID, claim.Rule, claim.Decision, claim.AmountCents, deref(claim.SimTime))
				if claim.AmountCents > maxPayoutCents {
					t.Fatalf("refusing: payout %d over the limit", claim.AmountCents) // the send is already queued; this cannot happen with cover 100
				}
			case model.EvPayoutSent:
				var b model.PayoutEvent
				if err := json.Unmarshal(m.Data, &b); err != nil {
					t.Fatal(err)
				}
				sent = &b.Payout
				t.Logf("STEP payout_sent: payout=%s status=%s batch=%s sender_batch_id=%s amount_cents=%d", sent.ID, sent.Status, deref(sent.PayPalBatchID), sent.SenderBatchID, sent.AmountCents)
				if sent.Status == model.PayoutFailed {
					t.Fatalf("STOP payout failed at send")
				}
			case model.EvPayoutConfirmed:
				var b model.PayoutEvent
				if err := json.Unmarshal(m.Data, &b); err != nil {
					t.Fatal(err)
				}
				confirmed = &b.Payout
			}
		case <-deadline:
			p := "none"
			if sent != nil {
				if cur, err := svc.GetPayout(ctx, sent.ID); err == nil {
					p = fmt.Sprintf("%+v", cur)
				}
			}
			t.Fatalf("STOP timeout waiting for payout_confirmed; payout now: %s", p)
		}
	}
	t.Logf("STEP payout_confirmed: payout=%s status=%s via=%s batch=%s item=%s confirmed_at=%s", confirmed.ID, confirmed.Status, deref(confirmed.ConfirmedVia), deref(confirmed.PayPalBatchID), deref(confirmed.PayPalItemID), deref(confirmed.ConfirmedAt))
	if claim.Rule != "R2" || claim.AmountCents != wantPayout {
		t.Errorf("claim %s %d, want R2 %d", claim.Rule, claim.AmountCents, wantPayout)
	}
	if confirmed.Status != model.PayoutSuccess || deref(confirmed.ConfirmedVia) != model.ViaPoll {
		t.Errorf("payout %s via %s, want success via poll", confirmed.Status, deref(confirmed.ConfirmedVia))
	}
	led, err := svc.Ledger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("STEP ledger: balance_cents=%d entries=%d", led.BalanceCents, len(led.Entries))
	if led.BalanceCents != 100-wantPayout {
		t.Errorf("ledger balance %d, want %d", led.BalanceCents, 100-wantPayout)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("cancelled")
	}
}

// errorBody returns PayPal's error body for diagnosis, with the vault token
// removed in case the error echoes it.
func errorBody(err error, secret string) string {
	ae, ok := paypal.AsAPIError(err)
	if !ok {
		return "(no PayPal error body)"
	}
	b := string(ae.Body)
	if secret != "" {
		b = strings.ReplaceAll(b, secret, "<redacted>")
	}
	return "paypal error body: " + b
}

func deref(p *string) string {
	if p == nil {
		return "<null>"
	}
	return *p
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	w.t.Log(string(b))
	return len(b), nil
}
