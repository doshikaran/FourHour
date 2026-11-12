// Package paypaltest provides a fake PayPal server for tests.
//
// Every response it serves is HAND-WRITTEN from the request/response samples
// and schemas in PayPal's published API reference (developer.paypal.com).
// None is a recorded sandbox response. Two behaviours follow what spikes S1
// and S2 observed in the real sandbox (CONTRACTS.md section 7):
//   - a repeated order create with the same PayPal-Request-Id returns the
//     original order with HTTP 200;
//   - a repeated sender_batch_id returns HTTP 400 USER_BUSINESS_ERROR with
//     details[0].field SENDER_BATCH_ID and a link to the original batch.
//     The body shape here is written from the S2 description, not copied
//     from a recording.
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

type batch struct {
	id, senderBatchID, itemID, receiver, senderItemID, value string
	gets                                                     int
}

// Fake is a scripted PayPal sandbox stand-in. Exported fields may be set
// before requests are made, or changed later under Lock/Unlock.
type Fake struct {
	Server *httptest.Server

	mu        sync.Mutex
	requests  []Request
	orders    map[string]string // PayPal-Request-Id -> order ID
	orderBody map[string]string // order ID -> response body
	batches   map[string]*batch // sender_batch_id -> batch
	byBatchID map[string]*batch // payout_batch_id -> batch
	refunds   map[string]string // PayPal-Request-Id -> refund ID

	// OrderStatus and CaptureStatus are returned by order create.
	OrderStatus   string
	CaptureStatus string
	// OrderFail, if not zero, makes order create return this HTTP status
	// with a PayPal error body.
	OrderFail int
	// BatchStatuses is the sequence of batch_status values returned by
	// successive GETs of one batch; the last one repeats.
	BatchStatuses []string
	// ItemStatuses is the matching sequence of item transaction_status values.
	ItemStatuses []string
	// PayoutFail, if not zero, makes payout create return this HTTP status
	// with a VALIDATION_ERROR body (not a duplicate).
	PayoutFail int
	// FailSetupToken makes setup-token creation return a 422 error.
	FailSetupToken bool
	// FailToken makes the OAuth call fail with 401.
	FailToken bool
	// VerifyStatus is returned by verify-webhook-signature.
	VerifyStatus string
	// VerifyFail, if not zero, makes verify-webhook-signature return it.
	VerifyFail int
}

// New starts a Fake. It is closed when the test ends.
func New(t testing.TB) *Fake {
	t.Helper()
	f := &Fake{
		orders:        map[string]string{},
		orderBody:     map[string]string{},
		batches:       map[string]*batch{},
		byBatchID:     map[string]*batch{},
		refunds:       map[string]string{},
		OrderStatus:   "COMPLETED",
		CaptureStatus: "COMPLETED",
		BatchStatuses: []string{"PENDING", "PROCESSING", "SUCCESS"},
		ItemStatuses:  []string{"PENDING", "PENDING", "SUCCESS"},
		VerifyStatus:  "SUCCESS",
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.Server.Close)
	return f
}

// Lock and Unlock guard changes to the exported fields after start.
func (f *Fake) Lock()   { f.mu.Lock() }
func (f *Fake) Unlock() { f.mu.Unlock() }

// HTTPClient returns an http.Client that routes to the fake.
func (f *Fake) HTTPClient() *http.Client {
	u, err := url.Parse(f.Server.URL)
	if err != nil {
		panic(err) // httptest URLs always parse
	}
	return &http.Client{Transport: RewriteTransport{Target: u}}
}

// Requests returns a copy of the requests received so far.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// RequestsTo returns the received requests matching method and path.
// A path ending in "/" matches as a prefix.
func (f *Fake) RequestsTo(method, path string) []Request {
	var out []Request
	for _, r := range f.Requests() {
		if r.Method != method {
			continue
		}
		if r.Path == path || (strings.HasSuffix(path, "/") && strings.HasPrefix(r.Path, path)) {
			out = append(out, r)
		}
	}
	return out
}

