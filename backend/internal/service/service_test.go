package service_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fourhour/backend/internal/config"
	"fourhour/backend/internal/db"
	"fourhour/backend/internal/model"
	"fourhour/backend/internal/paypal"
	"fourhour/backend/internal/paypal/paypaltest"
	"fourhour/backend/internal/rules"
	"fourhour/backend/internal/service"
	"fourhour/backend/internal/stream"
	"fourhour/backend/internal/witness"
)

// Every PayPal response in these tests is hand-written in paypaltest.

type env struct {
	svc  *service.Service
	db   *sql.DB
	fake *paypaltest.Fake
	art  string
	now  time.Time
}

func newEnv(t *testing.T, cfg config.Config) env {
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
	pp, err := paypal.NewClient(paypal.Config{BaseURL: paypal.SandboxBaseURL, ClientID: paypaltest.ClientID, ClientSecret: paypaltest.ClientSecret, HTTPClient: f.HTTPClient(), Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 19, 0, 0, 0, time.UTC)
	art := t.TempDir()
	svc := service.New(service.Options{DB: d, PayPal: pp, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Hub: stream.NewHub(1024), Config: cfg,
		ArtifactsDir: art, Now: func() time.Time { return now }, PollDelay: 5 * time.Millisecond, PollInterval: 5 * time.Millisecond, PollTimeout: 2 * time.Second})
	t.Cleanup(svc.Close)
	return env{svc: svc, db: d, fake: f, art: art, now: now}
}

func (e env) seed(t *testing.T) model.Policy {
	t.Helper()
	r, err := e.svc.Seed(context.Background(), service.SeedInput{MemberEmail: "m@example.com", PayoutEmail: "p@example.com", VaultToken: "tok", FIPS: "48201"})
	if err != nil {
		t.Fatal(err)
	}
	return r.Policy
}

func svcErr(t *testing.T, err error) *service.Error {
	t.Helper()
	var se *service.Error
	if !errors.As(err, &se) {
		t.Fatalf("want *service.Error, got %v", err)
	}
	return se
}

// --- B2: premium charges ----------------------------------------------------

func TestChargeOncePerPeriodAndRowUnique(t *testing.T) {
	e := newEnv(t, config.Config{})
	p := e.seed(t)
	ctx := context.Background()
	c1, err := e.svc.ChargePremium(ctx, p.ID, "2026-12")
	if err != nil || c1.Status != model.ChargeCompleted || c1.PayPalOrderID == nil || c1.PayPalCaptureID == nil {
		t.Fatalf("%+v %v", c1, err)
	}
	c2, err := e.svc.ChargePremium(ctx, p.ID, "2026-12")
	if err != nil || c2.ID != c1.ID {
		t.Fatalf("%+v %v", c2, err)
	}
	if n := len(e.fake.RequestsTo("POST", "/v2/checkout/orders")); n != 1 {
		t.Fatalf("%d PayPal order calls, want 1", n)
	}
	if h := e.fake.RequestsTo("POST", "/v2/checkout/orders")[0].Header.Get("PayPal-Request-Id"); h != "prem-"+p.ID+"-2026-12" {
		t.Fatalf("request id %q", h)
	}
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM premium_charges WHERE policy_id = ?`, p.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows %d %v", n, err)
	}
	// The database refuses a second row for the period whatever the code does.
	if _, err := e.db.Exec(`INSERT INTO premium_charges VALUES ('chg_x', ?, '2026-12', 100, 'other', NULL, NULL, 'created', '2026-11-11T00:00:00Z')`, p.ID); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("want unique violation, got %v", err)
	}
	led, _ := e.svc.Ledger(ctx)
	if led.BalanceCents != 100 || len(led.Entries) != 1 {
		t.Fatalf("ledger %+v", led)
	}
	if _, err := e.svc.ChargePremium(ctx, p.ID, "2026-13"); svcErr(t, err).Code != "invalid_period" {
		t.Fatal(err)
	}
}

func TestChargeRefusedMarksFailedWithDebugID(t *testing.T) {
	e := newEnv(t, config.Config{})
	p := e.seed(t)
	e.fake.Lock()
	e.fake.OrderFail = http.StatusUnprocessableEntity
	e.fake.Unlock()
	_, err := e.svc.ChargePremium(context.Background(), p.ID, "2026-12")
	se := svcErr(t, err)
	if se.Code != "paypal_error" || se.DebugID == nil || *se.DebugID == "" {
		t.Fatalf("%+v", se)
	}
	var st string
	if err := e.db.QueryRow(`SELECT status FROM premium_charges WHERE policy_id = ?`, p.ID).Scan(&st); err != nil || st != model.ChargeFailed {
		t.Fatalf("status %q %v", st, err)
	}
	if led, _ := e.svc.Ledger(context.Background()); len(led.Entries) != 0 {
		t.Fatal("failed charge must not reach the ledger")
	}
}

func TestChargeUnknownOutcomeRetriesSameKey(t *testing.T) {
	e := newEnv(t, config.Config{})
	p := e.seed(t)
	e.fake.Lock()
	e.fake.OrderFail = http.StatusInternalServerError
	e.fake.Unlock()
	if _, err := e.svc.ChargePremium(context.Background(), p.ID, "2026-12"); err == nil {
		t.Fatal("want error")
	}
	var st string
	if err := e.db.QueryRow(`SELECT status FROM premium_charges WHERE policy_id = ?`, p.ID).Scan(&st); err != nil || st != model.ChargeCreated {
		t.Fatalf("a 5xx leaves the outcome unknown: status %q", st)
	}
	e.fake.Lock()
	e.fake.OrderFail = 0
	e.fake.Unlock()
	c, err := e.svc.ChargePremium(context.Background(), p.ID, "2026-12")
	if err != nil || c.Status != model.ChargeCompleted {
		t.Fatalf("%+v %v", c, err)
	}
	reqs := e.fake.RequestsTo("POST", "/v2/checkout/orders")
	if len(reqs) != 2 || reqs[0].Header.Get("PayPal-Request-Id") != reqs[1].Header.Get("PayPal-Request-Id") {
		t.Fatal("retry must reuse the PayPal-Request-Id")
	}
}

func TestChargePendingCaptureStaysCreatedUntilWebhook(t *testing.T) {
	e := newEnv(t, config.Config{PayPalWebhookID: "WH-ID"})
	p := e.seed(t)
	e.fake.Lock()
	e.fake.OrderStatus, e.fake.CaptureStatus = "COMPLETED", "PENDING"
	e.fake.Unlock()
	c, err := e.svc.ChargePremium(context.Background(), p.ID, "2026-12")
	if err != nil || c.Status != model.ChargeCreated || c.PayPalCaptureID == nil {
		t.Fatalf("%+v %v", c, err)
	}
	if led, _ := e.svc.Ledger(context.Background()); len(led.Entries) != 0 {
		t.Fatal("pending capture must not reach the ledger")
	}
	body := `{"id":"WH-CAP-1","event_type":"PAYMENT.CAPTURE.COMPLETED","resource_type":"capture","resource":{"id":"` + *c.PayPalCaptureID + `","status":"COMPLETED"}}`
	if err := e.svc.HandleWebhook(context.Background(), webhookHeaders(), []byte(body)); err != nil {
		t.Fatal(err)
	}
	var st string
	if err := e.db.QueryRow(`SELECT status FROM premium_charges WHERE id = ?`, c.ID).Scan(&st); err != nil || st != model.ChargeCompleted {
		t.Fatalf("status %q", st)
	}
	if led, _ := e.svc.Ledger(context.Background()); led.BalanceCents != 100 {
		t.Fatalf("ledger %+v", led)
	}
}

func TestChargeNeedsActivePolicy(t *testing.T) {
	e := newEnv(t, config.Config{})
	if _, err := e.svc.ChargePremium(context.Background(), "pol_missing", "2026-12"); svcErr(t, err).Code != "policy_not_found" {
		t.Fatal(err)
	}
}

// --- B2: enrolment ------------------------------------------------------------

func writePricing(t *testing.T, dir string, counties string) {
	t.Helper()
	body := `{"model_version":"m-test","counties":{` + counties + `},"rates":{"risk_margin_rate":0.35,"expense_load_cents":150},"payout_schedule":{"t4":0.6},` +
		`"provenance":{"generated_at":"2026-11-11T00:00:00Z","git_commit":"x","code":"test","data_sources":[],"train_years":[2014,2022],"test_years":[2023,2024],"seed":1,"notes":"test input"}}`
	if err := os.WriteFile(filepath.Join(dir, "pricing_table.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// testCounties: Harris offered; Miami-Dade offered with a null input;
// Cook not offered. Every other county is missing.
const testCounties = `"48201":{"expected_events_per_year":0.61,"prob_premises_affected":0.22,"credibility_weight":0.74,"offered":true,"not_offered_reason":null},` +
	`"12086":{"expected_events_per_year":null,"prob_premises_affected":0.1,"credibility_weight":0.1,"offered":true,"not_offered_reason":null},` +
	`"17031":{"expected_events_per_year":0,"prob_premises_affected":0.1,"credibility_weight":0,"offered":false,"not_offered_reason":"No qualifying event in this county's credibility group."}`

func TestCountyNotOfferedRefusesQuoteAndPolicy(t *testing.T) {
	e := newEnv(t, config.Config{AppBaseURL: "https://api.example", FrontendURL: "https://app.example"})
	ctx := context.Background()
	writePricing(t, e.art, testCounties)
	m, err := e.svc.CreateMember(ctx, "Ana", "ana@example.com", "ana.pp@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for fips, reason := range map[string]string{
		"17031": "No qualifying event in this county's credibility group.",
		"06037": "No price has been computed for this county.",
	} {
		se := svcErr(t, func() error { _, err := e.svc.Quote(ctx, fips, 5000); return err }())
		if se.Status != 422 || se.Code != "county_not_offered" || se.Message != reason {
			t.Errorf("quote %s: %+v", fips, se)
		}
		se = svcErr(t, func() error { _, err := e.svc.CreatePolicy(ctx, m.ID, fips, 5000); return err }())
		if se.Status != 422 || se.Code != "county_not_offered" || se.Message != reason {
			t.Errorf("policy %s: %+v", fips, se)
		}
	}
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM policies`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("policies written for a county not offered: %d %v", n, err)
	}
	if len(e.fake.RequestsTo("POST", "/v3/vault/setup-tokens")) != 0 {
		t.Fatal("PayPal called for a county not offered")
	}
}

