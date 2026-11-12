/**
 * Types for every request, response, stream event and artifact in CONTRACTS.md,
 * sections 3, 4, 5, 6 and 8 (8.1 pins the artifact shapes). Field names match the contract exactly.
 *
 * Version: CONTRACTS.md 1.8.
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

/** Cover a member can choose (quotes and new policies never accept the test cover). */
export type QuotableCoverCents = 5000 | 15000 | 30000;
export const COVER_OPTIONS_CENTS: readonly QuotableCoverCents[] = [5000, 15000, 30000];
/** Section 3: 100 cents is the seeded sandbox test cover (model_version "test-cover"). */
export const TEST_COVER_CENTS = 100;
export const TEST_COVER_MODEL_VERSION = 'test-cover';
export type CoverCents = QuotableCoverCents | typeof TEST_COVER_CENTS;

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
  /** Null for R5, which has no outage event (CONTRACTS 1.4). */
  outage_event_id: OutageEventId | null;
  tier: ClaimTier;
  rule: ClaimRule;
  decision: ClaimDecision;
  amount_cents: Cents;
  /** One plain sentence. */
  reason: string;
  decided_at: IsoTime;
  sim_time: IsoTime | null;
  /** R5 only: the power_lost / attest_lost entry that started the witness interval. Null otherwise (1.4). */
  witness_entry_id: WitnessEntryId | null;
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
  /** Null until the data_quality.json row shape is pinned (CONTRACTS 1.4). */
  data_coverage: number | null;
  /** False when the county has no price (1.8). Quotes and policies for it return 422 county_not_offered. */
  offered: boolean;
  /** One sentence, null when offered (1.8). */
  not_offered_reason: string | null;
}

export interface CountiesResponse {
  counties: County[];
}

export interface QuoteRequest {
  fips: Fips;
  cover_cents: QuotableCoverCents;
}

