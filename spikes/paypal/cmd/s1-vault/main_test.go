package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fourhour/spikes/paypal/paypaltest"
)

// These tests run S1 end to end against paypaltest's HAND-WRITTEN responses.
// They prove the program's flow and output, not PayPal's behaviour.

func envFile(t *testing.T, base string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	content := "PAYPAL_BASE_URL=" + base + "\nPAYPAL_CLIENT_ID=" + paypaltest.ClientID + "\nPAYPAL_CLIENT_SECRET=" + paypaltest.ClientSecret + "\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func browserHit(t *testing.T, path string) func(string, string, string) {
	return func(_, returnURL, cancelURL string) {
		u := returnURL + "?approval_token_id=4G4976650J0948357&approval_session_id=XYZ"
		if path == "cancel" {
			u = cancelURL
		}
		resp, err := http.Get(u)
		if err != nil {
			t.Errorf("browser hit: %v", err)
			return
		}
		resp.Body.Close()
	}
}

func assertNoSecrets(t *testing.T, out string) {
	t.Helper()
	for _, s := range []string{paypaltest.ClientSecret, paypaltest.AccessToken} {
		if strings.Contains(out, s) {
			t.Errorf("output leaks %q", s)
		}
	}
}

func TestS1PassAgainstFake(t *testing.T) {
	f := paypaltest.New(t)
	var out, errOut bytes.Buffer
	code := run(context.Background(), []string{"-env-file", envFile(t, "https://api-m.sandbox.paypal.com"), "-port", "0"}, &out, &errOut,
		deps{httpClient: f.HTTPClient(), onApproveURL: browserHit(t, "return")})
	s := out.String()
	if code != exitPass {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, s, errOut.String())
	}
	for _, want := range []string{
		"https://sandbox.paypal.com/agreements/approve?approval_session_id=",
		"approval_token_id=[4G4976650J0948357]",
		"payment token (vault_id)=jwgvx42",
		"capture id=3B017991HX624902V status=COMPLETED",
		"IDEMPOTENCY: PayPal returned the ORIGINAL order",
		"RESULT: PASS order_id=",
		"paypal-debug-id=f000000000001",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q\n%s", want, s)
		}
	}
	// Every response's debug id is printed: one line per request.
	if n := strings.Count(s, "paypal-debug-id="); n != len(f.Requests()) {
		t.Errorf("printed %d debug ids for %d requests", n, len(f.Requests()))
	}
	// Both order calls used one key; vault calls used theirs.
	orders := f.RequestsTo("POST", "/v2/checkout/orders")
	if len(orders) != 2 || orders[0].Header.Get("PayPal-Request-Id") != orders[1].Header.Get("PayPal-Request-Id") || !strings.HasPrefix(orders[0].Header.Get("PayPal-Request-Id"), "spike-prem-") {
		t.Errorf("order keys wrong")
	}
	if !strings.Contains(string(orders[0].Body), `"vault_id":"jwgvx42"`) || !strings.Contains(string(orders[0].Body), `"value":"1.00"`) {
		t.Errorf("order body: %s", orders[0].Body)
	}
	st := f.RequestsTo("POST", "/v3/vault/setup-tokens")[0]
	if !strings.Contains(string(st.Body), `"usage_type":"MERCHANT"`) || !strings.Contains(string(st.Body), `"return_url":"http://localhost:`) {
		t.Errorf("setup body: %s", st.Body)
	}
	assertNoSecrets(t, s+errOut.String())
}

func TestS1FailsWhenCaptureNotCompleted(t *testing.T) {
	f := paypaltest.New(t)
	f.CaptureStatus = "PENDING"
	var out bytes.Buffer
	code := run(context.Background(), []string{"-env-file", envFile(t, "https://api-m.sandbox.paypal.com"), "-port", "0"}, &out, &out,
		deps{httpClient: f.HTTPClient(), onApproveURL: browserHit(t, "return")})
	if code != exitFail || !strings.Contains(out.String(), "RESULT: FAIL") || strings.Contains(out.String(), "RESULT: PASS") {
		t.Errorf("exit %d\n%s", code, out.String())
	}
}

func TestS1Cancel(t *testing.T) {
	f := paypaltest.New(t)
	var out bytes.Buffer
	code := run(context.Background(), []string{"-env-file", envFile(t, "https://api-m.sandbox.paypal.com"), "-port", "0"}, &out, &out,
		deps{httpClient: f.HTTPClient(), onApproveURL: browserHit(t, "cancel")})
	if code != exitFail || !strings.Contains(out.String(), "buyer cancelled") {
		t.Errorf("exit %d\n%s", code, out.String())
	}
	if n := len(f.RequestsTo("POST", "/v2/checkout/orders")); n != 0 {
		t.Errorf("cancel must not charge; %d order calls", n)
	}
}

func TestS1PrintsFullErrorBody(t *testing.T) {
	f := paypaltest.New(t)
	f.FailSetupToken = true
	var out bytes.Buffer
	code := run(context.Background(), []string{"-env-file", envFile(t, "https://api-m.sandbox.paypal.com"), "-port", "0"}, &out, &out, deps{httpClient: f.HTTPClient()})
	s := out.String()
	if code != exitFail || !strings.Contains(s, `"issue":"FEATURE_NOT_ENABLED"`) || !strings.Contains(s, "paypal-debug-id: f") {
		t.Errorf("exit %d\n%s", code, s)
	}
	assertNoSecrets(t, s)
}

func TestS1RefusesNonSandboxWithoutNetwork(t *testing.T) {
	f := paypaltest.New(t)
	var out, errOut bytes.Buffer
	code := run(context.Background(), []string{"-env-file", envFile(t, "https://api-m.paypal.com"), "-port", "0"}, &out, &errOut, deps{httpClient: f.HTTPClient()})
	if code != exitConfig || !strings.Contains(errOut.String(), "refusing to run: PAYPAL_BASE_URL must be exactly https://api-m.sandbox.paypal.com") {
		t.Errorf("exit %d stderr %s", code, errOut.String())
	}
	if len(f.Requests()) != 0 {
		t.Errorf("made %d requests", len(f.Requests()))
	}
}

func TestS1MissingEnvFile(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run(context.Background(), []string{"-env-file", filepath.Join(t.TempDir(), ".env")}, &out, &errOut, deps{})
	if code != exitConfig || !strings.Contains(errOut.String(), "no .env file at") {
		t.Errorf("exit %d stderr %s", code, errOut.String())
	}
}