func TestQuoteAndEnrolmentFlow(t *testing.T) {
	e := newEnv(t, config.Config{AppBaseURL: "https://api.example", FrontendURL: "https://app.example"})
	ctx := context.Background()
	if _, err := e.svc.Quote(ctx, "48201", 15000); svcErr(t, err).Code != "artifact_missing" {
		t.Fatal(err)
	}
	writePricing(t, e.art, testCounties)
	// share = t4 = 0.6; expected = 0.61*0.6*0.22*15000 = 1207.8;
	// premium = 1207.8/12*1.35 + 150 = 285.8775 -> 286.
	q, err := e.svc.Quote(ctx, "48201", 15000)
	if err != nil || q.PremiumCents != 286 || q.Breakdown.ExpectedAnnualPayoutCents != 1208 || q.ModelVersion != "m-test" || q.Breakdown.ExpectedPayoutShare != "0.6" {
		t.Fatalf("%+v %v", q, err)
	}
	qb, _ := json.Marshal(q)
	if !strings.Contains(string(qb), `"expected_payout_share":0.6`) || strings.Contains(string(qb), "prob_24h") {
		t.Fatalf("breakdown shape: %s", qb)
	}
	if _, err := e.svc.Quote(ctx, "12086", 15000); svcErr(t, err).Code != "pricing_not_computed" {
		t.Fatal(err)
	}
	if _, err := e.svc.Quote(ctx, "48201", 100); svcErr(t, err).Code != "invalid_cover" {
		t.Fatal("test cover must never be quoted")
	}
	m, err := e.svc.CreateMember(ctx, "Ana", "ana@example.com", "ana.pp@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreatePolicy(ctx, m.ID, "48201", 100); err == nil {
		t.Fatal("POST /api/policies must never accept 100")
	}
	cp, err := e.svc.CreatePolicy(ctx, m.ID, "48201", 15000)
	if err != nil || cp.Policy.Status != model.PolicyPendingVault || !strings.Contains(cp.ApproveURL, "approve") || cp.Policy.PremiumCents != 286 {
		t.Fatalf("%+v %v", cp, err)
	}
	st := e.fake.RequestsTo("POST", "/v3/vault/setup-tokens")
	if len(st) != 1 || st[0].Header.Get("PayPal-Request-Id") != "vst-"+cp.Policy.ID || !strings.Contains(string(st[0].Body), `"return_url":"https://api.example/api/paypal/vault/return?policy_id=`+cp.Policy.ID) {
		t.Fatalf("setup token request %s", st[0].Body)
	}
	b, _ := json.Marshal(cp)
	if strings.Contains(string(b), "vault") && strings.Contains(string(b), "token") {
		t.Fatalf("policy response leaks a vault token field: %s", b)
	}
	redirect := e.svc.VaultReturn(ctx, cp.Policy.ID)
	if redirect != "https://app.example/policies/"+cp.Policy.ID+"?enrolled=1" {
		t.Fatalf("redirect %s", redirect)
	}
	if pt := e.fake.RequestsTo("POST", "/v3/vault/payment-tokens"); len(pt) != 1 || pt[0].Header.Get("PayPal-Request-Id") != "vpt-"+cp.Policy.ID {
		t.Fatal("payment token call")
	}
	det, err := e.svc.GetPolicyDetail(ctx, cp.Policy.ID)
	if err != nil || det.Policy.Status != model.PolicyActive || len(det.PremiumCharges) != 1 || det.PremiumCharges[0].Status != model.ChargeCompleted || det.PremiumCharges[0].AmountCents != 286 {
		t.Fatalf("%+v %v", det, err)
	}
	// Returning again is harmless: no new token exchange, no new charge.
	if r := e.svc.VaultReturn(ctx, cp.Policy.ID); !strings.HasSuffix(r, "enrolled=1") || len(e.fake.RequestsTo("POST", "/v2/checkout/orders")) != 1 || len(e.fake.RequestsTo("POST", "/v3/vault/payment-tokens")) != 1 {
		t.Fatal("second return must be idempotent")
	}
	if r := e.svc.VaultReturn(ctx, "pol_nope"); !strings.Contains(r, "enrolled=0&error=policy_not_found") {
		t.Fatal(r)
	}
}

