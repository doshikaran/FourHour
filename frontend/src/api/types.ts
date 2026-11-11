/**
 * Types for every request, response, stream event and artifact in CONTRACTS.md v1.1,
 * sections 3, 4, 5, 6 and 8. Field names match the contract exactly.
 *
 * Version: CONTRACTS.md 1.2.
 *
 * Where the contract still leaves a shape open (section 8, note under the table), the type is
 * `JsonValue` marked `TODO(contract)`. Do not narrow those by guessing.
 *
 * Nullability follows section 3: a field that only gets a value later (PayPal IDs, end times,
 * cancelled_at, confirmed_at, confirmed_via, sim_time, reject_reason) is `| null`; every other
 * field is required. Artifact numbers are `| null` because section 8 writes null for anything
 * that could not be computed.
 */

// ---------- section 1: conventions ----------

/** ISO 8601 UTC with a Z suffix, e.g. "2024-07-08T09:15:00Z". */
export type IsoTime = string;
/** Integer cents, USD. Never a float. */
export type Cents = number;
/** 5-digit county FIPS string, e.g. "48201". */
export type Fips = string;

export type MemberId = `mem_${string}`;
export type PolicyId = `pol_${string}`;
export type ChargeId = `chg_${string}`;
export type OutageEventId = `evt_${string}`;
export type DeviceId = `dev_${string}`;
export type ClaimId = `clm_${string}`;
export type PayoutId = `pay_${string}`;
export type RefundId = `rfd_${string}`;
export type WitnessEntryId = `wen_${string}`;
export type LedgerEntryId = `led_${string}`;

export type JsonValue = string | number | boolean | null | JsonValue[] | { [key: string]: JsonValue };

export interface ApiErrorBody {
  error: {
    code: string;
    message: string;
    paypal_debug_id: string | null;
  };
}

// ---------- section 3: rows ----------

export interface Member {
  id: MemberId;
  name: string;
  email: string;
  /** The sandbox personal account that receives payouts. */
  paypal_email: string;
  created_at: IsoTime;
}

export type CoverCents = 5000 | 15000 | 30000;
export const COVER_OPTIONS_CENTS: readonly CoverCents[] = [5000, 15000, 30000];

export type PolicyStatus = 'pending_vault' | 'active' | 'cancelled';

export interface Policy {
  id: PolicyId;
  member_id: MemberId;
  fips: Fips;
  cover_cents: CoverCents;
  premium_cents: Cents;
  status: PolicyStatus;
  // vault_setup_token and vault_payment_token are never in API responses (section 4).
  model_version: string;
  created_at: IsoTime;
  cancelled_at: IsoTime | null;
}

export type PremiumChargeStatus = 'created' | 'completed' | 'failed' | 'refunded';

export interface PremiumCharge {
  id: ChargeId;
  policy_id: PolicyId;
  /** YYYY-MM */
  period: string;
  amount_cents: Cents;
  request_id: string;
  paypal_order_id: string | null;
  paypal_capture_id: string | null;
  status: PremiumChargeStatus;
  created_at: IsoTime;
}

export interface OutageEvent {
  id: OutageEventId;
  fips: Fips;
  scenario_id: string | null;
  lo_started_at: IsoTime;
  lo_ended_at: IsoTime | null;
  hi_started_at: IsoTime | null;
  hi_ended_at: IsoTime | null;
  peak_share: number;
}

export interface PublicKeyJwk {
  kty: 'EC';
  crv: 'P-256';
  x: string;
  y: string;
}

export interface WitnessDevice {
  id: DeviceId;
  policy_id: PolicyId;
  public_key_jwk: PublicKeyJwk;
  registered_at: IsoTime;
  last_seq: number;
  last_hash: string;
}

// ---------- section 6: witness log entry (wire format) ----------

export type WitnessKind = 'heartbeat' | 'power_lost' | 'power_restored' | 'attest_lost' | 'attest_restored';
export const WITNESS_KINDS: readonly WitnessKind[] = [
  'heartbeat',
  'power_lost',
  'power_restored',
  'attest_lost',
  'attest_restored',
];

/** Fields that go into the hash input, in contract order. */
export interface WitnessEntryBody {
  seq: number;
  device_time: IsoTime;
  kind: WitnessKind;
  charging: boolean;
  /** 0 to 1, or null if unavailable. */
  battery_level: number | null;
  online: boolean;
  prev_hash: string;
}