export interface QuoteBreakdown {
  expected_events_per_year: number;
  /** The t4 schedule share, the only tier paid (1.8). */
  expected_payout_share: number;
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
  cover_cents: QuotableCoverCents;
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

/** GET /api/demo (1.8). policy_id null when no demo policy exists. */
export type DemoResponse =
  | { policy_id: PolicyId; member_name: string; fips: Fips; cover_cents: CoverCents; premium_cents: Cents }
  | { policy_id: null };

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
  /** The scenario file's `source`, unchanged (CONTRACTS 1.4, shape 8.1). */
  source: ScenarioSource;
  /** The scenario file's provenance, unchanged (CONTRACTS 1.4). */
  provenance: Provenance;
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

/**
 * One point of a county index series (same shape as a scenario series point, section 8).
 * A missing 15-minute interval has customers_out and share null (CONTRACTS 1.3). Draw a gap.
 */
export interface SeriesPoint {
  t: IsoTime;
  customers_out: number | null;
  share: number | null;
}

/** GET /api/claims/{id}. The payout is inside `claim`. */
export interface ClaimDetailResponse {
  claim: ClaimListItem;
  /** Null for R5, which has no outage event (CONTRACTS 1.4). */
  outage_event: OutageEvent | null;
  /** Covers the event window, or for R5 the witness interval. May be empty if there are no readings. */
  index_series: SeriesPoint[];
  /** The entries the rule engine used. */
  witness_entries: WitnessEntryRow[];
}

export interface LedgerResponse {
  balance_cents: Cents;
  entries: LedgerEntry[];
}

// ---------- section 8 / 8.1: artifacts (served unchanged by /api/analytics/*) ----------
// Any value may be null only with its reason in provenance.notes (8.1), so numbers are `| null`.

type N = number | null;

export interface TriggerGridRow {
  theta_lo: number;
  theta_hi: number;
  /** Per member-year, in units of one full scheduled payout, training county-years only. */
  expected_wrong_payouts: N;
  expected_missed_payouts: N;
  /** wrong + missed; the chosen pair is its minimum. */
  objective: N;
  chosen: boolean;
}

/** At the grid edge the UI says so and never calls the value an optimum (8.1, 1.7). */
export type ThetaHiStatus = 'interior_optimum' | 'grid_edge_no_interior_optimum';

/** The chosen pair for one combination of the product assumptions (8.1, 1.7). */
export interface TriggerSensitivityRow {
  witness_share: number;
  false_confirmation_rate: number;
  theta_lo: N;
  theta_hi: N;
  theta_hi_status: ThetaHiStatus;
  objective: N;
}

export interface TriggerParamsArtifact {
  theta_hi: N;
  theta_lo: N;
  gap_bridge_minutes: N;
  /** Hours clause (SPEC section 4), 72. */
  event_merge_hours: N;
  /** Partial-feed dropout rule (section 3, 1.6): a fall to at most this fraction counts as missing. */
  dropout_max_fraction: N;
  theta_hi_status: ThetaHiStatus;
  sensitivity: TriggerSensitivityRow[];
  grid: TriggerGridRow[];
  provenance: Provenance;
}

export interface PricingCounty {
  expected_events_per_year: N;
  prob_premises_affected: N;
  credibility_weight: N;
  /** 1.8: counties with no qualifying event in their credibility group are not offered. */
  offered: boolean;
  /** One sentence, null when offered. */
  not_offered_reason: string | null;
}

export interface PricingTableArtifact {
  model_version: string;
  /** Keyed by FIPS. */
  counties: Record<Fips, PricingCounty>;
  rates: {
    risk_margin_rate: N;
    expense_load_cents: Cents | null;
  };
  /** Only the tiers the backend pays (1.8): t4 only. */
  payout_schedule: {
    t4: number;
  };
  provenance: Provenance;
}

export type FrequencyModel = 'nb_glm' | 'poisson_glm' | 'historical_mean';

export interface CalibrationBin {
  bin: number;
  n: number;
  predicted_mean: N;
  realised_mean: N;
}

export interface FrequencyVariant {
  county_years_fitted: number;
  per_county_year: { fips: Fips; year: number; realised: N; nb_glm: N; poisson_glm: N; historical_mean: N }[];
  /** Out-of-sample Poisson deviance summed over test county-months. */
  deviance: Record<FrequencyModel, N>;
  /** Mean negative log predictive likelihood per test county-month (lower is better). */
  log_score: Record<FrequencyModel, N>;
  calibration: Record<FrequencyModel, CalibrationBin[]>;
  beats_baselines: boolean | null;
}

export type FrequencyVariantName = 'exclude_low_coverage' | 'include_low_coverage';

export interface BacktestArtifact {
  frequency: {
    threshold: string;
    theta: N;
    unit: string;
    primary_variant: FrequencyVariantName;
    variants: Record<FrequencyVariantName, FrequencyVariant | null>;
  } | null;
  severity: {
    threshold_hours: number;
    n_events_train: N;
    n_events_test: N;
    gpd: { shape: N; scale: N; location_hours: N };
    ks_statistic: N;
    ks_pvalue: N;
    prob_24h_given_event: { fitted: N; empirical_train: N; empirical_test: N };
    qq: { empirical_hours: number; model_hours: number }[];
  } | null;
  credibility: {
    method: string;
    k: N;
    counties: Record<Fips, { state: string; county_rate: N; state_rate: N; exposure_months: N; weight: N; blended_rate: N }>;
  } | null;
  pool: {
    members: number;
    years: {
      year: number;
      premiums_cents: Cents | null;
      payouts_cents: Cents | null;
      loss_ratio: N;
      events: N;
      events_by_county: Record<Fips, number>;
      claims_paid: N;
    }[];
  } | null;
  provenance: Provenance;
}

export interface SolvencyArtifact {
  members: number;
  simulations: number;
  /** 1.8: what capital the pool starts with and what counts as ruin. */
  capital_assumption: {
    initial_capital_cents: Cents | null;
    description: string;
  };
  ruin_probability: N;
  capital_1_in_200_cents: Cents | null;
  stop_loss: {
    attachment_cents: Cents | null;
    limit_cents: Cents | null;
    price_cents: Cents | null;
  };
  annual_loss_quantiles: { q: number; loss_cents: Cents | null }[];
  provenance: Provenance;
}

export type BasisRiskScheme = 'index_only' | 'rules_perfect_witness' | 'rules_10pct_missing' | 'rules_30pct_missing';

export interface BasisRiskSchemeResult {
  premises_events: N;
  paid: N;
  missed: N;
  wrong: N;
  /** missed / premises_events */
  missed_rate: N;
  /** wrong / paid */
  wrong_rate: N;
}

/** Caveats (perfect witness, upper bound) are in provenance.notes. */
export interface BasisRiskArtifact {
  schemes: Record<BasisRiskScheme, BasisRiskSchemeResult | null>;
  districts_used: { state: string; district: string; median_active_monitors: N }[];
  min_active_monitors: N;
  theta_hi: N;
  theta_lo: N;
  fit_period: { start: string; end: string };
  score_period: { start: string; end: string };
  provenance: Provenance;
}

export interface DataQualityRow {
  fips: Fips;
  county: string;
  state: string;
  year: number;
  county_customers: N;
  expected_intervals: N;
  intervals_present: N;
  coverage_share: N;
  zero_rows: N;
  max_customers_out: N;
  intervals_over_county_customers: N;
  in_model_universe: boolean;
  excluded_from_fitting: boolean;
  exclusion_reason: string | null;
}

export interface DataQualityFinding {
  id: string;
  finding: string;
}

export interface DataQualityArtifact {
  coverage_floor: N;
  rows: DataQualityRow[];
  year_summary: { year: number; rows: N; zero_rows: N; null_rows: N; off_grid_rows: N; first_t: IsoTime | null; last_t: IsoTime | null }[];
  gap_bridge_evidence: { events_examined: N; gaps: { minutes: number; count: number }[]; share_of_gaps_within_bridge: N };
  storm_windows: {
    id: string;
    fips: Fips;
    start: IsoTime;
    end: IsoTime;
    expected_intervals: N;
    intervals_present: N;
    zero_rows: N;
    longest_missing_minutes: N;
    notes: string;
  }[];
  timestamp_check: { assumed: string; storm: string; first_sharp_rise_utc: IsoTime | null; consistent_with_utc: boolean | null; evidence: string };
  county_coverage: Record<Fips, { data_coverage: N }>;
  /** 1.8: data checks and their outcome. Optional so an older artifact still renders. */
  findings?: DataQualityFinding[];
  provenance: Provenance;
}

/** Scenario `source` (8.1). */
export interface ScenarioSource {
  dataset: string;
  url: string;
  file: string;
  sha256: string;
}

export interface ScenarioArtifact {
  id: string;
  title: string;
  description: string;
  fips: Fips;
  county_customers: number;
  start: IsoTime;
  end: IsoTime;
  series: SeriesPoint[];
  source: ScenarioSource;
  provenance: Provenance;
}

export type AnalyticsName = 'backtest' | 'pricing' | 'solvency' | 'basis-risk' | 'trigger' | 'data-quality';

export interface AnalyticsArtifacts {
  backtest: BacktestArtifact;
  pricing: PricingTableArtifact;
  solvency: SolvencyArtifact;
  'basis-risk': BasisRiskArtifact;
  trigger: TriggerParamsArtifact;
  'data-quality': DataQualityArtifact;
}

// ---------- section 5: replay event stream ----------

export interface StreamEventBase {
  sim_time: IsoTime;
}

/** share and customers_out are null for a missing 15-minute interval (CONTRACTS 1.3). */
export interface TickEvent extends StreamEventBase {
  share: number | null;
  customers_out: number | null;
  fips: Fips;
}

/** May repeat for the same outage_event_id after an hours-clause merge: that reopens the event (1.4). */
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

/**
 * A provisional event reached 4 hours and merged into an earlier qualifying event by the hours
 * clause (1.5). Clients drop the provisional event; an event_opened for the target follows.
 */
export interface EventMergedEvent extends StreamEventBase {
  outage_event_id: OutageEventId;
  into_outage_event_id: OutageEventId;
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
  event_merged: EventMergedEvent;
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
  'event_merged',
  'witness_entry',
  'claim_decided',
  'payout_sent',
  'payout_confirmed',
  'replay_ended',
];

/** A stream message as a discriminated union: `{ type: 'tick', data: TickEvent }` etc. */
export type ReplayEvent = { [K in ReplayEventName]: { type: K; data: ReplayEventMap[K] } }[ReplayEventName];
