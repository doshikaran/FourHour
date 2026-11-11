// Package paypaltest provides a fake PayPal server for tests.
//
// Every response it serves is HAND-WRITTEN from the request/response samples
// and schemas in PayPal's published API reference (developer.paypal.com,
// read 2026-11-11). None of them is a recorded sandbox response. Where PayPal
// does not document a behaviour (for example, what a repeated order create with
// the same PayPal-Request-Id returns), the fake's behaviour is a stated
// assumption that the spikes exist to test.
package paypaltest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// Secrets used by the fake. Tests assert these never appear in output.
const (
	ClientID     = "test-client-id"
	ClientSecret = "test-client-secret-DO-NOT-PRINT"
	AccessToken  = "A21AA-test-access-token-DO-NOT-PRINT"
)

// RewriteTransport sends every request to Target instead of the URL's host,
// so a client configured with the sandbox base URL can be tested locally
// without weakening its sandbox check.
type RewriteTransport struct {
	Target *url.URL
	Base   http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t RewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.URL.Scheme = t.Target.Scheme
	r2.URL.Host = t.Target.Host
	r2.Host = t.Target.Host
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r2)
}

// Request is one request the fake received.
type Request struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

// Fake is a scripted PayPal sandbox stand-in.
type Fake struct {
	Server *httptest.Server

	mu       sync.Mutex
	requests []Request
	orders   map[string]string // PayPal-Request-Id -> order ID
	batches  map[string]string // sender_batch_id -> payout_batch_id
	gets     int

	// OrderStatus and CaptureStatus are returned by order create.
	OrderStatus   string
	CaptureStatus string
	// BatchStatuses is the sequence of batch_status values returned by
	// successive GET payout calls; the last one repeats.
	BatchStatuses []string
	// ItemStatus is the item transaction_status in GET payout responses.
	ItemStatus string
	// FailSetupToken makes setup-token creation return a 422 error.
	FailSetupToken bool
	// FailToken makes the OAuth call fail with 401.
	FailToken bool
}

// New starts a Fake. It is closed when the test ends.
func New(t testing.TB) *Fake {
	t.Helper()
	f := &Fake{
		orders:        map[string]string{},
		batches:       map[string]string{},
		OrderStatus:   "COMPLETED",
		CaptureStatus: "COMPLETED",
		BatchStatuses: []string{"PENDING", "PROCESSING", "SUCCESS"},
		ItemStatus:    "SUCCESS",
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.Server.Close)
	return f
}

// HTTPClient returns an http.Client that routes to the fake.
func (f *Fake) HTTPClient() *http.Client {
	u, _ := url.Parse(f.Server.URL) // httptest URLs always parse
	return &http.Client{Transport: RewriteTransport{Target: u}}
}