func (f *Fake) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body) // a test fake; a short read shows up as a decode failure below
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
	p := r.URL.Path
	switch {
	case r.Method == http.MethodPost && p == "/v3/vault/setup-tokens":
		f.setupToken(w, debugID)
	case r.Method == http.MethodPost && p == "/v3/vault/payment-tokens":
		f.paymentToken(w, body)
	case r.Method == http.MethodPost && p == "/v2/checkout/orders":
		f.order(w, r, body, debugID)
	case r.Method == http.MethodPost && p == "/v1/payments/payouts":
		f.createPayout(w, body, debugID)
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v1/payments/payouts/"):
		f.getPayout(w, strings.TrimPrefix(p, "/v1/payments/payouts/"), debugID)
	case r.Method == http.MethodPost && strings.HasPrefix(p, "/v2/payments/captures/") && strings.HasSuffix(p, "/refund"):
		f.refund(w, r, body)
	case r.Method == http.MethodPost && p == "/v1/notifications/verify-webhook-signature":
		f.verify(w, body, debugID)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"name":"RESOURCE_NOT_FOUND","message":"not in fake","debug_id":%q}`, debugID)
	}
}

func (f *Fake) token(w http.ResponseWriter, r *http.Request) {
	id, secret, ok := r.BasicAuth()
	f.mu.Lock()
	fail := f.FailToken
	f.mu.Unlock()
	if fail || !ok || id != ClientID || secret != ClientSecret {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid_client","error_description":"Client Authentication failed"}`)
		return
	}
	// Shape from developer.paypal.com/api/rest/authentication/ sample.
	fmt.Fprintf(w, `{"scope":"https://uri.paypal.com/services/payments/payment","access_token":%q,"token_type":"Bearer","app_id":"APP-80W284485P519543T","expires_in":31668,"nonce":"2026-11-11T00:00:00Zabc"}`, AccessToken)
}

func (f *Fake) setupToken(w http.ResponseWriter, debugID string) {
	f.mu.Lock()
	fail := f.FailSetupToken
	f.mu.Unlock()
	if fail {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprintf(w, `{"name":"UNPROCESSABLE_ENTITY","details":[{"issue":"FEATURE_NOT_ENABLED","description":"Vaulting is not enabled for this merchant."}],"message":"The requested action could not be performed, semantically incorrect, or failed business validation.","debug_id":%q}`, debugID)
		return
	}
	// Shape from the "save without purchase" PayPal guide sample.
	w.WriteHeader(http.StatusCreated)
	fmt.Fprint(w, `{"id":"4G4976650J0948357","customer":{"id":"customer_4029352051"},"status":"PAYER_ACTION_REQUIRED","payment_source":{"paypal":{"description":"FourHour","usage_pattern":"IMMEDIATE","usage_type":"MERCHANT","customer_type":"CONSUMER"}},"links":[{"href":"https://api-m.sandbox.paypal.com/v3/vault/setup-tokens/4G4976650J0948357","rel":"self","method":"GET"},{"href":"https://sandbox.paypal.com/agreements/approve?approval_session_id=4G4976650J0948357","rel":"approve","method":"GET"}]}`)
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
	fmt.Fprint(w, `{"id":"jwgvx42","customer":{"id":"customer_4029352051"},"payment_source":{"paypal":{"description":"FourHour","usage_pattern":"IMMEDIATE","shipping":{},"permit_multiple_payment_tokens":false,"usage_type":"MERCHANT","customer_type":"CONSUMER","email_address":"buyer@example.com","payer_id":"AJM9JTWQJCFTA"}},"links":[{"rel":"self","href":"https://api-m.sandbox.paypal.com/v3/vault/payment-tokens/jwgvx42","method":"GET"},{"rel":"delete","href":"https://api-m.sandbox.paypal.com/v3/vault/payment-tokens/jwgvx42","method":"DELETE"}]}`)
}