export interface WitnessEntry extends WitnessEntryBody {
  /** SHA-256 lowercase hex of the hash input string. */
  hash: string;
  /** ECDSA P-256 raw r||s (64 bytes), base64url without padding. */
  sig: string;
}

export type WitnessRejectReason = 'bad_signature' | 'broken_chain' | 'bad_sequence' | 'time_went_backwards' | 'stale';

/** A stored witness entry (section 3 witness_entries). Rejected entries are stored too, valid=false. */
export interface WitnessEntryRow extends WitnessEntry {
  id: WitnessEntryId;
  device_id: DeviceId;
  received_at: IsoTime;
  sim_time: IsoTime | null;
  valid: boolean;
  reject_reason: WitnessRejectReason | null;
}

// ---------- section 3: claims, payouts, refunds, ledger ----------

export type ClaimTier = 't4' | 't24';
export type ClaimRule = 'R1' | 'R2' | 'R3' | 'R4' | 'R5';
export type ClaimDecision = 'paid' | 'declined' | 'review';

export interface Claim {
  id: ClaimId;
  policy_id: PolicyId;
  outage_event_id: OutageEventId;
  tier: ClaimTier;
  rule: ClaimRule;
  decision: ClaimDecision;
  amount_cents: Cents;
  /** One plain sentence. */
  reason: string;
  decided_at: IsoTime;
  sim_time: IsoTime | null;
}

export type PayoutStatus = 'created' | 'pending' | 'success' | 'failed' | 'unclaimed';

export interface Payout {
  id: PayoutId;
  claim_id: ClaimId;
  sender_batch_id: string;
  paypal_batch_id: string | null;
  paypal_item_id: string | null;
  amount_cents: Cents;
  status: PayoutStatus;
  created_at: IsoTime;
  confirmed_at: IsoTime | null;
  confirmed_via: 'webhook' | 'poll' | null;
}

export type RefundStatus = 'created' | 'completed' | 'failed';

export interface Refund {
  id: RefundId;
  premium_charge_id: ChargeId;
  amount_cents: Cents;
  request_id: string;
  paypal_refund_id: string | null;
  status: RefundStatus;
  created_at: IsoTime;
}

export type LedgerKind = 'premium' | 'payout' | 'refund';

export interface LedgerEntry {
  id: LedgerEntryId;
  at: IsoTime;
  kind: LedgerKind;
  /** Positive into the pool, negative out. */
  amount_cents: Cents;
  policy_id: PolicyId;
  /** The premium charge, payout or refund ID. */
  ref_id: ChargeId | PayoutId | RefundId;
}

// ---------- section 8: provenance ----------

export interface DataSource {
  name: string;
  url: string;
  sha256: string;
  rows_used: number;
}

export interface Provenance {
  generated_at: IsoTime;
  git_commit: string;
  code: string;
  data_sources: DataSource[];
  train_years: [number, number];
  test_years: [number, number];
  seed: number;
  notes: string;
}

// ---------- section 4: reference data and quotes ----------

/** Section 8 top-level artifact file names without .json. */
export type ArtifactFileName = 'trigger_params' | 'pricing_table' | 'backtest' | 'solvency' | 'basis_risk' | 'data_quality';

/** 200 body. A database outage is a 503 error body with code `database_unavailable`. */
export interface HealthResponse {
  status: 'ok';
  paypal_env: 'sandbox';
  artifacts_loaded: ArtifactFileName[];
}

export interface County {
  fips: Fips;
  name: string;
  state: string;
  data_coverage: number;
}

export interface CountiesResponse {
  counties: County[];
}

export interface QuoteRequest {
  fips: Fips;
  cover_cents: CoverCents;
}

export interface QuoteBreakdown {
  expected_events_per_year: number;
  prob_24h_given_event: number;
  prob_premises_affected: number;
  expected_annual_payout_cents: Cents;
  credibility_weight: number;
  risk_margin_rate: number;
  expense_load_cents: Cents;
}

export interface QuoteResponse {
  fips: Fips;
  cover_cents: CoverCents;
  premium_cents: Cents;
  model_version: string;
  breakdown: QuoteBreakdown;
  provenance: Provenance;
}

// ---------- section 4: members and policies ----------

export interface CreateMemberRequest {
  name: string;
  email: string;
  paypal_email: string;
}
export type CreateMemberResponse = Member;

