package service_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"fourhour/backend/internal/config"
	"fourhour/backend/internal/model"
	"fourhour/backend/internal/paypal"
	"fourhour/backend/internal/paypal/paypaltest"
	"fourhour/backend/internal/service"
	"fourhour/backend/internal/stream"
)

// Every PayPal response here is hand-written in paypaltest.

var demoIn = service.DemoInput{MemberEmail: "member@example.com", PayoutEmail: "payee@example.com", VaultToken: "tok-demo"}

// restart builds a second Service on the same database and fake PayPal, as
// a process restart would, with the clock at now.
func restart(t *testing.T, e env, now time.Time) *service.Service {
	t.Helper()
	pp, err := paypal.NewClient(paypal.Config{BaseURL: paypal.SandboxBaseURL, ClientID: paypaltest.ClientID, ClientSecret: paypaltest.ClientSecret, HTTPClient: e.fake.HTTPClient(), Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	s := service.New(service.Options{DB: e.db, PayPal: pp, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Hub: stream.NewHub(16), Config: config.Config{},
		ArtifactsDir: e.art, Now: func() time.Time { return now }})
	t.Cleanup(s.Close)
	return s
}

func count(t *testing.T, e env, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDemoPolicyCreatedAndChargedOnceAcrossRestarts(t *testing.T) {
	e := newEnv(t, config.Config{})
	ctx := context.Background()
	writePricing(t, e.art, testCounties)
	q, err := e.svc.Quote(ctx, "48201", 5000)
	if err != nil {
		t.Fatal(err)
	}
	// 0.61*0.6*0.22*5000 = 402.6; /12*1.35 + 150 = 195.2925 -> 195.
	if q.PremiumCents != 195 {
		t.Fatalf("quote %d", q.PremiumCents)
	}

	res, err := e.svc.EnsureDemoPolicy(ctx, demoIn)
	if err != nil {
		t.Fatal(err)
	}
	p := res.Policy
	if !res.Created || p.FIPS != "48201" || p.CoverCents != 5000 || p.PremiumCents != q.PremiumCents || p.ModelVersion != "m-test" || p.Status != model.PolicyActive {
		t.Fatalf("%+v", res)
	}
	if res.Member.Email != demoIn.MemberEmail || res.Member.PayPalEmail != demoIn.PayoutEmail {
		t.Fatalf("member %+v", res.Member)
	}
	if res.Charge == nil || res.Charge.Status != model.ChargeCompleted || res.Charge.Period != "2026-11" || res.Charge.AmountCents != 195 {
		t.Fatalf("charge %+v", res.Charge)
	}
	orders := e.fake.RequestsTo("POST", "/v2/checkout/orders")
	if len(orders) != 1 || orders[0].Header.Get("PayPal-Request-Id") != "prem-"+p.ID+"-2026-11" || !strings.Contains(string(orders[0].Body), `"vault_id":"tok-demo"`) {
		t.Fatalf("order calls %d", len(orders))
	}
	if led, _ := e.svc.Ledger(ctx); led.BalanceCents != 195 || len(led.Entries) != 1 {
		t.Fatalf("ledger %+v", led)
	}

	// Restart in the same month, then in a later month: same policy, no
	// second policy, member or charge, no PayPal call.
	for _, now := range []time.Time{e.now.Add(time.Hour), e.now.AddDate(0, 2, 0)} {
		r2, err := restart(t, e, now).EnsureDemoPolicy(ctx, demoIn)
		if err != nil || r2.Created || r2.Policy.ID != p.ID || r2.Charge == nil || r2.Charge.ID != res.Charge.ID {
			t.Fatalf("restart at %s: %+v %v", now, r2, err)
		}
	}
	if n := len(e.fake.RequestsTo("POST", "/v2/checkout/orders")); n != 1 {
		t.Fatalf("%d order calls after restarts, want 1", n)
	}
	if count(t, e, `SELECT count(*) FROM policies`) != 1 || count(t, e, `SELECT count(*) FROM members`) != 1 || count(t, e, `SELECT count(*) FROM premium_charges`) != 1 {
		t.Fatal("restart wrote a second row")
	}
	// The database refuses a second demo policy whatever the code does.
	if _, err := e.db.Exec(`INSERT INTO demo_policy (slot, policy_id, created_at) VALUES (2, ?, 'x')`, p.ID); err == nil {
		t.Fatal("second demo slot accepted")
	}
	if _, err := e.db.Exec(`INSERT INTO demo_policy (slot, policy_id, created_at) VALUES (1, ?, 'x')`, p.ID); err == nil {
		t.Fatal("second demo row accepted")
	}

	info, err := e.svc.DemoPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(info)
	want := `{"policy_id":"` + p.ID + `","member_name":"` + res.Member.Name + `","fips":"48201","cover_cents":5000,"premium_cents":195}`
	if string(b) != want {
		t.Fatalf("got %s\nwant %s", b, want)
	}
	if strings.Contains(string(b), "tok-demo") {
		t.Fatal("demo response leaks the vault token")
	}
}

func TestDemoPolicyAbsentIsNull(t *testing.T) {
	e := newEnv(t, config.Config{})
	info, err := e.svc.DemoPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(info); string(b) != `{"policy_id":null}` {
		t.Fatalf("got %s", b)
	}
}

func TestDemoPolicyRefusedChargeLoggedNotRetried(t *testing.T) {
	e := newEnv(t, config.Config{})
	ctx := context.Background()
	writePricing(t, e.art, testCounties)
	e.fake.Lock()
	e.fake.OrderFail = http.StatusUnprocessableEntity
	e.fake.Unlock()
	res, err := e.svc.EnsureDemoPolicy(ctx, demoIn)
	if err == nil || service.ErrDebugID(err) == "" {
		t.Fatalf("want an error with a debug_id, got %v", err)
	}
	if res.Policy.ID == "" || res.Charge == nil || res.Charge.Status != model.ChargeFailed {
		t.Fatalf("policy must exist with a failed charge: %+v", res)
	}
	e.fake.Lock()
	e.fake.OrderFail = 0
	e.fake.Unlock()
	r2, err := restart(t, e, e.now).EnsureDemoPolicy(ctx, demoIn)
	if err != nil || r2.Policy.ID != res.Policy.ID || r2.Charge.Status != model.ChargeFailed {
		t.Fatalf("%+v %v", r2, err)
	}
	if n := len(e.fake.RequestsTo("POST", "/v2/checkout/orders")); n != 1 {
		t.Fatalf("a refused first premium was charged again: %d order calls", n)
	}
}

func TestDemoPolicyUnknownOutcomeRetriedWithSameKey(t *testing.T) {
	e := newEnv(t, config.Config{})
	ctx := context.Background()
	writePricing(t, e.art, testCounties)
	e.fake.Lock()
	e.fake.OrderFail = http.StatusInternalServerError
	e.fake.Unlock()
	res, err := e.svc.EnsureDemoPolicy(ctx, demoIn)
	if err == nil || res.Charge == nil || res.Charge.Status != model.ChargeCreated {
		t.Fatalf("%+v %v", res, err)
	}
	e.fake.Lock()
	e.fake.OrderFail = 0
	e.fake.Unlock()
	// Restarted next month: the first premium (this month's) is retried, not next month's.
	r2, err := restart(t, e, e.now.AddDate(0, 1, 0)).EnsureDemoPolicy(ctx, demoIn)
	if err != nil || r2.Charge == nil || r2.Charge.Status != model.ChargeCompleted || r2.Charge.Period != "2026-11" {
		t.Fatalf("%+v %v", r2, err)
	}
	reqs := e.fake.RequestsTo("POST", "/v2/checkout/orders")
	if len(reqs) != 2 || reqs[0].Header.Get("PayPal-Request-Id") != reqs[1].Header.Get("PayPal-Request-Id") {
		t.Fatal("retry must reuse the PayPal-Request-Id")
	}
	if count(t, e, `SELECT count(*) FROM premium_charges`) != 1 {
		t.Fatal("second charge row")
	}
}

func TestDemoPolicyNotCreatedWhenHarrisNotOffered(t *testing.T) {
	e := newEnv(t, config.Config{})
	ctx := context.Background()
	writePricing(t, e.art, `"48201":{"expected_events_per_year":0,"prob_premises_affected":0.1,"credibility_weight":0,"offered":false,"not_offered_reason":"Not offered."}`)
	_, err := e.svc.EnsureDemoPolicy(ctx, demoIn)
	if svcErr(t, err).Code != "county_not_offered" {
		t.Fatal(err)
	}
	if count(t, e, `SELECT count(*) FROM policies`) != 0 || len(e.fake.Requests()) != 0 {
		t.Fatal("nothing may be written or charged")
	}
	if info, _ := e.svc.DemoPolicy(ctx); info.PolicyID != nil {
		t.Fatal(info)
	}
}

// The 100-cent test cover stays seed-only: the public quote and policy
// paths refuse it, and the startup demo policy is never test cover.
func TestTestCoverOnlyFromSeed(t *testing.T) {
	e := newEnv(t, config.Config{AppBaseURL: "https://api.example"})
	ctx := context.Background()
	writePricing(t, e.art, testCounties)
	m, err := e.svc.CreateMember(ctx, "Ana", "ana@example.com", "ana.pp@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Quote(ctx, "48201", 100); svcErr(t, err).Code != "invalid_cover" {
		t.Fatal(err)
	}
	if _, err := e.svc.CreatePolicy(ctx, m.ID, "48201", 100); svcErr(t, err).Code != "invalid_cover" {
		t.Fatal(err)
	}
	res, err := e.svc.EnsureDemoPolicy(ctx, demoIn)
	if err != nil || res.Policy.CoverCents != 5000 || res.Policy.ModelVersion == model.TestCoverModelVersion {
		t.Fatalf("%+v %v", res, err)
	}
	if count(t, e, `SELECT count(*) FROM policies WHERE cover_cents = 100`) != 0 {
		t.Fatal("test cover created outside the seed command")
	}
}