func (f *Fake) order(w http.ResponseWriter, r *http.Request, body []byte, debugID string) {
	var req struct {
		PurchaseUnits []struct {
			Amount struct {
				Value string `json:"value"`
			} `json:"amount"`
		} `json:"purchase_units"`
	}
	if json.Unmarshal(body, &req) != nil || len(req.PurchaseUnits) != 1 {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"name":"INVALID_REQUEST","message":"bad order","debug_id":%q}`, debugID)
		return
	}
	rid := r.Header.Get("PayPal-Request-Id")
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, seen := f.orders[rid]; seen && rid != "" {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, f.orderBody[id])
		return
	}
	if f.OrderFail != 0 {
		w.WriteHeader(f.OrderFail)
		fmt.Fprintf(w, `{"name":"UNPROCESSABLE_ENTITY","message":"The requested action could not be performed.","debug_id":%q,"details":[{"issue":"PAYER_ACCOUNT_RESTRICTED"}]}`, debugID)
		return
	}
	n := len(f.orders) + 1
	id := fmt.Sprintf("5O190127TN36471%02d", n)
	capID := fmt.Sprintf("3C679366HH9089%02d", n)
	b := fmt.Sprintf(`{"id":%q,"status":%q,"intent":"CAPTURE","payment_source":{"paypal":{"email_address":"buyer@example.com","account_id":"AJM9JTWQJCFTA","account_status":"VERIFIED"}},"purchase_units":[{"reference_id":"default","payments":{"captures":[{"id":%q,"status":%q,"amount":{"currency_code":"USD","value":%q},"final_capture":true,"create_time":"2026-11-11T10:00:00Z","update_time":"2026-11-11T10:00:00Z"}]}}],"links":[{"href":"https://api-m.sandbox.paypal.com/v2/checkout/orders/%s","rel":"self","method":"GET"}]}`,
		id, f.OrderStatus, capID, f.CaptureStatus, req.PurchaseUnits[0].Amount.Value, id)
	if rid != "" {
		f.orders[rid] = id
	}
	f.orderBody[id] = b
	w.WriteHeader(http.StatusCreated)
	fmt.Fprint(w, b)
}

func (f *Fake) createPayout(w http.ResponseWriter, body []byte, debugID string) {
	var req struct {
		SenderBatchHeader struct {
			SenderBatchID string `json:"sender_batch_id"`
		} `json:"sender_batch_header"`
		Items []struct {
			Amount struct {
				Value string `json:"value"`
			} `json:"amount"`
			Receiver     string `json:"receiver"`
			SenderItemID string `json:"sender_item_id"`
		} `json:"items"`
	}
	if json.Unmarshal(body, &req) != nil || req.SenderBatchHeader.SenderBatchID == "" || len(req.Items) != 1 {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"name":"VALIDATION_ERROR","message":"Invalid request - see details","debug_id":%q,"details":[{"field":"items","issue":"invalid"}]}`, debugID)
		return
	}
	sbid := req.SenderBatchHeader.SenderBatchID
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, dup := f.batches[sbid]; dup {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"name":"USER_BUSINESS_ERROR","message":"User business error.","debug_id":%q,"information_link":"https://developer.paypal.com/docs/api/payments.payouts-batch/#errors","details":[{"field":"SENDER_BATCH_ID","location":"body","issue":"Batch with given sender_batch_id already exists","link":[{"href":"https://api-m.sandbox.paypal.com/v1/payments/payouts/%s","rel":"self","method":"GET","encType":"application/json"}]}],"links":[]}`, debugID, existing.id)
		return
	}
	if f.PayoutFail != 0 {
		w.WriteHeader(f.PayoutFail)
		fmt.Fprintf(w, `{"name":"VALIDATION_ERROR","message":"Invalid request - see details","debug_id":%q,"details":[{"field":"items[0].receiver","location":"body","issue":"Receiver is invalid or does not match with type"}]}`, debugID)
		return
	}
	n := len(f.batches) + 1
	b := &batch{
		id: fmt.Sprintf("5UXD2E8A7EBQ%02d", n), senderBatchID: sbid, itemID: fmt.Sprintf("8AELMXH8UB2P%02d", n),
		receiver: req.Items[0].Receiver, senderItemID: req.Items[0].SenderItemID, value: req.Items[0].Amount.Value,
	}
	f.batches[sbid] = b
	f.byBatchID[b.id] = b
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, `{"batch_header":{"sender_batch_header":{"sender_batch_id":%q,"email_subject":"You have a payout!"},"payout_batch_id":%q,"batch_status":"PENDING"},"links":[{"href":"https://api-m.sandbox.paypal.com/v1/payments/payouts/%s","rel":"self","method":"GET","encType":"application/json"}]}`, sbid, b.id, b.id)
}

func pick(seq []string, i int) string {
	if i < len(seq) {
		return seq[i]
	}
	return seq[len(seq)-1]
}

func (f *Fake) getPayout(w http.ResponseWriter, batchID, debugID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.byBatchID[batchID]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"name":"INVALID_RESOURCE_ID","message":"Requested resource ID was not found.","debug_id":%q}`, debugID)
		return
	}
	i := b.gets
	b.gets++
	fmt.Fprintf(w, `{"batch_header":{"payout_batch_id":%q,"batch_status":%q,"time_created":"2026-11-11T10:00:00Z","sender_batch_header":{"sender_batch_id":%q,"email_subject":"You have a payout!"},"funding_source":"BALANCE","amount":{"currency":"USD","value":%q},"fees":{"currency":"USD","value":"0.00"}},"items":[{"payout_item_id":%q,"transaction_id":"0C413693MN970190K","activity_id":"0E158638XS0329106","transaction_status":%q,"payout_item_fee":{"currency":"USD","value":"0.00"},"payout_batch_id":%q,"payout_item":{"recipient_type":"EMAIL","amount":{"currency":"USD","value":%q},"note":"FourHour","receiver":%q,"sender_item_id":%q,"recipient_wallet":"PAYPAL"},"time_processed":"2026-11-11T10:00:05Z","links":[]}],"links":[]}`,
		b.id, pick(f.BatchStatuses, i), b.senderBatchID, b.value, b.itemID, pick(f.ItemStatuses, i), b.id, b.value, b.receiver, b.senderItemID)
}