export interface CreatePolicyRequest {
  member_id: MemberId;
  fips: Fips;
  cover_cents: CoverCents;
}

export interface CreatePolicyResponse {
  policy: Policy;
  approve_url: string;
}

export interface PolicyDetailResponse {
  policy: Policy;
  member: Member;
  premium_charges: PremiumCharge[];
  devices: WitnessDevice[];
  claims: Claim[];
}

export interface PoliciesResponse {
  policies: Policy[];
}

export interface ChargePremiumRequest {
  /** YYYY-MM */
  period: string;
}
export type ChargePremiumResponse = PremiumCharge;

export interface CancelPolicyResponse {
  policy: Policy;
  refund: Refund | null;
}

/** Query parameters the vault return redirect adds to /policies/:id (section 4). */
export interface VaultReturnQuery {
  enrolled: '1' | '0';
  error?: string;
}

// ---------- section 4: witness ----------

export interface RegisterDeviceRequest {
  policy_id: PolicyId;
  public_key_jwk: PublicKeyJwk;
}

export interface RegisterDeviceResponse {
  device_id: DeviceId;
  genesis_hash: string;
}

export interface PostEntriesRequest {
  device_id: DeviceId;
  entries: WitnessEntry[];
}

export interface PostEntriesResponse {
  accepted: number;
  rejected: { seq: number; reason: WitnessRejectReason }[];
  last_seq: number;
  last_hash: string;
}

export interface DeviceEntriesResponse {
  entries: WitnessEntryRow[];
}

// ---------- section 4: replay ----------

export interface ScenarioSummary {
  id: string;
  title: string;
  fips: Fips;
  start: IsoTime;
  end: IsoTime;
  description: string;
  /** True for fixture scenarios named `fixture-*`; the UI labels them. */
  is_fixture: boolean;
}

export interface ScenariosResponse {
  scenarios: ScenarioSummary[];
}

export interface StartReplayRequest {
  scenario_id: string;
  /** Simulated seconds per real second. */
  speed: number;
}

export interface StartReplayResponse {
  replay_id: string;
  sim_start: IsoTime;
  sim_end: IsoTime;
}

export interface StopReplayResponse {
  stopped: true;
}

export interface ResetReplayResponse {
  reset: true;
}

// ---------- section 4: claims, ledger ----------

export interface ClaimListItem extends Claim {
  payout: Payout | null;
  member_name: string;
  fips: Fips;
}

export interface ClaimsResponse {
  claims: ClaimListItem[];
}

/** One point of a county index series (same shape as a scenario series point, section 8). */
export interface SeriesPoint {
  t: IsoTime;
  customers_out: number;
  share: number;
}

/** GET /api/claims/{id}. The payout is inside `claim`. */
export interface ClaimDetailResponse {
  claim: ClaimListItem;
  outage_event: OutageEvent;
  /** Covers the event window. */
  index_series: SeriesPoint[];
  /** The entries the rule engine used. */
  witness_entries: WitnessEntryRow[];
}

export interface LedgerResponse {
  balance_cents: Cents;
  entries: LedgerEntry[];
}

// ---------- section 8: artifacts (served unchanged by /api/analytics/*) ----------

/** TODO(contract): the shape of one grid row ("every candidate pair with its expected wrong and missed payouts"). */
export type TodoTriggerGridRow = JsonValue;

export interface TriggerParamsArtifact {
  theta_hi: number | null;
  theta_lo: number | null;
  gap_bridge_minutes: number | null;
  grid: TodoTriggerGridRow[];
  provenance: Provenance;
}

export interface PricingCounty {
  expected_events_per_year: number | null;
  prob_24h_given_event: number | null;
  prob_premises_affected: number | null;
  credibility_weight: number | null;
}

export interface PricingTableArtifact {
  model_version: string;
  /** Keyed by FIPS. */
  counties: Record<Fips, PricingCounty>;
  rates: {
    risk_margin_rate: number | null;
    expense_load_cents: Cents | null;
  };
  payout_schedule: {
    t4: number;
    t24: number;
  };
  provenance: Provenance;
}

/** TODO(contract): inner shapes of frequency, severity, credibility and pool still open (section 8 note). */
export interface BacktestArtifact {
  frequency: JsonValue;
  severity: JsonValue;
  credibility: JsonValue;
  pool: JsonValue;
  provenance: Provenance;
}

