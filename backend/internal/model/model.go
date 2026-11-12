// Package model holds the JSON shapes of CONTRACTS.md sections 3 to 5 that
// cross package boundaries: database rows as the API returns them, and the
// replay stream bodies. Field names and nullability follow the contract.
package model

import "encoding/json"

// Member is a members row.
type Member struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	PayPalEmail string `json:"paypal_email"`
	CreatedAt   string `json:"created_at"`
}

// Policy is a policies row without the vault tokens, which never leave the
// database (CONTRACTS.md section 4).
type Policy struct {
	ID           string  `json:"id"`
	MemberID     string  `json:"member_id"`
	FIPS         string  `json:"fips"`
	CoverCents   int64   `json:"cover_cents"`
	PremiumCents int64   `json:"premium_cents"`
	Status       string  `json:"status"`
	ModelVersion string  `json:"model_version"`
	CreatedAt    string  `json:"created_at"`
	CancelledAt  *string `json:"cancelled_at"`
}

// Policy statuses.
const (
	PolicyPendingVault = "pending_vault"
	PolicyActive       = "active"
	PolicyCancelled    = "cancelled"
)

// TestCoverModelVersion marks a seeded sandbox test policy.
const TestCoverModelVersion = "test-cover"

// PremiumCharge is a premium_charges row.
type PremiumCharge struct {
	ID              string  `json:"id"`
	PolicyID        string  `json:"policy_id"`
	Period          string  `json:"period"`
	AmountCents     int64   `json:"amount_cents"`
	RequestID       string  `json:"request_id"`
	PayPalOrderID   *string `json:"paypal_order_id"`
	PayPalCaptureID *string `json:"paypal_capture_id"`
	Status          string  `json:"status"`
	CreatedAt       string  `json:"created_at"`
}

// Charge statuses.
const (
	ChargeCreated   = "created"
	ChargeCompleted = "completed"
	ChargeFailed    = "failed"
	ChargeRefunded  = "refunded"
)

// OutageEvent is an outage_events row.
type OutageEvent struct {
	ID          string  `json:"id"`
	FIPS        string  `json:"fips"`
	ScenarioID  *string `json:"scenario_id"`
	LoStartedAt string  `json:"lo_started_at"`
	LoEndedAt   *string `json:"lo_ended_at"`
	HiStartedAt *string `json:"hi_started_at"`
	HiEndedAt   *string `json:"hi_ended_at"`
	PeakShare   float64 `json:"peak_share"`
}

// WitnessDevice is a witness_devices row.
type WitnessDevice struct {
	ID           string          `json:"id"`
	PolicyID     string          `json:"policy_id"`
	PublicKeyJWK json.RawMessage `json:"public_key_jwk"`
	RegisteredAt string          `json:"registered_at"`
	LastSeq      int64           `json:"last_seq"`
	LastHash     string          `json:"last_hash"`
}

// WitnessEntry is a witness_entries row.
type WitnessEntry struct {
	ID           string   `json:"id"`
	DeviceID     string   `json:"device_id"`
	Seq          int64    `json:"seq"`
	DeviceTime   string   `json:"device_time"`
	Kind         string   `json:"kind"`
	Charging     bool     `json:"charging"`
	BatteryLevel *float64 `json:"battery_level"`
	Online       bool     `json:"online"`
	PrevHash     string   `json:"prev_hash"`
	Hash         string   `json:"hash"`
	Sig          string   `json:"sig"`
	ReceivedAt   string   `json:"received_at"`
	SimTime      *string  `json:"sim_time"`
	Valid        bool     `json:"valid"`
	RejectReason *string  `json:"reject_reason"`
}

// Claim is a claims row.
type Claim struct {
	ID             string  `json:"id"`
	PolicyID       string  `json:"policy_id"`
	OutageEventID  *string `json:"outage_event_id"`
	Tier           string  `json:"tier"`
	Rule           string  `json:"rule"`
	Decision       string  `json:"decision"`
	AmountCents    int64   `json:"amount_cents"`
	Reason         string  `json:"reason"`
	DecidedAt      string  `json:"decided_at"`
	SimTime        *string `json:"sim_time"`
	WitnessEntryID *string `json:"witness_entry_id"`
}

// Payout is a payouts row.
type Payout struct {
	ID            string  `json:"id"`
	ClaimID       string  `json:"claim_id"`
	SenderBatchID string  `json:"sender_batch_id"`
	PayPalBatchID *string `json:"paypal_batch_id"`
	PayPalItemID  *string `json:"paypal_item_id"`
	AmountCents   int64   `json:"amount_cents"`
	Status        string  `json:"status"`
	CreatedAt     string  `json:"created_at"`
	ConfirmedAt   *string `json:"confirmed_at"`
	ConfirmedVia  *string `json:"confirmed_via"`
}

