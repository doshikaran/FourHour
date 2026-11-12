// Package paypal holds typed request and response structs for the PayPal REST
// calls FourHour uses (CONTRACTS.md section 7) and a small client for them.
//
// It is the only package in the backend that talks to PayPal. It started as
// the spike package in spikes/paypal/paypal (S1 and S2 ran against the real
// sandbox with it) and adds P7, P8 and the P5 duplicate-batch reading here.
//
// Field names were checked against PayPal's published OpenAPI documents,
// downloaded on 2026-11-11 from developer.paypal.com:
//   - /api/payment-tokens/v3/schema.json  (setup_token_request, setup_token_response,
//     payment_token_request, payment_token_response, error)
//   - /api/orders/v2/schema.json          (order_request, paypal_wallet, order, capture)
//   - /api/payments.payouts-batch/v1/schema.json (create_payout_request, payout,
//     payout_batch, error)
//   - /api/rest/authentication/ (OAuth client-credentials token)
//   - /api/webhooks/v1/schema.json        (verify_webhook_signature, event), read 2026-11-12
//   - /api/payments/v2/ (refund request and response), read 2026-11-12
//
// Only the fields FourHour reads or writes are modelled. Unknown response
// fields are ignored by encoding/json.
package paypal

// SandboxBaseURL is the only PayPal base URL this code will talk to
// (CLAUDE.md rule 6, CONTRACTS.md section 2).
const SandboxBaseURL = "https://api-m.sandbox.paypal.com"

// Link is PayPal's HATEOAS link_description.
type Link struct {
	Href   string `json:"href"`
	Rel    string `json:"rel"`
	Method string `json:"method,omitempty"`
}

// FindLink returns the href of the first link with the given rel.
func FindLink(links []Link, rel string) (string, bool) {
	for _, l := range links {
		if l.Rel == rel {
			return l.Href, true
		}
	}
	return "", false
}

// ErrorDetail is one entry of error.details.
type ErrorDetail struct {
	Field       string `json:"field,omitempty"`
	Value       string `json:"value,omitempty"`
	Location    string `json:"location,omitempty"`
	Issue       string `json:"issue"`
	Description string `json:"description,omitempty"`
	// Link is not in the published error_details schema, but the sandbox
	// returns it on a repeated sender_batch_id (spike S2, CONTRACTS.md 7).
	Link []Link `json:"link,omitempty"`
}

// ErrorResponse is PayPal's standard REST error body (schema "error").
type ErrorResponse struct {
	Name            string        `json:"name"`
	Message         string        `json:"message"`
	DebugID         string        `json:"debug_id"`
	InformationLink string        `json:"information_link,omitempty"`
	Details         []ErrorDetail `json:"details,omitempty"`
	Links           []Link        `json:"links,omitempty"`
}

// OAuthErrorResponse is the body the token endpoint returns on failure. It is
// not the REST error shape.
type OAuthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// ---------------------------------------------------------------------------
// P1: POST /v1/oauth2/token

// AccessTokenResponse is the client-credentials token response. ExpiresIn is
// in seconds. It must never be printed or logged: String and GoString redact it.
type AccessTokenResponse struct {
	Scope       string `json:"scope"`
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	AppID       string `json:"app_id"`
	ExpiresIn   int64  `json:"expires_in"`
	Nonce       string `json:"nonce"`
}

// String redacts the token so an accidental %v cannot leak it.
func (AccessTokenResponse) String() string { return "AccessTokenResponse{redacted}" }

// GoString redacts the token so an accidental %#v cannot leak it.
func (AccessTokenResponse) GoString() string { return "AccessTokenResponse{redacted}" }

// ---------------------------------------------------------------------------
// P2: POST /v3/vault/setup-tokens   and   P3: POST /v3/vault/payment-tokens

// Usage types for payment_source.paypal.usage_type.
const (
	UsageTypeMerchant = "MERCHANT"
	UsageTypePlatform = "PLATFORM"
)

// Token types for payment_source.token.type.
const TokenTypeSetupToken = "SETUP_TOKEN"

// Vault token statuses (payment_token_status).
const (
	VaultStatusCreated             = "CREATED"
	VaultStatusPayerActionRequired = "PAYER_ACTION_REQUIRED"
	VaultStatusApproved            = "APPROVED"
	VaultStatusVaulted             = "VAULTED"
	VaultStatusTokenized           = "TOKENIZED"
)

// LinkRelApprove is the rel of the link the payer is sent to.
const LinkRelApprove = "approve"

// VaultCustomer is the customer object on vault requests and responses.
type VaultCustomer struct {
	ID                 string `json:"id,omitempty"`
	MerchantCustomerID string `json:"merchant_customer_id,omitempty"`
}