export interface SolvencyArtifact {
  members: number;
  simulations: number;
  ruin_probability: number | null;
  capital_1_in_200_cents: Cents | null;
  stop_loss: {
    attachment_cents: Cents | null;
    limit_cents: Cents | null;
    price_cents: Cents | null;
  };
  /** TODO(contract): shape still open (section 8 note). */
  annual_loss_quantiles: JsonValue;
  provenance: Provenance;
}

export type BasisRiskScheme = 'index_only' | 'rules_perfect_witness' | 'rules_10pct_missing' | 'rules_30pct_missing';

/**
 * TODO(contract): where the four schemes sit, their count keys, and the types of districts_used,
 * fit_period and score_period are still open (section 8 note). Caveats are in provenance.notes.
 */
export interface TodoBasisRiskArtifact {
  [key: string]: JsonValue | Provenance;
  provenance: Provenance;
}

/** TODO(contract): the per county-year row key is still open (section 8 note). */
export interface TodoDataQualityArtifact {
  [key: string]: JsonValue | Provenance;
  provenance: Provenance;
}

/** TODO(contract): the type of `source`, and how a missing 15-minute row appears in `series`, are still open. */
export interface ScenarioArtifact {
  id: string;
  title: string;
  description: string;
  fips: Fips;
  county_customers: number;
  start: IsoTime;
  end: IsoTime;
  series: SeriesPoint[];
  source: JsonValue;
  provenance: Provenance;
}

export type AnalyticsName = 'backtest' | 'pricing' | 'solvency' | 'basis-risk' | 'trigger' | 'data-quality';

export interface AnalyticsArtifacts {
  backtest: BacktestArtifact;
  pricing: PricingTableArtifact;
  solvency: SolvencyArtifact;
  'basis-risk': TodoBasisRiskArtifact;
  trigger: TriggerParamsArtifact;
  'data-quality': TodoDataQualityArtifact;
}

// ---------- section 5: replay event stream ----------

export interface StreamEventBase {
  sim_time: IsoTime;
}

export interface TickEvent extends StreamEventBase {
  share: number;
  customers_out: number;
  fips: Fips;
}

export interface EventOpenedEvent extends StreamEventBase {
  outage_event_id: OutageEventId;
  threshold: 'lo' | 'hi';
}

export interface EventClosedEvent extends StreamEventBase {
  outage_event_id: OutageEventId;
  threshold: 'lo' | 'hi';
  duration_hours: number;
}

export interface WitnessEntryEvent extends StreamEventBase {
  device_id: DeviceId;
  policy_id: PolicyId;
  kind: WitnessKind;
  valid: boolean;
}

export interface ClaimDecidedEvent extends StreamEventBase {
  claim: Claim;
}

export interface PayoutEvent extends StreamEventBase {
  payout: Payout;
}

export interface ReplayEndedEvent extends StreamEventBase {
  replay_id: string;
}

/**
 * Sent first on every connect (section 5). When no replay is running, replay_id, scenario_id and
 * sim_time are null and the lists are empty. This is the one event whose sim_time can be null.
 */
export interface SnapshotEvent {
  replay_id: string | null;
  scenario_id: string | null;
  sim_time: IsoTime | null;
  open_events: OutageEvent[];
  /** Tick bodies so far. */
  series: TickEvent[];
  /** Claims decided in this replay, same shape as GET /api/claims items. */
  claims: ClaimListItem[];
}

export interface ReplayEventMap {
  snapshot: SnapshotEvent;
  tick: TickEvent;
  event_opened: EventOpenedEvent;
  event_closed: EventClosedEvent;
  witness_entry: WitnessEntryEvent;
  claim_decided: ClaimDecidedEvent;
  payout_sent: PayoutEvent;
  payout_confirmed: PayoutEvent;
  replay_ended: ReplayEndedEvent;
}

export type ReplayEventName = keyof ReplayEventMap;

export const REPLAY_EVENT_NAMES: readonly ReplayEventName[] = [
  'snapshot',
  'tick',
  'event_opened',
  'event_closed',
  'witness_entry',
  'claim_decided',
  'payout_sent',
  'payout_confirmed',
  'replay_ended',
];

/** A stream message as a discriminated union: `{ type: 'tick', data: TickEvent }` etc. */
export type ReplayEvent = { [K in ReplayEventName]: { type: K; data: ReplayEventMap[K] } }[ReplayEventName];