// --- B6: payouts -------------------------------------------------------------

func decide(t *testing.T, e env, p model.Policy, eventID string) service.RecordedClaim {
	t.Helper()
	ctx := context.Background()
	if err := e.svc.UpsertOutageEvent(ctx, model.OutageEvent{ID: eventID, FIPS: "48201", LoStartedAt: "2000-01-01T00:00:00Z", PeakShare: 0.5}); err != nil {
		t.Fatal(err)
	}
	rc, err := e.svc.RecordDecision(ctx, p.ID, rules.Decision{Rule: "R2", Tier: "t4", Decision: "paid", EventID: eventID, AmountCents: 30, Reason: "r"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return rc
}

func waitStatus(t *testing.T, e env, id, want string) model.Payout {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		p, err := e.svc.GetPayout(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if p.Status == want {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("payout %s: status %s, want %s", id, p.Status, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestOneClaimAndPayoutPerEventTier(t *testing.T) {
	e := newEnv(t, config.Config{})
	p := e.seed(t)
	a := decide(t, e, p, "evt_a")
	b := decide(t, e, p, "evt_a")
	if !a.Created || b.Created || a.Claim.ID != b.Claim.ID || b.Payout == nil || b.Payout.ID != a.Payout.ID {
		t.Fatalf("%+v / %+v", a, b)
	}
	if a.Payout.SenderBatchID != "clm-"+p.ID+"-evt_a-t4" {
		t.Fatal(a.Payout.SenderBatchID)
	}
	// The database refuses a second payout for the claim and a reused key.
	if _, err := e.db.Exec(`INSERT INTO payouts (id,claim_id,sender_batch_id,amount_cents,status,created_at) VALUES ('pay_x',?,'other',30,'created','x')`, a.Claim.ID); err == nil {
		t.Fatal("second payout for a claim accepted")
	}
}

func TestPayoutSentPolledConfirmedAndLedgerOnce(t *testing.T) {
	e := newEnv(t, config.Config{})
	p := e.seed(t)
	rc := decide(t, e, p, "evt_a")
	sent, err := e.svc.SendPayout(context.Background(), rc.Payout.ID)
	if err != nil || sent.Status != model.PayoutPending || sent.PayPalBatchID == nil {
		t.Fatalf("%+v %v", sent, err)
	}
	done := waitStatus(t, e, rc.Payout.ID, model.PayoutSuccess)
	if done.ConfirmedVia == nil || *done.ConfirmedVia != model.ViaPoll || done.PayPalItemID == nil || done.ConfirmedAt == nil {
		t.Fatalf("%+v", done)
	}
	// A second send is a no-op; a late poll changes nothing; one ledger row.
	if again, err := e.svc.SendPayout(context.Background(), rc.Payout.ID); err != nil || again.Status != model.PayoutSuccess {
		t.Fatal(again, err)
	}
	if fin, err := e.svc.PollPayout(context.Background(), rc.Payout.ID); err != nil || !fin {
		t.Fatal(fin, err)
	}
	led, _ := e.svc.Ledger(context.Background())
	if len(led.Entries) != 1 || led.Entries[0].AmountCents != -30 || led.Entries[0].Kind != "payout" {
		t.Fatalf("%+v", led)
	}
	if n := len(e.fake.RequestsTo("POST", "/v1/payments/payouts")); n != 1 {
		t.Fatalf("%d payout calls", n)
	}
}

// D7: a repeated sender_batch_id is not a failure. Simulate a crash after
// the P5 call by putting the row back to "created" with no batch ID.
func TestPayoutDuplicateBatchContinuesFromOriginal(t *testing.T) {
	e := newEnv(t, config.Config{})
	p := e.seed(t)
	rc := decide(t, e, p, "evt_a")
	first, err := e.svc.SendPayout(context.Background(), rc.Payout.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, rc.Payout.ID, model.PayoutSuccess)
	// A fresh claim on another event whose row "lost" its batch ID after the call.
	rc2 := decide(t, e, p, "evt_b")
	if _, err := e.svc.SendPayout(context.Background(), rc2.Payout.ID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, rc2.Payout.ID, model.PayoutSuccess)
	// Rewind rc2's row as if the process died before storing the response.
	if _, err := e.db.Exec(`UPDATE payouts SET status='created', paypal_batch_id=NULL, paypal_item_id=NULL, confirmed_at=NULL, confirmed_via=NULL WHERE id = ?`, rc2.Payout.ID); err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.SendPayout(context.Background(), rc2.Payout.ID)
	if err != nil || again.Status != model.PayoutPending || again.PayPalBatchID == nil || *again.PayPalBatchID == *first.PayPalBatchID {
		t.Fatalf("duplicate must continue from the original batch: %+v %v", again, err)
	}
	waitStatus(t, e, rc2.Payout.ID, model.PayoutSuccess)
	// Ledger: one row per payout even though rc2 was confirmed twice.
	led, _ := e.svc.Ledger(context.Background())
	if len(led.Entries) != 2 {
		t.Fatalf("%+v", led)
	}
}

func TestDuplicateBatchIDParsing(t *testing.T) {
	dup := &paypal.APIError{StatusCode: 400, Parsed: &paypal.ErrorResponse{Name: "USER_BUSINESS_ERROR", Details: []paypal.ErrorDetail{{Field: "SENDER_BATCH_ID", Issue: "Batch with given sender_batch_id already exists",
		Link: []paypal.Link{{Href: "https://api-m.sandbox.paypal.com/v1/payments/payouts/ABC123", Rel: "self"}}}}}}
	if id, ok := paypal.DuplicateBatchID(dup); !ok || id != "ABC123" {
		t.Fatal(id, ok)
	}
	other := *dup
	other.Parsed = &paypal.ErrorResponse{Name: "VALIDATION_ERROR", Details: dup.Parsed.Details}
	if _, ok := paypal.DuplicateBatchID(&other); ok {
		t.Fatal("another 400 is not a duplicate")
	}
	noLink := &paypal.APIError{StatusCode: 400, Parsed: &paypal.ErrorResponse{Name: "USER_BUSINESS_ERROR", Details: []paypal.ErrorDetail{{Field: "SENDER_BATCH_ID"}}}}
	if _, ok := paypal.DuplicateBatchID(noLink); ok {
		t.Fatal("no link: not readable")
	}
	if _, ok := paypal.DuplicateBatchID(errors.New("x")); ok {
		t.Fatal("plain error")
	}
}

func TestPayoutOther400Fails(t *testing.T) {
	e := newEnv(t, config.Config{})
	p := e.seed(t)
	rc := decide(t, e, p, "evt_a")
	e.fake.Lock()
	e.fake.PayoutFail = 400
	e.fake.Unlock()
	if _, err := e.svc.SendPayout(context.Background(), rc.Payout.ID); err == nil {
		t.Fatal("want error")
	}
	if po, _ := e.svc.GetPayout(context.Background(), rc.Payout.ID); po.Status != model.PayoutFailed {
		t.Fatalf("%+v", po)
	}
}

func TestPayoutItemStatusMapping(t *testing.T) {
	for item, want := range map[string]string{"UNCLAIMED": model.PayoutUnclaimed, "FAILED": model.PayoutFailed, "RETURNED": model.PayoutFailed} {
		e := newEnv(t, config.Config{})
		p := e.seed(t)
		e.fake.Lock()
		e.fake.ItemStatuses = []string{item}
		e.fake.Unlock()
		rc := decide(t, e, p, "evt_a")
		if _, err := e.svc.SendPayout(context.Background(), rc.Payout.ID); err != nil {
			t.Fatal(err)
		}
		po := waitStatus(t, e, rc.Payout.ID, want)
		if led, _ := e.svc.Ledger(context.Background()); len(led.Entries) != 0 || po.ConfirmedAt != nil {
			t.Fatalf("%s: no ledger row unless success", item)
		}
	}
}

// --- B6: webhooks --------------------------------------------------------------

func webhookHeaders() http.Header {
	h := http.Header{}
	h.Set("Paypal-Auth-Algo", "SHA256withRSA")
	h.Set("Paypal-Cert-Url", "https://api-m.sandbox.paypal.com/v1/notifications/certs/CERT-1")
	h.Set("Paypal-Transmission-Id", "tx-1")
	h.Set("Paypal-Transmission-Sig", "sig")
	h.Set("Paypal-Transmission-Time", "2026-11-11T19:00:00Z")
	return h
}

func itemEvent(id, claimID, batch, status string) string {
	return `{"id":"` + id + `","event_type":"PAYMENT.PAYOUTS-ITEM.SUCCEEDED","resource_type":"payouts_item","resource":{"payout_item_id":"ITEM1","transaction_status":"` + status +
		`","payout_batch_id":"` + batch + `","payout_item":{"recipient_type":"EMAIL","amount":{"currency":"USD","value":"0.30"},"receiver":"p@example.com","sender_item_id":"` + claimID + `"}}}`
}

func TestWebhookConfirmsPayoutOnceAndDedupes(t *testing.T) {
	e := newEnv(t, config.Config{PayPalWebhookID: "WH-ID"})
	p := e.seed(t)
	rc := decide(t, e, p, "evt_a")
	// Hold polling off by marking the row pending directly (no P5 call).
	if _, err := e.db.Exec(`UPDATE payouts SET status='pending', paypal_batch_id='B1' WHERE id = ?`, rc.Payout.ID); err != nil {
		t.Fatal(err)
	}
	sub := e.svc.Hub().Subscribe()
	defer e.svc.Hub().Unsubscribe(sub)
	raw := itemEvent("WH-1", rc.Claim.ID, "B1", "SUCCESS")
	if err := e.svc.HandleWebhook(context.Background(), webhookHeaders(), []byte(raw)); err != nil {
		t.Fatal(err)
	}
	po, _ := e.svc.GetPayout(context.Background(), rc.Payout.ID)
	if po.Status != model.PayoutSuccess || *po.ConfirmedVia != model.ViaWebhook || *po.PayPalItemID != "ITEM1" {
		t.Fatalf("%+v", po)
	}
	select {
	case m := <-sub.C:
		if m.Event != model.EvPayoutConfirmed {
			t.Fatalf("event %s", m.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("no payout_confirmed")
	}
	// Verification request carried the raw body unchanged and every header.
	vr := e.fake.RequestsTo("POST", "/v1/notifications/verify-webhook-signature")
	if len(vr) != 1 || !strings.Contains(string(vr[0].Body), `"webhook_event":`+raw) || !strings.Contains(string(vr[0].Body), `"webhook_id":"WH-ID"`) || !strings.Contains(string(vr[0].Body), `"transmission_id":"tx-1"`) {
		t.Fatalf("verify body %s", vr[0].Body)
	}
	// Same event again: skipped, not re-verified.
	if err := e.svc.HandleWebhook(context.Background(), webhookHeaders(), []byte(raw)); err != nil {
		t.Fatal(err)
	}
	if n := len(e.fake.RequestsTo("POST", "/v1/notifications/verify-webhook-signature")); n != 1 {
		t.Fatalf("%d verifications", n)
	}
	// A different event for the same payout: verified, changes nothing, no second ledger row.
	if err := e.svc.HandleWebhook(context.Background(), webhookHeaders(), []byte(itemEvent("WH-2", rc.Claim.ID, "B1", "SUCCESS"))); err != nil {
		t.Fatal(err)
	}
	if led, _ := e.svc.Ledger(context.Background()); len(led.Entries) != 1 {
		t.Fatalf("%+v", led)
	}
	var verified int
	if err := e.db.QueryRow(`SELECT verified FROM webhook_events WHERE id = 'WH-1'`).Scan(&verified); err != nil || verified != 1 {
		t.Fatal(verified, err)
	}
}

func TestWebhookUnverifiedChangesNothing(t *testing.T) {
	for name, setup := range map[string]func(e env){
		"verification FAILURE (simulator)": func(e env) { e.fake.Lock(); e.fake.VerifyStatus = "FAILURE"; e.fake.Unlock() },
		"verification refused 400":         func(e env) { e.fake.Lock(); e.fake.VerifyFail = 400; e.fake.Unlock() },
	} {
		e := newEnv(t, config.Config{PayPalWebhookID: "WH-ID"})
		setup(e)
		p := e.seed(t)
		rc := decide(t, e, p, "evt_a")
		if _, err := e.db.Exec(`UPDATE payouts SET status='pending', paypal_batch_id='B1' WHERE id = ?`, rc.Payout.ID); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.HandleWebhook(context.Background(), webhookHeaders(), []byte(itemEvent("WH-9", rc.Claim.ID, "B1", "SUCCESS"))); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		po, _ := e.svc.GetPayout(context.Background(), rc.Payout.ID)
		var verified int
		if err := e.db.QueryRow(`SELECT verified FROM webhook_events WHERE id = 'WH-9'`).Scan(&verified); err != nil || verified != 0 || po.Status != model.PayoutPending {
			t.Fatalf("%s: stored=%v verified=%d status=%s", name, err, verified, po.Status)
		}
	}
	// No PAYPAL_WEBHOOK_ID: stored unverified, P8 not called.
	e := newEnv(t, config.Config{})
	if err := e.svc.HandleWebhook(context.Background(), webhookHeaders(), []byte(itemEvent("WH-3", "clm_x", "B1", "SUCCESS"))); err != nil {
		t.Fatal(err)
	}
	if n := len(e.fake.RequestsTo("POST", "/v1/notifications/verify-webhook-signature")); n != 0 {
		t.Fatal("P8 called without a webhook ID")
	}
}

func TestWebhookVerifierDownIsRetried(t *testing.T) {
	e := newEnv(t, config.Config{PayPalWebhookID: "WH-ID"})
	e.fake.Lock()
	e.fake.VerifyFail = 503
	e.fake.Unlock()
	err := e.svc.HandleWebhook(context.Background(), webhookHeaders(), []byte(itemEvent("WH-5", "clm_x", "B1", "SUCCESS")))
	if err == nil {
		t.Fatal("want error so PayPal retries")
	}
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM webhook_events WHERE id = 'WH-5'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("row must be removed so the retry is processed")
	}
	if err := e.svc.HandleWebhook(context.Background(), webhookHeaders(), []byte(`{"nope":1}`)); svcErr(t, err).Status != 400 {
		t.Fatal(err)
	}
}

// --- B4: witness upload -------------------------------------------------------

type fixtureFile struct {
	PublicKeyJWK json.RawMessage   `json:"public_key_jwk"`
	Entries      []json.RawMessage `json:"entries"`
}

func loadS5(t *testing.T) fixtureFile {
	t.Helper()
	b, err := os.ReadFile("../witness/testdata/fixture-s5-mac-chrome.json")
	if err != nil {
		t.Fatal(err)
	}
	var f fixtureFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestWitnessUploadRealBrowserFixture(t *testing.T) {
	e := newEnv(t, config.Config{})
	p := e.seed(t)
	ctx := context.Background()
	f := loadS5(t)
	dev, err := e.svc.RegisterDevice(ctx, p.ID, f.PublicKeyJWK)
	if err != nil || dev.GenesisHash != witness.GenesisHash {
		t.Fatal(dev, err)
	}
	// Tampered copy of entry 3 first: rejected as bad_signature and stored.
	var m map[string]any
	if err := json.Unmarshal(f.Entries[0], &m); err != nil {
		t.Fatal(err)
	}
	m["battery_level"] = 0.3
	tampered, _ := json.Marshal(m)
	res, err := e.svc.AddEntries(ctx, dev.DeviceID, []json.RawMessage{tampered})
	if err != nil || res.Accepted != 0 || len(res.Rejected) != 1 || res.Rejected[0].Reason != "bad_signature" || res.LastSeq != 0 {
		t.Fatalf("tampered: %+v %v", res, err)
	}
	// All seven real entries, in one upload.
	res, err = e.svc.AddEntries(ctx, dev.DeviceID, f.Entries)
	if err != nil || res.Accepted != 7 || len(res.Rejected) != 0 || res.LastSeq != 7 {
		t.Fatalf("fixture: %+v %v", res, err)
	}
	// Resend of an accepted entry with the same hash: accepted silently, not stored again.
	res, err = e.svc.AddEntries(ctx, dev.DeviceID, f.Entries[2:3])
	if err != nil || res.Accepted != 1 || len(res.Rejected) != 0 || res.LastSeq != 7 {
		t.Fatalf("resend: %+v %v", res, err)
	}
	// Replayed seq with different content: bad_sequence.
	var m3 map[string]any
	_ = json.Unmarshal(f.Entries[2], &m3)
	m3["charging"] = false
	replayed, _ := json.Marshal(m3)
	res, err = e.svc.AddEntries(ctx, dev.DeviceID, []json.RawMessage{replayed})
	if err != nil || len(res.Rejected) != 1 || res.Rejected[0].Reason != "bad_sequence" || *res.Rejected[0].Seq != 3 {
		t.Fatalf("replayed seq: %+v %v", res, err)
	}
	// Malformed: not stored, reported.
	res, err = e.svc.AddEntries(ctx, dev.DeviceID, []json.RawMessage{json.RawMessage(`{"seq":8,"kind":5}`)})
	if err != nil || len(res.Rejected) != 1 || res.Rejected[0].Reason != "malformed" {
		t.Fatalf("malformed: %+v %v", res, err)
	}
	es, err := e.svc.ListEntries(ctx, dev.DeviceID, 200)
	if err != nil {
		t.Fatal(err)
	}
	valid, rejected := 0, 0
	for _, x := range es {
		if x.Valid {
			valid++
		} else {
			rejected++
		}
	}
	if valid != 7 || rejected != 2 {
		t.Fatalf("stored valid=%d rejected=%d (malformed is not stored)", valid, rejected)
	}
}

func TestWitnessChainOrderTimeAndStale(t *testing.T) {
	e := newEnv(t, config.Config{})
	p := e.seed(t)
	ctx := context.Background()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	dev, err := e.svc.RegisterDevice(ctx, p.ID, witness.JWKFor(&k.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	bl := 0.83
	mk := func(seq int64, at, prev string, online bool) (witness.Entry, json.RawMessage) {
		return witness.SignEntry(k, witness.Entry{Seq: seq, DeviceTime: at, Kind: "heartbeat", Charging: true, BatteryLevel: &bl, Online: online, PrevHash: prev})
	}
	up := func(raw json.RawMessage) service.UploadResult {
		t.Helper()
		r, err := e.svc.AddEntries(ctx, dev.DeviceID, []json.RawMessage{raw})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	e1, r1 := mk(1, "2026-11-11T18:00:00Z", witness.GenesisHash, true)
	if r := up(r1); r.Accepted != 1 {
		t.Fatalf("%+v", r)
	}
	_, broken := mk(2, "2026-11-11T18:01:00Z", strings.Repeat("ab", 32), true)
	if r := up(broken); r.Rejected[0].Reason != "broken_chain" {
		t.Fatalf("%+v", r)
	}
	_, skip := mk(3, "2026-11-11T18:01:00Z", e1.Hash, true)
	if r := up(skip); r.Rejected[0].Reason != "bad_sequence" {
		t.Fatalf("%+v", r)
	}
	_, back := mk(2, "2026-11-11T17:59:59Z", e1.Hash, true)
	if r := up(back); r.Rejected[0].Reason != "time_went_backwards" {
		t.Fatalf("%+v", r)
	}
	// now is 2026-11-11T19:00Z: 25 hours behind, written online: stale.
	_, stale := mk(2, "2026-11-11T18:00:00Z", e1.Hash, true)
	_ = stale
	e2, ok2 := mk(2, "2026-11-11T18:30:00Z", e1.Hash, true)
	if r := up(ok2); r.Accepted != 1 {
		t.Fatalf("%+v", r)
	}
	// Old entry from a fresh device: stale when online, accepted when written offline.
	k2, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	dev2, _ := e.svc.RegisterDevice(ctx, p.ID, witness.JWKFor(&k2.PublicKey))
	old, oldRaw := witness.SignEntry(k2, witness.Entry{Seq: 1, DeviceTime: "2026-11-10T17:00:00Z", Kind: "power_lost", BatteryLevel: nil, Online: true, PrevHash: witness.GenesisHash})
	_ = old
	r, _ := e.svc.AddEntries(ctx, dev2.DeviceID, []json.RawMessage{oldRaw})
	if len(r.Rejected) != 1 || r.Rejected[0].Reason != "stale" {
		t.Fatalf("stale: %+v", r)
	}
	_, offRaw := witness.SignEntry(k2, witness.Entry{Seq: 1, DeviceTime: "2026-11-10T17:00:00Z", Kind: "power_lost", Online: false, PrevHash: witness.GenesisHash})
	if r, _ := e.svc.AddEntries(ctx, dev2.DeviceID, []json.RawMessage{offRaw}); r.Accepted != 1 {
		t.Fatalf("offline-written old entry: %+v", r)
	}
	_ = e2
}
