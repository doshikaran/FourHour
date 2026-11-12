package paypal_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"fourhour/backend/internal/paypal"
	"fourhour/backend/internal/paypal/paypaltest"
)

// All responses in these tests are hand-written in paypaltest from PayPal's
// API reference. None was recorded from the sandbox.

func newTestClient(t *testing.T, f *paypaltest.Fake, onResp func(paypal.ResponseInfo)) *paypal.Client {
	t.Helper()
	c, err := paypal.NewClient(paypal.Config{
		BaseURL:      paypal.SandboxBaseURL,
		ClientID:     paypaltest.ClientID,
		ClientSecret: paypaltest.ClientSecret,
		HTTPClient:   f.HTTPClient(),
		OnResponse:   onResp,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestNewClientRefusesNonSandbox(t *testing.T) {
	for _, u := range []string{"", "https://api-m.paypal.com", "https://api-m.sandbox.paypal.com/", "http://api-m.sandbox.paypal.com", "http://localhost:8080"} {
		_, err := paypal.NewClient(paypal.Config{BaseURL: u, ClientID: "a", ClientSecret: "b"})
		if !errors.Is(err, paypal.ErrNotSandbox) {
			t.Errorf("base URL %q: want ErrNotSandbox, got %v", u, err)
		}
	}
	if _, err := paypal.NewClient(paypal.Config{BaseURL: paypal.SandboxBaseURL}); err == nil {
		t.Error("missing credentials: want error")
	}
}

func TestFormatCents(t *testing.T) {
	cases := map[int64]string{0: "0.00", 5: "0.05", 100: "1.00", 412: "4.12", 15000: "150.00", 30000: "300.00", 123456789: "1234567.89"}
	for in, want := range cases {
		got, err := paypal.FormatCents(in)
		if err != nil || got != want {
			t.Errorf("FormatCents(%d) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := paypal.FormatCents(-1); !errors.Is(err, paypal.ErrNegativeAmount) {
		t.Errorf("negative: got %v", err)
	}
}

func TestSetupTokenRequestShape(t *testing.T) {
	req := paypal.SetupTokenRequest{PaymentSource: paypal.SetupTokenPaymentSource{PayPal: &paypal.VaultPayPalWalletRequest{
		UsageType: paypal.UsageTypeMerchant,
		ExperienceContext: &paypal.VaultExperienceContext{
			ReturnURL: "http://localhost:8799/return",
			CancelURL: "http://localhost:8799/cancel",
		},
	}}}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"payment_source":{"paypal":{"usage_type":"MERCHANT","experience_context":{"return_url":"http://localhost:8799/return","cancel_url":"http://localhost:8799/cancel"}}}}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

func TestOrderAndPayoutRequestShapes(t *testing.T) {
	amt, _ := paypal.USD(100)
	o := paypal.OrderRequest{
		Intent:        paypal.IntentCapture,
		PurchaseUnits: []paypal.PurchaseUnitRequest{{Amount: amt}},
		PaymentSource: &paypal.OrderPaymentSource{PayPal: &paypal.OrderPayPalWallet{VaultID: "jwgvx42"}},
	}
	b, _ := json.Marshal(o)
	if want := `{"intent":"CAPTURE","purchase_units":[{"amount":{"currency_code":"USD","value":"1.00"}}],"payment_source":{"paypal":{"vault_id":"jwgvx42"}}}`; string(b) != want {
		t.Errorf("order got  %s\nwant %s", b, want)
	}

	pa, _ := paypal.PayoutUSD(100)
	p := paypal.CreatePayoutRequest{
		SenderBatchHeader: paypal.SenderBatchHeader{SenderBatchID: "clm-pol_x-evt_y-t4"},
		Items: []paypal.PayoutItemRequest{{
			RecipientType: paypal.RecipientTypeEmail, Amount: pa, Receiver: "r@example.com",
			SenderItemID: "clm_abc", RecipientWallet: paypal.RecipientWalletPayPal,
		}},
	}
	b, _ = json.Marshal(p)
	if want := `{"sender_batch_header":{"sender_batch_id":"clm-pol_x-evt_y-t4"},"items":[{"recipient_type":"EMAIL","amount":{"currency":"USD","value":"1.00"},"receiver":"r@example.com","sender_item_id":"clm_abc","recipient_wallet":"PAYPAL"}]}`; string(b) != want {
		t.Errorf("payout got  %s\nwant %s", b, want)
	}
}

func TestVaultAndOrderFlowAgainstFake(t *testing.T) {
	f := paypaltest.New(t)
	var infos []paypal.ResponseInfo
	c := newTestClient(t, f, func(ri paypal.ResponseInfo) { infos = append(infos, ri) })
	ctx := context.Background()

	st, res, err := c.CreateSetupToken(ctx, "vst-pol_1", paypal.SetupTokenRequest{PaymentSource: paypal.SetupTokenPaymentSource{PayPal: &paypal.VaultPayPalWalletRequest{UsageType: paypal.UsageTypeMerchant}}})
	if err != nil {
		t.Fatalf("setup token: %v", err)
	}
	if res.StatusCode != 201 || res.DebugID == "" || st.Status != paypal.VaultStatusPayerActionRequired {
		t.Errorf("setup token: res=%+v st=%+v", res, st)
	}
	if u, ok := st.ApproveURL(); !ok || !strings.Contains(u, "approval_session_id=") {
		t.Errorf("approve url: %q %v", u, ok)
	}

	pt, _, err := c.CreatePaymentToken(ctx, "vpt-pol_1", paypal.PaymentTokenRequest{PaymentSource: paypal.PaymentTokenPaymentSource{Token: &paypal.TokenIDRequest{ID: st.ID, Type: paypal.TokenTypeSetupToken}}})
	if err != nil || pt.ID != "jwgvx42" || pt.PaymentSource == nil || pt.PaymentSource.PayPal.PayerID != "AJM9JTWQJCFTA" {
		t.Fatalf("payment token: %+v %v", pt, err)
	}

	amt, _ := paypal.USD(100)
	oreq := paypal.OrderRequest{Intent: paypal.IntentCapture, PurchaseUnits: []paypal.PurchaseUnitRequest{{Amount: amt}}, PaymentSource: &paypal.OrderPaymentSource{PayPal: &paypal.OrderPayPalWallet{VaultID: pt.ID}}}
	o1, r1, err := c.CreateOrder(ctx, "prem-pol_1-2026-12", oreq)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	cap1, ok := o1.FirstCapture()
	if !ok || o1.Status != "COMPLETED" || cap1.Status != "COMPLETED" || cap1.Amount.Value != "1.00" {
		t.Errorf("order: %+v capture %+v", o1, cap1)
	}
	o2, r2, err := c.CreateOrder(ctx, "prem-pol_1-2026-12", oreq)
	if err != nil || o2.ID != o1.ID || r1.StatusCode != 201 || r2.StatusCode != 200 {
		t.Errorf("repeat: o1=%s o2=%s r1=%d r2=%d err=%v", o1.ID, o2.ID, r1.StatusCode, r2.StatusCode, err)
	}

	// Headers on the wire.
	orders := f.RequestsTo("POST", "/v2/checkout/orders")
	if len(orders) != 2 {
		t.Fatalf("want 2 order requests, got %d", len(orders))
	}
	for _, r := range orders {
		if r.Header.Get("PayPal-Request-Id") != "prem-pol_1-2026-12" || r.Header.Get("Prefer") != "return=representation" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("order headers: %v", r.Header)
		}
	}
	if got := f.RequestsTo("POST", "/v3/vault/setup-tokens")[0].Header.Get("PayPal-Request-Id"); got != "vst-pol_1" {
		t.Errorf("setup token request id: %q", got)
	}
	// Token fetched once and cached.
	if n := len(f.RequestsTo("POST", "/v1/oauth2/token")); n != 1 {
		t.Errorf("token fetched %d times, want 1", n)
	}
	tokReq := f.RequestsTo("POST", "/v1/oauth2/token")[0]
	if string(tokReq.Body) != "grant_type=client_credentials" || tokReq.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Errorf("token request: %q %v", tokReq.Body, tokReq.Header)
	}
	// Every response reported with a debug ID.
	if len(infos) != len(f.Requests()) {
		t.Errorf("OnResponse called %d times for %d requests", len(infos), len(f.Requests()))
	}
	for _, ri := range infos {
		if ri.DebugID == "" {
			t.Errorf("missing debug id: %+v", ri)
		}
	}
}

func TestAPIErrorCarriesBodyAndDebugID(t *testing.T) {
	f := paypaltest.New(t)
	f.FailSetupToken = true
	c := newTestClient(t, f, nil)
	_, _, err := c.CreateSetupToken(context.Background(), "vst-x", paypal.SetupTokenRequest{})
	ae, ok := paypal.AsAPIError(err)
	if !ok {
		t.Fatalf("want APIError, got %v", err)
	}
	if ae.StatusCode != 422 || ae.DebugID == "" || ae.Parsed == nil || ae.Parsed.Name != "UNPROCESSABLE_ENTITY" || ae.Parsed.Details[0].Issue != "FEATURE_NOT_ENABLED" {
		t.Errorf("APIError: %+v", ae)
	}
	if !strings.Contains(string(ae.Body), "FEATURE_NOT_ENABLED") || !strings.Contains(ae.Error(), ae.DebugID) {
		t.Errorf("body or message: %s / %s", ae.Body, ae.Error())
	}
}

func TestTokenFailureDoesNotLeakSecret(t *testing.T) {
	f := paypaltest.New(t)
	f.FailToken = true
	c := newTestClient(t, f, nil)
	_, _, err := c.GetPayoutBatch(context.Background(), "X")
	ae, ok := paypal.AsAPIError(err)
	if !ok || ae.StatusCode != 401 {
		t.Fatalf("want 401 APIError, got %v", err)
	}
	if strings.Contains(err.Error(), paypaltest.ClientSecret) || strings.Contains(string(ae.Body), paypaltest.ClientSecret) {
		t.Error("secret leaked in error")
	}
}

func TestAccessTokenResponseRedacts(t *testing.T) {
	tr := paypal.AccessTokenResponse{AccessToken: paypaltest.AccessToken}
	for _, s := range []string{tr.String(), tr.GoString()} {
		if strings.Contains(s, paypaltest.AccessToken) {
			t.Errorf("token leaked: %s", s)
		}
	}
}

func TestTokenRefreshAfterExpiry(t *testing.T) {
	f := paypaltest.New(t)
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	c, err := paypal.NewClient(paypal.Config{BaseURL: paypal.SandboxBaseURL, ClientID: paypaltest.ClientID, ClientSecret: paypaltest.ClientSecret, HTTPClient: f.HTTPClient(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, _, err := c.GetPayoutBatch(ctx, "B"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(31668*time.Second - 6*time.Minute) // still inside the margin
	if _, _, err := c.GetPayoutBatch(ctx, "B"); err != nil {
		t.Fatal(err)
	}
	if n := len(f.RequestsTo("POST", "/v1/oauth2/token")); n != 1 {
		t.Errorf("before margin: %d token calls", n)
	}
	now = now.Add(2 * time.Minute) // past expiry minus 5 minutes
	if _, _, err := c.GetPayoutBatch(ctx, "B"); err != nil {
		t.Fatal(err)
	}
	if n := len(f.RequestsTo("POST", "/v1/oauth2/token")); n != 2 {
		t.Errorf("after margin: %d token calls, want 2", n)
	}
}

func TestPayoutDuplicateReturnsAPIError(t *testing.T) {
	f := paypaltest.New(t)
	c := newTestClient(t, f, nil)
	pa, _ := paypal.PayoutUSD(100)
	req := paypal.CreatePayoutRequest{SenderBatchHeader: paypal.SenderBatchHeader{SenderBatchID: "sb-1"}, Items: []paypal.PayoutItemRequest{{RecipientType: "EMAIL", Amount: pa, Receiver: "r@example.com", RecipientWallet: "PAYPAL"}}}
	out, _, err := c.CreatePayout(context.Background(), req)
	if err != nil || out.BatchHeader.PayoutBatchID == "" || out.BatchHeader.BatchStatus != "PENDING" {
		t.Fatalf("first payout: %+v %v", out, err)
	}
	if h := f.RequestsTo("POST", "/v1/payments/payouts")[0].Header.Get("PayPal-Request-Id"); h != "" {
		t.Errorf("payout must not send PayPal-Request-Id (contract uses sender_batch_id), got %q", h)
	}
	_, _, err = c.CreatePayout(context.Background(), req)
	if ae, ok := paypal.AsAPIError(err); !ok || ae.StatusCode != 400 || len(ae.Body) == 0 {
		t.Errorf("duplicate: %v", err)
	}
	if _, _, err := c.CreatePayout(context.Background(), paypal.CreatePayoutRequest{}); err == nil {
		t.Error("empty sender_batch_id: want error")
	}
}

func TestPayoutBatchParse(t *testing.T) {
	f := paypaltest.New(t)
	c := newTestClient(t, f, nil)
	b, _, err := c.GetPayoutBatch(context.Background(), "5UXD2E8A7EBQJ")
	if err != nil {
		t.Fatal(err)
	}
	if b.BatchHeader.BatchStatus != "PENDING" || len(b.Items) != 1 || b.Items[0].TransactionStatus != "SUCCESS" || b.Items[0].PayoutItem.Amount.Value != "1.00" || b.BatchHeader.Amount.Currency != "USD" {
		t.Errorf("parsed: %+v", b)
	}
}