// VaultExperienceContext is paypal_wallet_experience_context in the vault API.
type VaultExperienceContext struct {
	BrandName          string `json:"brand_name,omitempty"`
	Locale             string `json:"locale,omitempty"`
	ReturnURL          string `json:"return_url,omitempty"`
	CancelURL          string `json:"cancel_url,omitempty"`
	ShippingPreference string `json:"shipping_preference,omitempty"`
	VaultInstruction   string `json:"vault_instruction,omitempty"`
	UserAction         string `json:"user_action,omitempty"`
}

// VaultPayPalWalletRequest is paypal_wallet_request in the vault API.
type VaultPayPalWalletRequest struct {
	Description                 string                  `json:"description,omitempty"`
	UsagePattern                string                  `json:"usage_pattern,omitempty"`
	UsageType                   string                  `json:"usage_type,omitempty"`
	CustomerType                string                  `json:"customer_type,omitempty"`
	PermitMultiplePaymentTokens *bool                   `json:"permit_multiple_payment_tokens,omitempty"`
	ExperienceContext           *VaultExperienceContext `json:"experience_context,omitempty"`
}

// SetupTokenPaymentSource is setup_token_request.payment_source (PayPal only).
type SetupTokenPaymentSource struct {
	PayPal *VaultPayPalWalletRequest `json:"paypal,omitempty"`
}

// SetupTokenRequest is the body of POST /v3/vault/setup-tokens.
type SetupTokenRequest struct {
	Customer      *VaultCustomer          `json:"customer,omitempty"`
	PaymentSource SetupTokenPaymentSource `json:"payment_source"`
}

// VaultPayPalWalletResponse is paypal_wallet_response in the vault API.
type VaultPayPalWalletResponse struct {
	Description                 string `json:"description,omitempty"`
	UsagePattern                string `json:"usage_pattern,omitempty"`
	UsageType                   string `json:"usage_type,omitempty"`
	CustomerType                string `json:"customer_type,omitempty"`
	PermitMultiplePaymentTokens *bool  `json:"permit_multiple_payment_tokens,omitempty"`
	EmailAddress                string `json:"email_address,omitempty"`
	PayerID                     string `json:"payer_id,omitempty"`
	AccountID                   string `json:"account_id,omitempty"`
}

// VaultPaymentSourceResponse is the payment_source of vault responses (PayPal only).
type VaultPaymentSourceResponse struct {
	PayPal *VaultPayPalWalletResponse `json:"paypal,omitempty"`
}

// SetupTokenResponse is the response of POST /v3/vault/setup-tokens.
type SetupTokenResponse struct {
	ID            string                      `json:"id"`
	Customer      *VaultCustomer              `json:"customer,omitempty"`
	Status        string                      `json:"status"`
	PaymentSource *VaultPaymentSourceResponse `json:"payment_source,omitempty"`
	Links         []Link                      `json:"links,omitempty"`
}

// ApproveURL returns the href of the "approve" link.
func (r SetupTokenResponse) ApproveURL() (string, bool) { return FindLink(r.Links, LinkRelApprove) }

// TokenIDRequest is token_id_request: a setup token to upgrade.
type TokenIDRequest struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// PaymentTokenPaymentSource is payment_token_request.payment_source (token only).
type PaymentTokenPaymentSource struct {
	Token *TokenIDRequest `json:"token,omitempty"`
}

// PaymentTokenRequest is the body of POST /v3/vault/payment-tokens.
type PaymentTokenRequest struct {
	Customer      *VaultCustomer            `json:"customer,omitempty"`
	PaymentSource PaymentTokenPaymentSource `json:"payment_source"`
}

// PaymentTokenResponse is the response of POST /v3/vault/payment-tokens.
type PaymentTokenResponse struct {
	ID            string                      `json:"id"`
	Customer      *VaultCustomer              `json:"customer,omitempty"`
	PaymentSource *VaultPaymentSourceResponse `json:"payment_source,omitempty"`
	Links         []Link                      `json:"links,omitempty"`
}

// ---------------------------------------------------------------------------
// P4: POST /v2/checkout/orders

// Order intents.
const IntentCapture = "CAPTURE"

// Order statuses (order_status).
const (
	OrderStatusCreated             = "CREATED"
	OrderStatusSaved               = "SAVED"
	OrderStatusApproved            = "APPROVED"
	OrderStatusVoided              = "VOIDED"
	OrderStatusCompleted           = "COMPLETED"
	OrderStatusPayerActionRequired = "PAYER_ACTION_REQUIRED"
)

