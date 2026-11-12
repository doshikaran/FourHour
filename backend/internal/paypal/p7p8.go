package paypal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ---------------------------------------------------------------------------
// P7: POST /v2/payments/captures/{capture_id}/refund

// Refund statuses.
const (
	RefundStatusCompleted = "COMPLETED"
	RefundStatusPending   = "PENDING"
	RefundStatusCancelled = "CANCELLED"
	RefundStatusFailed    = "FAILED"
)

// RefundRequest is the body of a capture refund. Amount is required by
// FourHour (partial, pro-rata refunds).
type RefundRequest struct {
	Amount      *Money `json:"amount,omitempty"`
	NoteToPayer string `json:"note_to_payer,omitempty"`
}

// Refund is the refund resource.
type Refund struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Amount *Money `json:"amount,omitempty"`
	Links  []Link `json:"links,omitempty"`
}

// RefundCapture is P7. requestID is sent as PayPal-Request-Id.
func (c *Client) RefundCapture(ctx context.Context, requestID, captureID string, req RefundRequest) (Refund, Result, error) {
	var out Refund
	if captureID == "" || requestID == "" {
		return out, Result{}, errors.New("paypal: capture ID and request ID are required")
	}
	res, err := c.doJSON(ctx, http.MethodPost, "/v2/payments/captures/"+url.PathEscape(captureID)+"/refund", requestID, true, req, &out)
	return out, res, err
}

// ---------------------------------------------------------------------------
// P8: POST /v1/notifications/verify-webhook-signature

// Verification statuses.
const (
	VerificationSuccess = "SUCCESS"
	VerificationFailure = "FAILURE"
)

// VerifyWebhookRequest is verify_webhook_signature. Every field is required.
// WebhookEvent must be the received body byte for byte.
type VerifyWebhookRequest struct {
	AuthAlgo         string          `json:"auth_algo"`
	CertURL          string          `json:"cert_url"`
	TransmissionID   string          `json:"transmission_id"`
	TransmissionSig  string          `json:"transmission_sig"`
	TransmissionTime string          `json:"transmission_time"`
	WebhookID        string          `json:"webhook_id"`
	WebhookEvent     json.RawMessage `json:"webhook_event"`
}

// VerifyWebhookResponse is verify_webhook_signature_response.
type VerifyWebhookResponse struct {
	VerificationStatus string `json:"verification_status"`
}

// VerifyWebhookRequestFromHeaders fills the transmission fields from the
// PAYPAL-* headers of a received notification.
func VerifyWebhookRequestFromHeaders(h http.Header, webhookID string, rawBody []byte) VerifyWebhookRequest {
	return VerifyWebhookRequest{
		AuthAlgo:         h.Get("Paypal-Auth-Algo"),
		CertURL:          h.Get("Paypal-Cert-Url"),
		TransmissionID:   h.Get("Paypal-Transmission-Id"),
		TransmissionSig:  h.Get("Paypal-Transmission-Sig"),
		TransmissionTime: h.Get("Paypal-Transmission-Time"),
		WebhookID:        webhookID,
		WebhookEvent:     json.RawMessage(rawBody),
	}
}

// VerifyWebhookSignature is P8.
func (c *Client) VerifyWebhookSignature(ctx context.Context, req VerifyWebhookRequest) (VerifyWebhookResponse, Result, error) {
	var out VerifyWebhookResponse
	if !json.Valid(req.WebhookEvent) {
		return out, Result{}, errors.New("paypal: webhook_event is not valid JSON")
	}
	body, err := req.encode()
	if err != nil {
		return out, Result{}, err
	}
	res, err := c.doJSON(ctx, http.MethodPost, "/v1/notifications/verify-webhook-signature", "", false, body, &out)
	return out, res, err
}

// encode writes the request with webhook_event as the received bytes,
// unchanged. encoding/json would compact them and escape <, > and &, so the
// event PayPal checks would not be the event PayPal sent.
func (r VerifyWebhookRequest) encode() (preEncoded, error) {
	head := struct {
		AuthAlgo         string `json:"auth_algo"`
		CertURL          string `json:"cert_url"`
		TransmissionID   string `json:"transmission_id"`
		TransmissionSig  string `json:"transmission_sig"`
		TransmissionTime string `json:"transmission_time"`
		WebhookID        string `json:"webhook_id"`
	}{r.AuthAlgo, r.CertURL, r.TransmissionID, r.TransmissionSig, r.TransmissionTime, r.WebhookID}
	b, err := json.Marshal(head)
	if err != nil {
		return nil, fmt.Errorf("paypal: encode verify-webhook-signature: %w", err)
	}
	out := make([]byte, 0, len(b)+len(r.WebhookEvent)+20)
	out = append(out, b[:len(b)-1]...) // drop the closing brace
	out = append(out, `,"webhook_event":`...)
	out = append(out, r.WebhookEvent...)
	out = append(out, '}')
	return preEncoded(out), nil
}

// ---------------------------------------------------------------------------
// P5 duplicate sender_batch_id (decision D7, CONTRACTS.md section 7)

// DuplicateBatchID reports whether err is PayPal's answer to a repeated
// sender_batch_id: HTTP 400, name USER_BUSINESS_ERROR, details[0].field
// SENDER_BATCH_ID, with details[0].link[0].href pointing at the original
// batch. It returns the original payout_batch_id read from that link. Any
// other error, including another 400, returns ok false.
func DuplicateBatchID(err error) (string, bool) {
	ae, ok := AsAPIError(err)
	if !ok || ae.StatusCode != http.StatusBadRequest || ae.Parsed == nil {
		return "", false
	}
	p := ae.Parsed
	if p.Name != "USER_BUSINESS_ERROR" || len(p.Details) == 0 || p.Details[0].Field != "SENDER_BATCH_ID" || len(p.Details[0].Link) == 0 {
		return "", false
	}
	u, perr := url.Parse(p.Details[0].Link[0].Href)
	if perr != nil {
		return "", false
	}
	const prefix = "/v1/payments/payouts/"
	if !strings.HasPrefix(u.Path, prefix) {
		return "", false
	}
	id := strings.TrimPrefix(u.Path, prefix)
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}
