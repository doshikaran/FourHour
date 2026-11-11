package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fourhour/spikes/paypal/paypaltest"
)

// These tests run S2 against paypaltest's HAND-WRITTEN responses. In
// particular, the duplicate sender_batch_id response is a guess at shape:
// the reference documents the behaviour but not the exact error name.

func envFile(t *testing.T, base string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	content := "PAYPAL_BASE_URL=" + base + "\nPAYPAL_CLIENT_ID=" + paypaltest.ClientID + "\nPAYPAL_CLIENT_SECRET=" + paypaltest.ClientSecret + "\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func fast(env string, extra ...string) []string {
	return append([]string{"-env-file", env, "-poll-interval", "1ms", "-poll-timeout", "2s"}, extra...)
}

func TestS2PassAgainstFake(t *testing.T) {
	f := paypaltest.New(t)
	var out, errOut bytes.Buffer
	code := run(context.Background(), fast(envFile(t, "https://api-m.sandbox.paypal.com"), "-sender-batch-id", "sb-test-1", "receiver@example.com"), &out, &errOut, deps{httpClient: f.HTTPClient()})
	s := out.String()
	if code != exitPass {
		t.Fatalf("exit %d\n%s\n%s", code, s, errOut.String())
	}
	for _, want := range []string{
		"payout_batch_id=5UXD2E8A7EBQJ batch_status=PENDING (HTTP 201)",
		"batch_status=PROCESSING",
		"final batch_status=SUCCESS",
		"transaction_status=SUCCESS",
		"[P5 repeat]",
		"HTTP status: 400",
		`"issue":"Batch with given sender_batch_id already exists"`,
		"RESULT: PASS payout_batch_id=5UXD2E8A7EBQJ",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q\n%s", want, s)
		}
	}
	if n := strings.Count(s, "paypal-debug-id"); n < len(f.Requests()) {
		t.Errorf("printed %d debug ids for %d requests", n, len(f.Requests()))
	}
	posts := f.RequestsTo("POST", "/v1/payments/payouts")
	if len(posts) != 2 || !bytes.Equal(posts[0].Body, posts[1].Body) {
		t.Fatalf("want two identical payout posts")
	}
	body := string(posts[0].Body)
	for _, want := range []string{`"sender_batch_id":"sb-test-1"`, `"recipient_wallet":"PAYPAL"`, `"recipient_type":"EMAIL"`, `"amount":{"currency":"USD","value":"1.00"}`, `"receiver":"receiver@example.com"`} {
		if !strings.Contains(body, want) {
			t.Errorf("payout body missing %s: %s", want, body)
		}
	}
	for _, sec := range []string{paypaltest.ClientSecret, paypaltest.AccessToken} {
		if strings.Contains(s+errOut.String(), sec) {
			t.Errorf("output leaks %q", sec)
		}
	}
}

func TestS2FailsOnDenied(t *testing.T) {
	f := paypaltest.New(t)
	f.BatchStatuses = []string{"DENIED"}
	var out bytes.Buffer
	code := run(context.Background(), fast(envFile(t, "https://api-m.sandbox.paypal.com"), "r@example.com"), &out, &out, deps{httpClient: f.HTTPClient()})
	if code != exitFail || !strings.Contains(out.String(), "RESULT: FAIL") {
		t.Errorf("exit %d\n%s", code, out.String())
	}
}

func TestS2FailsWhenStillPendingAtTimeout(t *testing.T) {
	f := paypaltest.New(t)
	f.BatchStatuses = []string{"PENDING"}
	var out bytes.Buffer
	args := []string{"-env-file", envFile(t, "https://api-m.sandbox.paypal.com"), "-poll-interval", "5ms", "-poll-timeout", "30ms", "r@example.com"}
	code := run(context.Background(), args, &out, &out, deps{httpClient: f.HTTPClient()})
	if code != exitFail || !strings.Contains(out.String(), "giving up") {
		t.Errorf("exit %d\n%s", code, out.String())
	}
}

func TestS2WarnsOnUnclaimedItem(t *testing.T) {
	f := paypaltest.New(t)
	f.ItemStatus = "UNCLAIMED"
	var out bytes.Buffer
	code := run(context.Background(), fast(envFile(t, "https://api-m.sandbox.paypal.com"), "r@example.com"), &out, &out, deps{httpClient: f.HTTPClient()})
	if code != exitPass || !strings.Contains(out.String(), "WARNING: batch is SUCCESS but an item is not SUCCESS") {
		t.Errorf("exit %d\n%s", code, out.String())
	}
}

func TestS2RefusesNonSandbox(t *testing.T) {
	f := paypaltest.New(t)
	var out, errOut bytes.Buffer
	code := run(context.Background(), fast(envFile(t, "https://api-m.paypal.com"), "r@example.com"), &out, &errOut, deps{httpClient: f.HTTPClient()})
	if code != exitConfig || !strings.Contains(errOut.String(), "refusing to run") || len(f.Requests()) != 0 {
		t.Errorf("exit %d stderr %s requests %d", code, errOut.String(), len(f.Requests()))
	}
}

func TestS2ArgErrors(t *testing.T) {
	env := envFile(t, "https://api-m.sandbox.paypal.com")
	for _, args := range [][]string{
		{"-env-file", env},
		{"-env-file", env, "not-an-email"},
		{"-env-file", env, "a@example.com", "b@example.com"},
		{"-env-file", filepath.Join(t.TempDir(), ".env"), "a@example.com"},
	} {
		var out, errOut bytes.Buffer
		if code := run(context.Background(), args, &out, &errOut, deps{}); code != exitConfig {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}