// Capture statuses (capture.status).
const (
	CaptureStatusCompleted         = "COMPLETED"
	CaptureStatusDeclined          = "DECLINED"
	CaptureStatusPartiallyRefunded = "PARTIALLY_REFUNDED"
	CaptureStatusPending           = "PENDING"
	CaptureStatusRefunded          = "REFUNDED"
	CaptureStatusFailed            = "FAILED"
)

// Money is the Orders and Payments API money object. Value is a decimal
// string; build it with FormatCents, never from a float.
type Money struct {
	CurrencyCode string `json:"currency_code"`
	Value        string `json:"value"`
}

// PurchaseUnitRequest is purchase_unit_request (fields FourHour uses).
type PurchaseUnitRequest struct {
	ReferenceID string `json:"reference_id,omitempty"`
	Description string `json:"description,omitempty"`
	CustomID    string `json:"custom_id,omitempty"`
	InvoiceID   string `json:"invoice_id,omitempty"`
	Amount      Money  `json:"amount"`
}

// OrderPayPalWallet is payment_source.paypal on an order request. FourHour
// sets only vault_id, the saved payment token (CONTRACTS.md P4).
type OrderPayPalWallet struct {
	VaultID string `json:"vault_id,omitempty"`
}

// OrderPaymentSource is order_request.payment_source (PayPal only).
type OrderPaymentSource struct {
	PayPal *OrderPayPalWallet `json:"paypal,omitempty"`
}

// OrderRequest is the body of POST /v2/checkout/orders.
type OrderRequest struct {
	Intent        string                `json:"intent"`
	PurchaseUnits []PurchaseUnitRequest `json:"purchase_units"`
	PaymentSource *OrderPaymentSource   `json:"payment_source,omitempty"`
}

// CaptureStatusDetails explains a PENDING or DECLINED capture.
type CaptureStatusDetails struct {
	Reason string `json:"reason,omitempty"`
}

// Capture is a capture inside an order's purchase unit.
type Capture struct {
	ID            string                `json:"id"`
	Status        string                `json:"status"`
	StatusDetails *CaptureStatusDetails `json:"status_details,omitempty"`
	Amount        *Money                `json:"amount,omitempty"`
	FinalCapture  bool                  `json:"final_capture,omitempty"`
	CreateTime    string                `json:"create_time,omitempty"`
	UpdateTime    string                `json:"update_time,omitempty"`
}

// PaymentCollection is purchase_unit.payments.
type PaymentCollection struct {
	Captures []Capture `json:"captures,omitempty"`
}

// PurchaseUnit is a purchase unit in an order response.
type PurchaseUnit struct {
	ReferenceID string             `json:"reference_id,omitempty"`
	Amount      *Money             `json:"amount,omitempty"`
	CustomID    string             `json:"custom_id,omitempty"`
	Payments    *PaymentCollection `json:"payments,omitempty"`
}

// Order is the order resource. Captures are only present when the request
// was sent with "Prefer: return=representation".
type Order struct {
	ID            string         `json:"id"`
	Status        string         `json:"status"`
	Intent        string         `json:"intent,omitempty"`
	PurchaseUnits []PurchaseUnit `json:"purchase_units,omitempty"`
	Links         []Link         `json:"links,omitempty"`
	CreateTime    string         `json:"create_time,omitempty"`
	UpdateTime    string         `json:"update_time,omitempty"`
}

// FirstCapture returns the first capture of the first purchase unit.
func (o Order) FirstCapture() (Capture, bool) {
	for _, pu := range o.PurchaseUnits {
		if pu.Payments != nil && len(pu.Payments.Captures) > 0 {
			return pu.Payments.Captures[0], true
		}
	}
	return Capture{}, false
}

// ---------------------------------------------------------------------------
// P5: POST /v1/payments/payouts   and   P6: GET /v1/payments/payouts/{id}

// Recipient types and wallets.
const (
	RecipientTypeEmail    = "EMAIL"
	RecipientWalletPayPal = "PAYPAL"
)

// Batch statuses (batch_enum).
const (
	BatchStatusDenied     = "DENIED"
	BatchStatusPending    = "PENDING"
	BatchStatusProcessing = "PROCESSING"
	BatchStatusSuccess    = "SUCCESS"
	BatchStatusCanceled   = "CANCELED"
)

