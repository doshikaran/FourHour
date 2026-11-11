// Command s1-vault is spike S1: save a PayPal wallet (vault) and charge it
// with a merchant-initiated $1.00 order, with no buyer present for the charge.
//
// Steps: P1 token, P2 setup token, buyer approves in a browser, P3 payment
// token, P4 order with vault_id, then P4 again with the same PayPal-Request-Id.
// It prints PASS only when the first order and its capture are both COMPLETED.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	"fourhour/spikes/paypal/internal/spikeenv"
	"fourhour/spikes/paypal/paypal"
)

const chargeCents int64 = 100 // $1.00

// Exit codes.
const (
	exitPass   = 0
	exitFail   = 1
	exitConfig = 2
)

// deps lets tests replace the network and drive the browser step.
type deps struct {
	httpClient *http.Client
	// onApproveURL, if set, is called (in a goroutine) once the approval URL
	// is printed, with the local return and cancel URLs.
	onApproveURL func(approveURL, returnURL, cancelURL string)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, deps{})
	stop()
	os.Exit(code)
}

type callback struct {
	kind  string // "return" or "cancel"
	query map[string][]string
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	fs := flag.NewFlagSet("s1-vault", flag.ContinueOnError)
	fs.SetOutput(stderr)
	envFile := fs.String("env-file", "", "path to .env (default: .env at the repo root, found by walking up to CONTRACTS.md)")
	port := fs.Int("port", 8799, "localhost port for the PayPal return/cancel listener (0 picks a free port)")
	wait := fs.Duration("wait", 10*time.Minute, "how long to wait for the browser to come back from PayPal")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}

	cfg, err := loadConfig(*envFile)
	if err != nil {
		fmt.Fprintf(stderr, "s1-vault: %v\n", err)
		return exitConfig
	}
	p := func(format string, a ...any) { fmt.Fprintf(stdout, format+"\n", a...) }
	p("S1 vault + merchant-initiated charge. Sandbox: %s. Env file: %s", cfg.BaseURL, cfg.EnvFile)

	client, err := paypal.NewClient(paypal.Config{
		BaseURL: cfg.BaseURL, ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret,
		HTTPClient: d.httpClient,
		OnResponse: func(ri paypal.ResponseInfo) {
			p("  <- %s %s HTTP %d paypal-debug-id=%s", ri.Method, ri.Path, ri.StatusCode, orNone(ri.DebugID))
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "s1-vault: %v\n", err)
		return exitConfig
	}

	fail := func(step string, err error) int {
		p("ERROR at %s: %v", step, err)
		if ae, ok := paypal.AsAPIError(err); ok {
			p("  HTTP status: %d", ae.StatusCode)
			p("  paypal-debug-id: %s", orNone(ae.DebugID))
			p("  full error body:\n%s", string(ae.Body))
		}
		p("RESULT: FAIL (%s)", step)
		return exitFail
	}

	// Listener first, so a busy port fails before anything is created at PayPal.
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		fmt.Fprintf(stderr, "s1-vault: cannot listen on 127.0.0.1:%d: %v (use -port)\n", *port, err)
		return exitConfig
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port
	returnURL := fmt.Sprintf("http://localhost:%d/return", actualPort)
	cancelURL := fmt.Sprintf("http://localhost:%d/cancel", actualPort)
	cbCh := make(chan callback, 1)
	srv := &http.Server{Handler: callbackHandler(cbCh), ReadHeaderTimeout: 10 * time.Second}
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "s1-vault: listener: %v\n", err)
		}
	}()
	shutdown := func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			fmt.Fprintf(stderr, "s1-vault: listener shutdown: %v\n", err)
		}
		<-srvDone
	}
	defer shutdown()
	p("Listening for PayPal redirects on %s and %s", returnURL, cancelURL)

	runID := newRunID()
	p("Run ID %s (used in every PayPal-Request-Id below)", runID)

	// P2: setup token.
	vstID := "spike-vst-" + runID
	p("\n[P2] POST /v3/vault/setup-tokens  PayPal-Request-Id: %s", vstID)
	st, _, err := client.CreateSetupToken(ctx, vstID, paypal.SetupTokenRequest{
		PaymentSource: paypal.SetupTokenPaymentSource{PayPal: &paypal.VaultPayPalWalletRequest{
			UsageType:         paypal.UsageTypeMerchant,
			ExperienceContext: &paypal.VaultExperienceContext{ReturnURL: returnURL, CancelURL: cancelURL},
		}},
	})
	if err != nil {
		return fail("create setup token", err)
	}
	p("  setup token id=%s status=%s", st.ID, st.Status)
	approveURL, ok := st.ApproveURL()
	if !ok {
		return fail("create setup token", fmt.Errorf("response has no link with rel %q; links: %+v", paypal.LinkRelApprove, st.Links))
	}

	p("\n================================================================")
	p("Open this URL in a browser and approve with a SANDBOX PERSONAL account:")
	p("")
	p("  %s", approveURL)
	p("")
	p("Waiting up to %s for PayPal to redirect back to localhost:%d ...", *wait, actualPort)
	p("================================================================")
	if d.onApproveURL != nil {
		go d.onApproveURL(approveURL, returnURL, cancelURL)
	}

	var cb callback
	select {
	case cb = <-cbCh:
	case <-time.After(*wait):
		return fail("wait for approval", fmt.Errorf("no redirect within %s", *wait))
	case <-ctx.Done():
		return fail("wait for approval", ctx.Err())
	}
	p("\nBrowser came back on /%s with query parameters:", cb.kind)
	printQuery(p, cb.query)
	if cb.kind == "cancel" {
		p("RESULT: FAIL (buyer cancelled at PayPal)")
		return exitFail
	}

	// P3: payment token.
	vptID := "spike-vpt-" + runID
	p("\n[P3] POST /v3/vault/payment-tokens  PayPal-Request-Id: %s", vptID)
	pt, _, err := client.CreatePaymentToken(ctx, vptID, paypal.PaymentTokenRequest{
		PaymentSource: paypal.PaymentTokenPaymentSource{Token: &paypal.TokenIDRequest{ID: st.ID, Type: paypal.TokenTypeSetupToken}},
	})
	if err != nil {
		return fail("create payment token", err)
	}
	customerID := ""
	if pt.Customer != nil {
		customerID = pt.Customer.ID
	}
	p("  payment token (vault_id)=%s customer.id=%s", pt.ID, orNone(customerID))
	if pt.PaymentSource != nil && pt.PaymentSource.PayPal != nil {
		p("  payer_id=%s email=%s", orNone(pt.PaymentSource.PayPal.PayerID), orNone(pt.PaymentSource.PayPal.EmailAddress))
	}
	if pt.ID == "" {
		return fail("create payment token", errors.New("response has no id"))
	}

	// P4: merchant-initiated order. From here on no buyer interaction happens.
	amount, err := paypal.USD(chargeCents)
	if err != nil {
		return fail("build order", err)
	}
	orderReq := paypal.OrderRequest{
		Intent:        paypal.IntentCapture,
		PurchaseUnits: []paypal.PurchaseUnitRequest{{Amount: amount}},
		PaymentSource: &paypal.OrderPaymentSource{PayPal: &paypal.OrderPayPalWallet{VaultID: pt.ID}},
	}
	premID := "spike-prem-" + runID
	p("\n[P4] POST /v2/checkout/orders  PayPal-Request-Id: %s  amount %s %s  vault_id %s", premID, amount.Value, amount.CurrencyCode, pt.ID)
	order, res1, err := client.CreateOrder(ctx, premID, orderReq)
	if err != nil {
		return fail("create order", err)
	}
	capture, hasCapture := order.FirstCapture()
	p("  order id=%s status=%s (HTTP %d)", order.ID, order.Status, res1.StatusCode)
	if hasCapture {
		reason := ""
		if capture.StatusDetails != nil && capture.StatusDetails.Reason != "" {
			reason = " reason=" + capture.StatusDetails.Reason
		}
		p("  capture id=%s status=%s%s", capture.ID, capture.Status, reason)
	} else {
		p("  capture: none in response")
	}

	// P4 again, same key.
	p("\n[P4 repeat] POST /v2/checkout/orders  same PayPal-Request-Id: %s", premID)
	order2, res2, err2 := client.CreateOrder(ctx, premID, orderReq)
	switch {
	case err2 != nil:
		p("  repeat call returned an error: %v", err2)
		if ae, ok := paypal.AsAPIError(err2); ok {
			p("  full error body:\n%s", string(ae.Body))
		}
		p("IDEMPOTENCY: UNKNOWN (repeat call failed; see above)")
	case order2.ID == order.ID:
		c2, _ := order2.FirstCapture()
		p("  order id=%s status=%s capture id=%s (HTTP %d)", order2.ID, order2.Status, orNone(c2.ID), res2.StatusCode)
		p("IDEMPOTENCY: PayPal returned the ORIGINAL order %s (HTTP %d); no second order was created", order.ID, res2.StatusCode)
	default:
		p("  order id=%s status=%s (HTTP %d)", order2.ID, order2.Status, res2.StatusCode)
		p("IDEMPOTENCY: WARNING PayPal created a SECOND order %s (first was %s) for the same PayPal-Request-Id", order2.ID, order.ID)
	}

	if order.Status == paypal.OrderStatusCompleted && hasCapture && capture.Status == paypal.CaptureStatusCompleted {
		p("\nRESULT: PASS order_id=%s capture_id=%s (order COMPLETED, capture COMPLETED, no buyer present for the charge)", order.ID, capture.ID)
		return exitPass
	}
	p("\nRESULT: FAIL order status=%s capture status=%s (PASS needs both COMPLETED)", order.Status, orNone(capture.Status))
	return exitFail
}