func (f *Fake) refund(w http.ResponseWriter, r *http.Request, body []byte) {
	var req struct {
		Amount struct {
			Value string `json:"value"`
		} `json:"amount"`
	}
	_ = json.Unmarshal(body, &req) // an empty amount is echoed as empty
	rid := r.Header.Get("PayPal-Request-Id")
	f.mu.Lock()
	id, seen := f.refunds[rid]
	if !seen {
		id = fmt.Sprintf("1JU08902781691%03d", len(f.refunds)+1)
		f.refunds[rid] = id
	}
	f.mu.Unlock()
	if seen {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
	fmt.Fprintf(w, `{"id":%q,"status":"COMPLETED","amount":{"currency_code":"USD","value":%q},"links":[]}`, id, req.Amount.Value)
}

func (f *Fake) verify(w http.ResponseWriter, body []byte, debugID string) {
	var req map[string]json.RawMessage
	required := []string{"auth_algo", "cert_url", "transmission_id", "transmission_sig", "transmission_time", "webhook_id", "webhook_event"}
	ok := json.Unmarshal(body, &req) == nil
	for _, k := range required {
		if _, has := req[k]; !has {
			ok = false
		}
	}
	f.mu.Lock()
	fail, status := f.VerifyFail, f.VerifyStatus
	f.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"name":"VALIDATION_ERROR","message":"Invalid request - see details","debug_id":%q}`, debugID)
		return
	}
	if fail != 0 {
		w.WriteHeader(fail)
		fmt.Fprintf(w, `{"name":"INTERNAL_SERVICE_ERROR","message":"An internal service error has occurred.","debug_id":%q}`, debugID)
		return
	}
	fmt.Fprintf(w, `{"verification_status":%q}`, status)
}