// Item transaction statuses (transaction_enum).
const (
	TransactionStatusSuccess   = "SUCCESS"
	TransactionStatusFailed    = "FAILED"
	TransactionStatusPending   = "PENDING"
	TransactionStatusUnclaimed = "UNCLAIMED"
	TransactionStatusReturned  = "RETURNED"
	TransactionStatusOnHold    = "ONHOLD"
	TransactionStatusBlocked   = "BLOCKED"
	TransactionStatusRefunded  = "REFUNDED"
	TransactionStatusReversed  = "REVERSED"
)

// PayoutCurrency is the Payouts API money object. Note the field names differ
// from Money: "currency", not "currency_code".
type PayoutCurrency struct {
	Currency string `json:"currency"`
	Value    string `json:"value"`
}

// SenderBatchHeader is sender_batch_header (request) and
// payout_sender_batch_header (response).
type SenderBatchHeader struct {
	SenderBatchID string `json:"sender_batch_id,omitempty"`
	RecipientType string `json:"recipient_type,omitempty"`
	EmailSubject  string `json:"email_subject,omitempty"`
	EmailMessage  string `json:"email_message,omitempty"`
	Note          string `json:"note,omitempty"`
}

// PayoutItemRequest is payout_item_request.
type PayoutItemRequest struct {
	RecipientType   string         `json:"recipient_type,omitempty"`
	Amount          PayoutCurrency `json:"amount"`
	Note            string         `json:"note,omitempty"`
	Receiver        string         `json:"receiver"`
	SenderItemID    string         `json:"sender_item_id,omitempty"`
	RecipientWallet string         `json:"recipient_wallet,omitempty"`
}

// CreatePayoutRequest is the body of POST /v1/payments/payouts.
type CreatePayoutRequest struct {
	SenderBatchHeader SenderBatchHeader   `json:"sender_batch_header"`
	Items             []PayoutItemRequest `json:"items"`
}

// PayoutHeader is payout_header, in the create response.
type PayoutHeader struct {
	PayoutBatchID     string            `json:"payout_batch_id"`
	BatchStatus       string            `json:"batch_status"`
	TimeCreated       string            `json:"time_created,omitempty"`
	SenderBatchHeader SenderBatchHeader `json:"sender_batch_header"`
}

// CreatePayoutResponse is schema "payout", the create response.
type CreatePayoutResponse struct {
	BatchHeader PayoutHeader `json:"batch_header"`
	Links       []Link       `json:"links,omitempty"`
}

// PayoutBatchHeader is payout_batch_header, in the GET response.
type PayoutBatchHeader struct {
	PayoutBatchID     string            `json:"payout_batch_id"`
	BatchStatus       string            `json:"batch_status"`
	TimeCreated       string            `json:"time_created,omitempty"`
	TimeCompleted     string            `json:"time_completed,omitempty"`
	TimeClosed        string            `json:"time_closed,omitempty"`
	SenderBatchHeader SenderBatchHeader `json:"sender_batch_header"`
	FundingSource     string            `json:"funding_source,omitempty"`
	Amount            *PayoutCurrency   `json:"amount,omitempty"`
	Fees              *PayoutCurrency   `json:"fees,omitempty"`
}

// PayoutItemDetail is payout_item_detail.
type PayoutItemDetail struct {
	RecipientType   string         `json:"recipient_type,omitempty"`
	Amount          PayoutCurrency `json:"amount"`
	Note            string         `json:"note,omitempty"`
	Receiver        string         `json:"receiver"`
	SenderItemID    string         `json:"sender_item_id,omitempty"`
	RecipientWallet string         `json:"recipient_wallet,omitempty"`
	Purpose         string         `json:"purpose,omitempty"`
}

// PayoutBatchItem is payout_batch_items.
type PayoutBatchItem struct {
	PayoutItemID      string           `json:"payout_item_id"`
	TransactionID     string           `json:"transaction_id,omitempty"`
	ActivityID        string           `json:"activity_id,omitempty"`
	TransactionStatus string           `json:"transaction_status,omitempty"`
	PayoutItemFee     *PayoutCurrency  `json:"payout_item_fee,omitempty"`
	PayoutBatchID     string           `json:"payout_batch_id"`
	PayoutItem        PayoutItemDetail `json:"payout_item"`
	TimeProcessed     string           `json:"time_processed,omitempty"`
	Errors            *ErrorResponse   `json:"errors,omitempty"`
	Links             []Link           `json:"links,omitempty"`
}

// PayoutBatch is schema "payout_batch", the GET response.
type PayoutBatch struct {
	TotalItems  int               `json:"total_items,omitempty"`
	TotalPages  int               `json:"total_pages,omitempty"`
	BatchHeader PayoutBatchHeader `json:"batch_header"`
	Items       []PayoutBatchItem `json:"items,omitempty"`
	Links       []Link            `json:"links,omitempty"`
}
