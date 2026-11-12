package replay_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fourhour/backend/internal/config"
	"fourhour/backend/internal/db"
	"fourhour/backend/internal/model"
	"fourhour/backend/internal/paypal"
	"fourhour/backend/internal/paypal/paypaltest"
	"fourhour/backend/internal/replay"
	"fourhour/backend/internal/service"
	"fourhour/backend/internal/stream"
)

// All PayPal responses here come from paypaltest (hand-written, not recorded).

type env struct {
	svc  *service.Service
	eng  *replay.Engine
	fake *paypaltest.Fake
}

func setup(t *testing.T) env {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	f := paypaltest.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pp, err := paypal.NewClient(paypal.Config{BaseURL: paypal.SandboxBaseURL, ClientID: paypaltest.ClientID, ClientSecret: paypaltest.ClientSecret, HTTPClient: f.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	artDir := t.TempDir() // empty: fixture trigger params, SPEC schedule, fixture scenario
	svc := service.New(service.Options{DB: d, PayPal: pp, Log: log, Hub: stream.NewHub(1024), Config: config.Config{}, ArtifactsDir: artDir,
		PollDelay: 10 * time.Millisecond, PollInterval: 10 * time.Millisecond, PollTimeout: 5 * time.Second})
	t.Cleanup(svc.Close)
	eng := replay.New(replay.Options{Service: svc, Log: log, ArtifactsDir: artDir})
	t.Cleanup(eng.Reset)
	return env{svc: svc, eng: eng, fake: f}
}

type collected struct {
	events []stream.Message
}

func (c *collected) names() string {
	var out []string
	for _, m := range c.events {
		if m.Event != model.EvTick {
			out = append(out, m.Event)
		}
	}
	return strings.Join(out, " ")
}

// collect reads sub until stop returns true for a message or the timeout.
func collect(t *testing.T, sub *stream.Sub, timeout time.Duration, stop func(stream.Message) bool) *collected {
	t.Helper()
	c := &collected{}
	deadline := time.After(timeout)
	for {
		select {
		case m, ok := <-sub.C:
			if !ok {
				t.Fatal("subscriber dropped")
			}
			c.events = append(c.events, m)
			if stop(m) {
				return c
			}
		case <-deadline:
			t.Fatalf("timeout; got %s", c.names())
		}
	}
}

func TestLoopSeedChargeReplayR2PayoutConfirmed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	seed, err := e.svc.Seed(ctx, service.SeedInput{MemberEmail: "member@example.com", PayoutEmail: "payee@example.com", VaultToken: "vault-token-x", FIPS: "48201"})
	if err != nil || !seed.Created || seed.Policy.CoverCents != 100 || seed.Policy.ModelVersion != "test-cover" {
		t.Fatalf("seed: %+v %v", seed, err)
	}
	again, err := e.svc.Seed(ctx, service.SeedInput{MemberEmail: "member@example.com", PayoutEmail: "payee@example.com", VaultToken: "vault-token-x", FIPS: "48201"})
	if err != nil || again.Created || again.Policy.ID != seed.Policy.ID {
		t.Fatalf("seed must be idempotent: %+v %v", again, err)
	}
	ch, err := e.svc.ChargePremium(ctx, seed.Policy.ID, "2026-11")
	if err != nil || ch.Status != model.ChargeCompleted || ch.AmountCents != 100 || ch.RequestID != "prem-"+seed.Policy.ID+"-2026-11" {
		t.Fatalf("charge: %+v %v", ch, err)
	}
	var body struct {
		PaymentSource struct {
			PayPal struct {
				VaultID string `json:"vault_id"`
			} `json:"paypal"`
		} `json:"payment_source"`
		PurchaseUnits []struct {
			Amount paypal.Money `json:"amount"`
		} `json:"purchase_units"`
	}
	orders := e.fake.RequestsTo("POST", "/v2/checkout/orders")
	if len(orders) != 1 || json.Unmarshal(orders[0].Body, &body) != nil || body.PaymentSource.PayPal.VaultID != "vault-token-x" || body.PurchaseUnits[0].Amount.Value != "1.00" {
		t.Fatalf("order request: %s", orders[0].Body)
	}

	sub, snap, err := e.eng.Subscribe(ctx)
	if err != nil || snap.ReplayID != nil {
		t.Fatalf("snapshot before replay: %+v %v", snap, err)
	}
	defer e.svc.Hub().Unsubscribe(sub)
	res, err := e.eng.Start("fixture-outage-harris", 900*1000) // 15 min per ms
	if err != nil || res.ReplayID == "" {
		t.Fatal(res, err)
	}
	c := collect(t, sub, 10*time.Second, func(m stream.Message) bool { return m.Event == model.EvPayoutConfirmed })
	names := c.names()
	for _, want := range []string{"event_opened", "claim_decided", "payout_sent", "payout_confirmed"} {
		if !strings.Contains(names, want) {
			t.Fatalf("missing %s in %s", want, names)
		}
	}
	var claim model.Claim
	var confirmed model.Payout
	for _, m := range c.events {
		switch m.Event {
		case model.EvClaimDecided:
			var b model.ClaimDecided
			if err := json.Unmarshal(m.Data, &b); err != nil {
				t.Fatal(err)
			}
			claim = b.Claim
		case model.EvPayoutConfirmed:
			var b model.PayoutEvent
			if err := json.Unmarshal(m.Data, &b); err != nil {
				t.Fatal(err)
			}
			confirmed = b.Payout
		}
	}
	if claim.Rule != "R2" || claim.AmountCents != 30 || claim.Decision != "paid" || claim.Tier != "t4" || claim.OutageEventID == nil || claim.SimTime == nil {
		t.Fatalf("claim %+v", claim)
	}
	// The fixture's theta_hi run starts at 02:00 (reading 8), so it reaches 4h at 06:00.
	if *claim.SimTime != "2000-01-01T06:00:00Z" {
		t.Errorf("claim sim_time %s", *claim.SimTime)
	}
	if confirmed.Status != model.PayoutSuccess || confirmed.ConfirmedVia == nil || *confirmed.ConfirmedVia != "poll" || confirmed.AmountCents != 30 ||
		confirmed.SenderBatchID != "clm-"+seed.Policy.ID+"-"+*claim.OutageEventID+"-t4" || confirmed.PayPalItemID == nil {
		t.Fatalf("payout %+v", confirmed)
	}
	var pb struct {
		SenderBatchHeader paypal.SenderBatchHeader   `json:"sender_batch_header"`
		Items             []paypal.PayoutItemRequest `json:"items"`
	}
	pr := e.fake.RequestsTo("POST", "/v1/payments/payouts")
	if len(pr) != 1 || json.Unmarshal(pr[0].Body, &pb) != nil || pb.Items[0].Receiver != "payee@example.com" || pb.Items[0].Amount.Value != "0.30" || pb.Items[0].SenderItemID != claim.ID {
		t.Fatalf("payout request: %s", pr[0].Body)
	}
	<-e.eng.Done()
	led, err := e.svc.Ledger(ctx)
	if err != nil || led.BalanceCents != 70 || len(led.Entries) != 2 {
		t.Fatalf("ledger %+v %v", led, err)
	}
	// Exactly one claim for the event, even after the replay ended.
	items, err := e.svc.ClaimItems(ctx, nil)
	if err != nil || len(items) != 1 || items[0].Payout == nil || items[0].MemberName == "" || items[0].FIPS != "48201" {
		t.Fatalf("claims %+v %v", items, err)
	}
	// A repeated charge for the same period: same row, no PayPal call.
	ch2, err := e.svc.ChargePremium(ctx, seed.Policy.ID, "2026-11")
	if err != nil || ch2.ID != ch.ID || len(e.fake.RequestsTo("POST", "/v2/checkout/orders")) != 1 {
		t.Fatalf("repeat charge: %+v %v", ch2, err)
	}
}

func TestSnapshotOnReconnectAndStop(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.eng.Start("fixture-outage-harris", 900); err != nil { // 1 reading per second
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	sub, snap, err := e.eng.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer e.svc.Hub().Unsubscribe(sub)
	if snap.ReplayID == nil || *snap.ScenarioID != "fixture-outage-harris" || len(snap.Series) < 1 || snap.SimTime == nil {
		t.Fatalf("%+v", snap)
	}
	m := collect(t, sub, 3*time.Second, func(m stream.Message) bool { return m.Event == model.EvTick })
	var tk model.Tick
	if err := json.Unmarshal(m.events[len(m.events)-1].Data, &tk); err != nil {
		t.Fatal(err)
	}
	if tk.SimTime <= snap.Series[len(snap.Series)-1].SimTime {
		t.Fatalf("tick after snapshot must be newer: %s vs %s", tk.SimTime, snap.Series[len(snap.Series)-1].SimTime)
	}
	if !e.eng.Stop() {
		t.Fatal("stop")
	}
	collect(t, sub, 3*time.Second, func(m stream.Message) bool { return m.Event == model.EvReplayEnded })
	_, snap2, err := e.eng.Subscribe(ctx)
	if err != nil || snap2.ReplayID != nil || len(snap2.Series) != 0 {
		t.Fatalf("after stop: %+v", snap2)
	}
}

func TestStartValidation(t *testing.T) {
	e := setup(t)
	if _, err := e.eng.Start("nope", 3600); err == nil {
		t.Error("unknown scenario")
	}
	if _, err := e.eng.Start("fixture-outage-harris", 0); err == nil {
		t.Error("zero speed")
	}
	sc := e.eng.Scenarios()
	if len(sc) != 1 || !sc[0].IsFixture || sc[0].Provenance == nil || sc[0].Source == nil {
		t.Fatalf("%+v", sc)
	}
}
