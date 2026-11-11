package paypal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ErrNotSandbox is returned when the base URL is anything but SandboxBaseURL.
var ErrNotSandbox = errors.New("paypal: base URL must be " + SandboxBaseURL)

// DefaultTimeout bounds every PayPal call when Config.Timeout is zero.
const DefaultTimeout = 30 * time.Second

// maxResponseBytes caps how much of a response body is read.
const maxResponseBytes = 4 << 20

// tokenRefreshMargin is how long before expiry a cached token is replaced.
const tokenRefreshMargin = 5 * time.Minute

// ResponseInfo describes one PayPal HTTP response. It never contains
// credentials or tokens, so it is safe to log.
type ResponseInfo struct {
	Method     string
	Path       string
	StatusCode int
	DebugID    string
}

// Config configures a Client.
type Config struct {
	// BaseURL must equal SandboxBaseURL exactly.
	BaseURL      string
	ClientID     string
	ClientSecret string
	// HTTPClient is optional. Tests inject a transport here.
	HTTPClient *http.Client
	// Timeout per call; DefaultTimeout when zero.
	Timeout time.Duration
	// OnResponse, if set, is called for every response, including the token call.
	OnResponse func(ResponseInfo)
	// Now is optional, for tests.
	Now func() time.Time
}

// Client talks to the PayPal REST API. It is safe for concurrent use.
type Client struct {
	baseURL      string
	clientID     string
	clientSecret string
	httpClient   *http.Client
	timeout      time.Duration
	onResponse   func(ResponseInfo)
	now          func() time.Time

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

// NewClient validates cfg and returns a Client. It refuses any base URL other
// than the sandbox.
func NewClient(cfg Config) (*Client, error) {
	if cfg.BaseURL != SandboxBaseURL {
		return nil, fmt.Errorf("%w (got %q)", ErrNotSandbox, cfg.BaseURL)
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("paypal: client ID and client secret are required")
	}
	c := &Client{
		baseURL:      cfg.BaseURL,
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		httpClient:   cfg.HTTPClient,
		timeout:      cfg.Timeout,
		onResponse:   cfg.OnResponse,
		now:          cfg.Now,
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{Timeout: DefaultTimeout + 5*time.Second}
	}
	if c.timeout <= 0 {
		c.timeout = DefaultTimeout
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c, nil
}

// Result carries the HTTP status, the paypal-debug-id header and the raw body
// of a successful call.
type Result struct {
	StatusCode int
	DebugID    string
	Body       []byte
}

// APIError is returned for any non-2xx response. Body is the full raw body.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	DebugID    string
	Body       []byte
	// Parsed is set when the body is PayPal's standard REST error shape.
	Parsed *ErrorResponse
}

func (e *APIError) Error() string {
	name := ""
	if e.Parsed != nil && e.Parsed.Name != "" {
		name = " " + e.Parsed.Name
	}
	return fmt.Sprintf("paypal: %s %s: HTTP %d%s (paypal-debug-id %s)", e.Method, e.Path, e.StatusCode, name, e.DebugID)
}

// AsAPIError unwraps err into an *APIError if it is one.
func AsAPIError(err error) (*APIError, bool) {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// Calls

// CreateSetupToken is P2. requestID is sent as PayPal-Request-Id.
func (c *Client) CreateSetupToken(ctx context.Context, requestID string, req SetupTokenRequest) (SetupTokenResponse, Result, error) {
	var out SetupTokenResponse
	res, err := c.doJSON(ctx, http.MethodPost, "/v3/vault/setup-tokens", requestID, false, req, &out)
	return out, res, err
}

// CreatePaymentToken is P3. requestID is sent as PayPal-Request-Id.
func (c *Client) CreatePaymentToken(ctx context.Context, requestID string, req PaymentTokenRequest) (PaymentTokenResponse, Result, error) {
	var out PaymentTokenResponse
	res, err := c.doJSON(ctx, http.MethodPost, "/v3/vault/payment-tokens", requestID, false, req, &out)
	return out, res, err
}

// CreateOrder is P4. requestID is sent as PayPal-Request-Id. The request asks
// for "Prefer: return=representation" so the captures are in the response.
func (c *Client) CreateOrder(ctx context.Context, requestID string, req OrderRequest) (Order, Result, error) {
	var out Order
	res, err := c.doJSON(ctx, http.MethodPost, "/v2/checkout/orders", requestID, true, req, &out)
	return out, res, err
}

// CreatePayout is P5. Idempotency comes from sender_batch_header.sender_batch_id
// (CONTRACTS.md section 7); no PayPal-Request-Id header is sent.
func (c *Client) CreatePayout(ctx context.Context, req CreatePayoutRequest) (CreatePayoutResponse, Result, error) {
	var out CreatePayoutResponse
	if req.SenderBatchHeader.SenderBatchID == "" {
		return out, Result{}, errors.New("paypal: sender_batch_id is required")
	}
	res, err := c.doJSON(ctx, http.MethodPost, "/v1/payments/payouts", "", false, req, &out)
	return out, res, err
}

// GetPayoutBatch is P6.
func (c *Client) GetPayoutBatch(ctx context.Context, payoutBatchID string) (PayoutBatch, Result, error) {
	var out PayoutBatch
	if payoutBatchID == "" {
		return out, Result{}, errors.New("paypal: payout_batch_id is required")
	}
	res, err := c.doJSON(ctx, http.MethodGet, "/v1/payments/payouts/"+url.PathEscape(payoutBatchID), "", false, nil, &out)
	return out, res, err
}

// ---------------------------------------------------------------------------
// Transport

// accessToken returns a cached token or fetches a new one (P1).
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.now().Before(c.tokenExpiry) {
		return c.token, nil
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	const path = "/v1/oauth2/token"
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("paypal: build token request: %w", err)
	}
	req.SetBasicAuth(c.clientID, c.clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	status, debugID, body, err := c.send(req, path)
	if err != nil {
		return "", err
	}
	if status < 200 || status > 299 {
		// The OAuth error body holds no secret; keep it for diagnosis.
		return "", &APIError{Method: http.MethodPost, Path: path, StatusCode: status, DebugID: debugID, Body: body}
	}
	var tr AccessTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		// Do not include the body: on success it contains the token.
		return "", fmt.Errorf("paypal: decode token response (paypal-debug-id %s): %w", debugID, err)
	}
	if tr.AccessToken == "" || tr.ExpiresIn <= 0 {
		return "", fmt.Errorf("paypal: token response missing access_token or expires_in (paypal-debug-id %s)", debugID)
	}
	lifetime := time.Duration(tr.ExpiresIn) * time.Second
	margin := tokenRefreshMargin
	if margin > lifetime/2 {
		margin = lifetime / 2
	}
	c.token = tr.AccessToken
	c.tokenExpiry = c.now().Add(lifetime - margin)
	return c.token, nil
}

func (c *Client) doJSON(ctx context.Context, method, path, requestID string, preferRepresentation bool, in, out any) (Result, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var bodyReader io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return Result{}, fmt.Errorf("paypal: encode %s %s: %w", method, path, err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return Result{}, fmt.Errorf("paypal: build %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if requestID != "" {
		req.Header.Set("PayPal-Request-Id", requestID)
	}
	if preferRepresentation {
		req.Header.Set("Prefer", "return=representation")
	}

	status, debugID, body, err := c.send(req, path)
	if err != nil {
		return Result{}, err
	}
	res := Result{StatusCode: status, DebugID: debugID, Body: body}
	if status < 200 || status > 299 {
		ae := &APIError{Method: method, Path: path, StatusCode: status, DebugID: debugID, Body: body}
		var pe ErrorResponse
		if json.Unmarshal(body, &pe) == nil && pe.Name != "" {
			ae.Parsed = &pe
		}
		return res, ae
	}
	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return res, fmt.Errorf("paypal: decode %s %s (paypal-debug-id %s): %w", method, path, debugID, err)
		}
	}
	return res, nil
}

// send performs the request, reads the body and reports the response.
func (c *Client) send(req *http.Request, path string) (int, string, []byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// url.Error includes the URL only; no credentials are in it.
		return 0, "", nil, fmt.Errorf("paypal: %s %s: %w", req.Method, path, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	debugID := resp.Header.Get("Paypal-Debug-Id")
	if c.onResponse != nil {
		c.onResponse(ResponseInfo{Method: req.Method, Path: path, StatusCode: resp.StatusCode, DebugID: debugID})
	}
	if readErr != nil {
		return resp.StatusCode, debugID, nil, fmt.Errorf("paypal: read %s %s body (paypal-debug-id %s): %w", req.Method, path, debugID, readErr)
	}
	return resp.StatusCode, debugID, body, nil
}