func loadConfig(envFile string) (spikeenv.Config, error) {
	if envFile == "" {
		wd, err := os.Getwd()
		if err != nil {
			return spikeenv.Config{}, fmt.Errorf("working directory: %w", err)
		}
		envFile, err = spikeenv.FindEnvFile(wd)
		if err != nil {
			return spikeenv.Config{}, err
		}
	}
	return spikeenv.Load(envFile)
}

// callbackHandler serves /return and /cancel. Only the first hit is delivered.
func callbackHandler(ch chan<- callback) http.Handler {
	var once sync.Once
	deliver := func(kind string, r *http.Request) {
		once.Do(func() { ch <- callback{kind: kind, query: r.URL.Query()} })
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /return", func(w http.ResponseWriter, r *http.Request) {
		deliver("return", r)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!doctype html><title>S1</title><p>Approved. Return to the terminal; S1 is now charging the saved wallet with no buyer present.</p>")
	})
	mux.HandleFunc("GET /cancel", func(w http.ResponseWriter, r *http.Request) {
		deliver("cancel", r)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><title>S1</title><p>%s</p>", html.EscapeString("Cancelled at PayPal. S1 will report FAIL."))
	})
	return mux
}

func printQuery(p func(string, ...any), q map[string][]string) {
	if len(q) == 0 {
		p("  (none)")
		return
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p("  %s=%v", k, q[k])
	}
}

func newRunID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is not recoverable in a meaningful way; fall back to time.
		return time.Now().UTC().Format("20060102T150405")
	}
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b)
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