// Payout statuses and confirmation sources.
const (
	PayoutCreated   = "created"
	PayoutPending   = "pending"
	PayoutSuccess   = "success"
	PayoutFailed    = "failed"
	PayoutUnclaimed = "unclaimed"

	ViaWebhook = "webhook"
	ViaPoll    = "poll"
)

// Refund is a refunds row.
type Refund struct {
	ID              string  `json:"id"`
	PremiumChargeID string  `json:"premium_charge_id"`
	AmountCents     int64   `json:"amount_cents"`
	RequestID       string  `json:"request_id"`
	PayPalRefundID  *string `json:"paypal_refund_id"`
	Status          string  `json:"status"`
	CreatedAt       string  `json:"created_at"`
}

// LedgerEntry is a ledger_entries row.
type LedgerEntry struct {
	ID          string `json:"id"`
	At          string `json:"at"`
	Kind        string `json:"kind"`
	AmountCents int64  `json:"amount_cents"`
	PolicyID    string `json:"policy_id"`
	RefID       string `json:"ref_id"`
}

// ClaimItem is one item of GET /api/claims: the claim row plus its payout,
// the member's name and the policy's county.
type ClaimItem struct {
	Claim
	Payout     *Payout `json:"payout"`
	MemberName string  `json:"member_name"`
	FIPS       string  `json:"fips"`
}

// ---------------------------------------------------------------------------
// Replay stream bodies (CONTRACTS.md section 5). Every body has sim_time.

// Stream event names.
const (
	EvSnapshot        = "snapshot"
	EvTick            = "tick"
	EvEventOpened     = "event_opened"
	EvEventClosed     = "event_closed"
	EvEventMerged     = "event_merged"
	EvWitnessEntry    = "witness_entry"
	EvClaimDecided    = "claim_decided"
	EvPayoutSent      = "payout_sent"
	EvPayoutConfirmed = "payout_confirmed"
	EvReplayEnded     = "replay_ended"
)

// Tick is a tick body; Share and CustomersOut are null for a missing interval.
type Tick struct {
	SimTime      string   `json:"sim_time"`
	Share        *float64 `json:"share"`
	CustomersOut *int64   `json:"customers_out"`
	FIPS         string   `json:"fips"`
}

// EventOpened is an event_opened body.
type EventOpened struct {
	SimTime       string `json:"sim_time"`
	OutageEventID string `json:"outage_event_id"`
	Threshold     string `json:"threshold"`
}

// EventClosed is an event_closed body.
type EventClosed struct {
	SimTime       string  `json:"sim_time"`
	OutageEventID string  `json:"outage_event_id"`
	Threshold     string  `json:"threshold"`
	DurationHours float64 `json:"duration_hours"`
}

// EventMerged is an event_merged body (CONTRACTS.md 1.5).
type EventMerged struct {
	SimTime           string `json:"sim_time"`
	OutageEventID     string `json:"outage_event_id"`
	IntoOutageEventID string `json:"into_outage_event_id"`
}

// WitnessEntryEvent is a witness_entry body. SimTime is null outside a replay.
type WitnessEntryEvent struct {
	SimTime  *string `json:"sim_time"`
	DeviceID string  `json:"device_id"`
	PolicyID string  `json:"policy_id"`
	Kind     string  `json:"kind"`
	Valid    bool    `json:"valid"`
}

// ClaimDecided is a claim_decided body.
type ClaimDecided struct {
	SimTime *string `json:"sim_time"`
	Claim   Claim   `json:"claim"`
}

// PayoutEvent is a payout_sent or payout_confirmed body. SimTime is the
// replay clock when one is running, else null.
type PayoutEvent struct {
	SimTime *string `json:"sim_time"`
	Payout  Payout  `json:"payout"`
}

// ReplayEnded is a replay_ended body.
type ReplayEnded struct {
	SimTime  string `json:"sim_time"`
	ReplayID string `json:"replay_id"`
}

// Snapshot is the first message on every stream connection.
type Snapshot struct {
	ReplayID   *string       `json:"replay_id"`
	ScenarioID *string       `json:"scenario_id"`
	SimTime    *string       `json:"sim_time"`
	OpenEvents []OutageEvent `json:"open_events"`
	Series     []Tick        `json:"series"`
	Claims     []ClaimItem   `json:"claims"`
}