// Requests returns a copy of the requests received so far.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// RequestsTo returns the received requests matching method and path.
func (f *Fake) RequestsTo(method, path string) []Request {
	var out []Request
	for _, r := range f.Requests() {
		if r.Method == method && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

func (f *Fake) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, Request{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
	n := len(f.requests)
	f.mu.Unlock()
	debugID := fmt.Sprintf("f%012d", n)
	w.Header().Set("Paypal-Debug-Id", debugID)
	w.Header().Set("Content-Type", "application/json")

	if r.URL.Path == "/v1/oauth2/token" {
		f.token(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+AccessToken {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":"invalid_token","error_description":"Token signature verification failed"}`)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v3/vault/setup-tokens":
		f.setupToken(w, debugID)
	case r.Method == http.MethodPost && r.URL.Path == "/v3/vault/payment-tokens":
		f.paymentToken(w, body)
	case r.Method == http.MethodPost && r.URL.Path == "/v2/checkout/orders":
		f.order(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/payments/payouts":
		f.createPayout(w, body, debugID)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/payments/payouts/"):
		f.getPayout(w, strings.TrimPrefix(r.URL.Path, "/v1/payments/payouts/"))
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"name":"RESOURCE_NOT_FOUND","message":"not in fake","debug_id":%q}`, debugID)
	}
}

func (f *Fake) token(w http.ResponseWriter, r *http.Request) {
	id, secret, ok := r.BasicAuth()
	if f.FailToken || !ok || id != ClientID || secret != ClientSecret {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid_client","error_description":"Client Authentication failed"}`)
		return
	}
	// Shape from developer.paypal.com/api/rest/authentication/ sample.
	fmt.Fprintf(w, `{"scope":"https://uri.paypal.com/services/payments/payment","access_token":%q,"token_type":"Bearer","app_id":"APP-80W284485P519543T","expires_in":31668,"nonce":"2026-11-11T00:00:00Zabc"}`, AccessToken)
}

func (f *Fake) setupToken(w http.ResponseWriter, debugID string) {
	if f.FailSetupToken {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprintf(w, `{"name":"UNPROCESSABLE_ENTITY","details":[{"issue":"FEATURE_NOT_ENABLED","description":"Vaulting is not enabled for this merchant."}],"message":"The requested action could not be performed, semantically incorrect, or failed business validation.","debug_id":%q}`, debugID)
		return
	}
	// Shape from the "save without purchase" PayPal guide sample.
	w.WriteHeader(http.StatusCreated)
	fmt.Fprint(w, `{"id":"4G4976650J0948357","customer":{"id":"customer_4029352051"},"status":"PAYER_ACTION_REQUIRED","payment_source":{"paypal":{"description":"FourHour spike","usage_pattern":"IMMEDIATE","usage_type":"MERCHANT","customer_type":"CONSUMER"}},"links":[{"href":"https://api-m.sandbox.paypal.com/v3/vault/setup-tokens/4G4976650J0948357","rel":"self","method":"GET"},{"href":"https://sandbox.paypal.com/agreements/approve?approval_session_id=4G4976650J0948357","rel":"approve","method":"GET"}]}`)
}

func (f *Fake) paymentToken(w http.ResponseWriter, body []byte) {
	var req struct {
		PaymentSource struct {
			Token struct{ ID, Type string } `json:"token"`
		} `json:"payment_source"`
	}
	if json.Unmarshal(body, &req) != nil || req.PaymentSource.Token.ID == "" || req.PaymentSource.Token.Type != "SETUP_TOKEN" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"name":"INVALID_REQUEST","message":"bad token","debug_id":"x"}`)
		return
	}
	w.WriteHeader(http.StatusCreated)
	fmt.Fprint(w, `{"id":"jwgvx42","customer":{"id":"customer_4029352051"},"payment_source":{"paypal":{"description":"FourHour spike","usage_pattern":"IMMEDIATE","shipping":{},"permit_multiple_payment_tokens":false,"usage_type":"MERCHANT","customer_type":"CONSUMER","email_address":"buyer@example.com","payer_id":"AJM9JTWQJCFTA"}},"links":[{"rel":"self","href":"https://api-m.sandbox.paypal.com/v3/vault/payment-tokens/jwgvx42","method":"GET"},{"rel":"delete","href":"https://api-m.sandbox.paypal.com/v3/vault/payment-tokens/jwgvx42","method":"DELETE"}]}`)
}

// order assumes a repeated PayPal-Request-Id returns the original order with
// HTTP 200 (the Orders reference says 200 is returned "to an idempotent
// request"). Spike S1 checks this against the real sandbox.
func (f *Fake) order(w http.ResponseWriter, r *http.Request) {
	rid := r.Header.Get("PayPal-Request-Id")
	f.mu.Lock()
	id, seen := f.orders[rid]
	if !seen {
		id = fmt.Sprintf("4TH21426N0569294%d", len(f.orders))
		if rid != "" {
			f.orders[rid] = id
		}
	}
	f.mu.Unlock()
	if seen {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
	fmt.Fprintf(w, `{"id":%q,"status":%q,"payment_source":{"paypal":{"email_address":"buyer@example.com","account_id":"AJM9JTWQJCFTA","account_status":"VERIFIED"}},"purchase_units":[{"reference_id":"default","payments":{"captures":[{"id":"3B017991HX624902V","status":%q,"amount":{"currency_code":"USD","value":"1.00"},"final_capture":true,"create_time":"2026-11-11T10:00:00Z","update_time":"2026-11-11T10:00:00Z"}]}}],"links":[{"href":"https://api-m.sandbox.paypal.com/v2/checkout/orders/%s","rel":"self","method":"GET"}]}`, id, f.OrderStatus, f.CaptureStatus, id)
}

func (f *Fake) createPayout(w http.ResponseWriter, body []byte, debugID string) {
	var req struct {
		SenderBatchHeader struct {
			SenderBatchID string `json:"sender_batch_id"`
		} `json:"sender_batch_header"`
	}
	if json.Unmarshal(body, &req) != nil || req.SenderBatchHeader.SenderBatchID == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"name":"VALIDATION_ERROR","message":"Invalid request - see details","debug_id":%q}`, debugID)
		return
	}
	sbid := req.SenderBatchHeader.SenderBatchID
	f.mu.Lock()
	existing, dup := f.batches[sbid]
	if !dup {
		f.batches[sbid] = "5UXD2E8A7EBQJ"
	}
	f.mu.Unlock()
	if dup {
		// The reference says a reused sender_batch_id is rejected with an error
		// naming it and a link to the original batch. The exact name and status
		// code are not documented; these are placeholders for the fake only.
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"name":"USER_BUSINESS_ERROR","message":"User business error.","debug_id":%q,"information_link":"https://developer.paypal.com/docs/api/payments.payouts-batch/#errors","details":[{"field":"SENDER_BATCH_ID","location":"body","issue":"Batch with given sender_batch_id already exists","link":[{"href":"https://api-m.sandbox.paypal.com/v1/payments/payouts/%s","rel":"self","method":"GET"}]}]}`, debugID, existing)
		return
	}
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, `{"batch_header":{"sender_batch_header":{"sender_batch_id":%q,"email_subject":"You have a payout!"},"payout_batch_id":"5UXD2E8A7EBQJ","batch_status":"PENDING"},"links":[{"href":"https://api-m.sandbox.paypal.com/v1/payments/payouts/5UXD2E8A7EBQJ","rel":"self","method":"GET","encType":"application/json"}]}`, sbid)
}

func (f *Fake) getPayout(w http.ResponseWriter, batchID string) {
	f.mu.Lock()
	i := f.gets
	f.gets++
	f.mu.Unlock()
	status := f.BatchStatuses[len(f.BatchStatuses)-1]
	if i < len(f.BatchStatuses) {
		status = f.BatchStatuses[i]
	}
	fmt.Fprintf(w, `{"batch_header":{"payout_batch_id":%q,"batch_status":%q,"time_created":"2026-11-11T10:00:00Z","time_completed":"2026-11-11T10:00:05Z","sender_batch_header":{"sender_batch_id":"x","email_subject":"You have a payout!"},"funding_source":"BALANCE","amount":{"currency":"USD","value":"1.00"},"fees":{"currency":"USD","value":"0.00"}},"items":[{"payout_item_id":"8AELMXH8UB2P8","transaction_id":"0C413693MN970190K","activity_id":"0E158638XS0329106","transaction_status":%q,"payout_item_fee":{"currency":"USD","value":"0.00"},"payout_batch_id":%q,"payout_item":{"recipient_type":"EMAIL","amount":{"currency":"USD","value":"1.00"},"note":"FourHour spike","receiver":"receiver@example.com","sender_item_id":"spike-item-1","recipient_wallet":"PAYPAL"},"time_processed":"2026-11-11T10:00:05Z","links":[]}],"links":[]}`, batchID, status, f.ItemStatus, batchID)
}
