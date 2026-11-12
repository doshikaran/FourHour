/**
 * FIXTURE DATA. Every value here is made up so pages can render before the backend exists.
 * None of it is a model result, a price or real outage data. Names say "Fixture", dates are in the
 * year 2000, and the app shows a "Mock data" banner whenever these are served. Thresholds and
 * shares are round on purpose. Scenario IDs follow the real ones (derecho-harris-2024,
 * spoof-quiet) so the live view's flow can be built; their curves are invented shapes.
 */
import type {
  AnalyticsArtifacts,
  ClaimListItem,
  County,
  Member,
  OutageEvent,
  Payout,
  Policy,
  PremiumCharge,
  Provenance,
  ScenarioSummary,
  SeriesPoint,
  WitnessDevice,
  WitnessEntryRow,
} from '../api/types';

/** Prefix + "fixture" + 5 base32 letters = 12 characters, per CONTRACTS.md section 1. */
export function fixtureId<P extends string>(prefix: P, n: number): `${P}_${string}` {
  const alphabet = 'abcdefghijklmnopqrstuvwxyz234567';
  let s = '';
  let v = n;
  for (let i = 0; i < 5; i++) {
    s = alphabet[v % 32] + s;
    v = Math.floor(v / 32);
  }
  return `${prefix}_fixture${s}`;
}

export const FIXTURE_NOTE = 'FIXTURE: made-up value served by the frontend mock. Not a result.';

export const fixtureProvenance: Provenance = {
  generated_at: '2000-01-01T00:00:00Z',
  git_commit: 'fixture',
  code: 'frontend/src/mock/fixtures.ts',
  data_sources: [{ name: 'Fixture source (not real data)', url: 'https://example.invalid/fixture', sha256: '0'.repeat(64), rows_used: 0 }],
  train_years: [2014, 2022],
  test_years: [2023, 2024],
  seed: 0,
  notes: FIXTURE_NOTE,
};

export const FIXTURE_FIPS = '99001';

export const fixtureCounties: County[] = [
  { fips: '99001', name: 'Fixture County A', state: 'ZZ', data_coverage: 0.5 },
  { fips: '99002', name: 'Fixture County B', state: 'ZZ', data_coverage: null },
  // The mock answers 422 pricing_not_computed for this one, to exercise that state.
  { fips: '99003', name: 'Fixture County C (pricing not computed)', state: 'ZZ', data_coverage: null },
];
export const FIXTURE_UNPRICED_FIPS = '99003';

export const T0 = '2000-01-01T00:00:00Z';

export const fixtureMember: Member = {
  id: fixtureId('mem', 1),
  name: 'Fixture Member One',
  email: 'fixture-member-1@example.invalid',
  paypal_email: 'fixture-payee-1@example.invalid',
  created_at: T0,
};

export const fixturePolicy: Policy = {
  id: fixtureId('pol', 1),
  member_id: fixtureMember.id,
  fips: FIXTURE_FIPS,
  cover_cents: 15000,
  premium_cents: 111,
  status: 'active',
  model_version: 'fixture-0',
  created_at: T0,
  cancelled_at: null,
};

/** Seeded sandbox test policy shape (CONTRACTS section 3): $1.00 cover, model_version test-cover. */
export const fixtureTestPolicy: Policy = {
  id: fixtureId('pol', 2),
  member_id: fixtureMember.id,
  fips: FIXTURE_FIPS,
  cover_cents: 100,
  premium_cents: 100,
  status: 'active',
  model_version: 'test-cover',
  created_at: T0,
  cancelled_at: null,
};

export const fixtureCharge: PremiumCharge = {
  id: fixtureId('chg', 1),
  policy_id: fixturePolicy.id,
  period: '2000-01',
  amount_cents: 111,
  request_id: `prem-${fixturePolicy.id}-2000-01`,
  paypal_order_id: 'FIXTURE-ORDER',
  paypal_capture_id: 'FIXTURE-CAPTURE',
  status: 'completed',
  created_at: T0,
};

export const fixtureDevice: WitnessDevice = {
  id: fixtureId('dev', 1),
  policy_id: fixturePolicy.id,
  public_key_jwk: { kty: 'EC', crv: 'P-256', x: 'FIXTURE-X', y: 'FIXTURE-Y' },
  registered_at: T0,
  last_seq: 0,
  last_hash: '0'.repeat(64),
};

export const fixtureOutageEvent: OutageEvent = {
  id: fixtureId('evt', 1),
  fips: FIXTURE_FIPS,
  scenario_id: 'derecho-harris-2024',
  lo_started_at: '2000-01-01T01:15:00Z',
  lo_ended_at: '2000-01-01T07:00:00Z',
  hi_started_at: '2000-01-01T02:00:00Z',
  hi_ended_at: '2000-01-01T06:30:00Z',
  peak_share: 0.6,
};

export const fixturePayout: Payout = {
  id: fixtureId('pay', 1),
  claim_id: fixtureId('clm', 1),
  sender_batch_id: `clm-${fixturePolicy.id}-${fixtureOutageEvent.id}-t4`,
  paypal_batch_id: 'FIXTURE-BATCH',
  paypal_item_id: 'FIXTURE-ITEM',
  amount_cents: 9000,
  status: 'success',
  created_at: '2000-01-01T06:15:00Z',
  confirmed_at: '2000-01-01T06:16:00Z',
  confirmed_via: 'poll',
};

export const fixtureClaim: ClaimListItem = {
  id: fixtureId('clm', 1),
  policy_id: fixturePolicy.id,
  outage_event_id: fixtureOutageEvent.id,
  tier: 't4',
  rule: 'R1',
  decision: 'paid',
  amount_cents: 9000,
  reason: 'Fixture claim: index and witness agree for 4 hours (made-up example).',
  decided_at: '2000-01-01T06:15:00Z',
  sim_time: '2000-01-01T06:15:00Z',
  witness_entry_id: null,
  payout: fixturePayout,
  member_name: fixtureMember.name,
  fips: FIXTURE_FIPS,
};

function fixtureEntry(n: number, seq: number, kind: WitnessEntryRow['kind'], t: string, charging: boolean): WitnessEntryRow {
  return {
    id: fixtureId('wen', n),
    device_id: fixtureDevice.id,
    seq,
    device_time: '2026-11-12T05:00:00Z',
    kind,
    charging,
    battery_level: 0.5,
    online: true,
    prev_hash: seq === 1 ? '0'.repeat(64) : 'e'.repeat(64),
    hash: (seq % 2 ? 'f' : 'e').repeat(64),
    sig: 'FIXTURE-SIGNATURE',
    received_at: '2026-11-12T05:00:01Z',
    sim_time: t,
    valid: true,
    reject_reason: null,
  };
}

export const fixtureWitnessEntries: WitnessEntryRow[] = [
  fixtureEntry(1, 1, 'power_lost', '2000-01-01T02:15:00Z', false),
  fixtureEntry(2, 2, 'power_restored', '2000-01-01T07:30:00Z', true),
];

const fixtureSource = {
  dataset: 'FIXTURE curve invented in frontend/src/mock/fixtures.ts (not EAGLE-I)',
  url: 'https://example.invalid/fixture',
  file: 'none',
  sha256: '0'.repeat(64),
};

export const fixtureScenarios: ScenarioSummary[] = [
  {
    id: 'derecho-harris-2024',
    title: 'FIXTURE stand-in for the derecho replay (made-up curve)',
    fips: FIXTURE_FIPS,
    start: '2000-01-01T00:00:00Z',
    end: '2000-01-02T12:00:00Z',
    description:
      'Made-up curve in Fixture County A with the derecho scenario ID, so the live view can be built: the index rises past both thresholds, holds for over 24 hours with a 1-hour data gap, falls, then a second 5-hour run within 72 hours merges into the same event.',
    is_fixture: true,
    source: fixtureSource,
    provenance: fixtureProvenance,
  },
  {
    id: 'spoof-quiet',
    title: 'FIXTURE stand-in for the spoof scenario (made-up curve)',
    fips: FIXTURE_FIPS,
    start: '2000-01-01T00:00:00Z',
    end: '2000-01-01T12:00:00Z',
    description: 'Made-up quiet index in Fixture County A. The fixture witness reports an outage anyway; R5 flags it for review.',
    is_fixture: true,
    source: fixtureSource,
    provenance: fixtureProvenance,
  },
];

export const FIXTURE_THETA_HI = 0.5;
export const FIXTURE_THETA_LO = 0.25;
export const FIXTURE_GAP_BRIDGE_MINUTES = 120;
export const FIXTURE_COUNTY_CUSTOMERS = 1000;
export const STEP_MS = 15 * 60 * 1000;

function toIso(ms: number): string {
  return new Date(ms).toISOString().replace(/\.\d{3}Z$/, 'Z');
}

function shareSeries(start: string, shares: (number | null)[]): SeriesPoint[] {
  const t0 = Date.parse(start);
  return shares.map((share, i) => ({
    t: toIso(t0 + i * STEP_MS),
    customers_out: share === null ? null : Math.round(share * FIXTURE_COUNTY_CUSTOMERS),
    share,
  }));
}

/**
 * Derecho-shaped fixture, 36 hours at 15-minute steps (144 points). Index:
 * i 0-3 quiet; 4-7 ramp; 8-107 above the strong threshold (a 1-hour gap at 40-43);
 * 108-115 falling; 116-119 quiet; 120-139 above the lenient threshold only (a second run that
 * reaches 4 hours and merges into the first event by the hours clause); 140-143 quiet.
 */
export function derechoFixtureSeries(): SeriesPoint[] {
  const shares: (number | null)[] = [];
  for (let i = 0; i < 144; i++) {
    let v: number | null;
    if (i < 4) v = 0.02;
    else if (i < 8) v = [0.1, 0.2, 0.3, 0.4][i - 4] as number;
    else if (i < 108) v = i >= 40 && i < 44 ? null : 0.55 + ((i * 7) % 10) / 100;
    else if (i < 116) v = [0.45, 0.4, 0.35, 0.3, 0.25, 0.2, 0.1, 0.05][i - 108] as number;
    else if (i < 120) v = 0.03;
    else if (i < 140) v = 0.3;
    else v = 0.02;
    shares.push(v);
  }
  return shareSeries('2000-01-01T00:00:00Z', shares);
}

/** Quiet fixture, 12 hours (48 points), share 1-2% throughout. */
export function spoofFixtureSeries(): SeriesPoint[] {
  return shareSeries(
    '2000-01-01T00:00:00Z',
    Array.from({ length: 48 }, (_, i) => (i % 3 === 0 ? 0.02 : 0.01)),
  );
}

export function scenarioSeries(id: string): SeriesPoint[] | null {
  if (id === 'derecho-harris-2024') return derechoFixtureSeries();
  if (id === 'spoof-quiet') return spoofFixtureSeries();
  return null;
}

export const fixtureAnalytics: AnalyticsArtifacts = {
  trigger: {
    theta_hi: FIXTURE_THETA_HI,
    theta_lo: FIXTURE_THETA_LO,
    gap_bridge_minutes: FIXTURE_GAP_BRIDGE_MINUTES,
    event_merge_hours: 72,
    dropout_max_fraction: 0.1,
    // Mirrors the real artifact's status so the grid-edge wording is exercised in mock mode.
    theta_hi_status: 'grid_edge_no_interior_optimum',
    sensitivity: [
      { witness_share: 0.5, false_confirmation_rate: 0.1, theta_lo: FIXTURE_THETA_LO, theta_hi: FIXTURE_THETA_HI, theta_hi_status: 'grid_edge_no_interior_optimum', objective: null },
      { witness_share: 0.25, false_confirmation_rate: 0.1, theta_lo: FIXTURE_THETA_LO, theta_hi: FIXTURE_THETA_HI, theta_hi_status: 'interior_optimum', objective: null },
    ],
    grid: [],
    provenance: fixtureProvenance,
  },
  pricing: {
    model_version: 'fixture-0',
    counties: {
      '99001': { expected_events_per_year: null, prob_24h_given_event: null, prob_premises_affected: null, credibility_weight: null },
    },
    rates: { risk_margin_rate: null, expense_load_cents: null },
    payout_schedule: { t4: 0.6, t24: 1.0 },
    provenance: fixtureProvenance,
  },
  // 8.1 shapes with every metric null, so the evidence panels show "not computed" in mock mode.
  backtest: { frequency: null, severity: null, credibility: null, pool: null, provenance: fixtureProvenance },
  solvency: {
    members: 0,
    simulations: 0,
    ruin_probability: null,
    capital_1_in_200_cents: null,
    stop_loss: { attachment_cents: null, limit_cents: null, price_cents: null },
    annual_loss_quantiles: [],
    provenance: fixtureProvenance,
  },
  'basis-risk': {
    schemes: { index_only: null, rules_perfect_witness: null, rules_10pct_missing: null, rules_30pct_missing: null },
    districts_used: [],
    min_active_monitors: null,
    theta_hi: null,
    theta_lo: null,
    fit_period: { start: '2000-01-01', end: '2000-06-30' },
    score_period: { start: '2000-07-01', end: '2000-12-31' },
    provenance: fixtureProvenance,
  },
  'data-quality': {
    coverage_floor: null,
    rows: [],
    year_summary: [],
    gap_bridge_evidence: { events_examined: null, gaps: [], share_of_gaps_within_bridge: null },
    storm_windows: [],
    timestamp_check: { assumed: 'UTC', storm: 'Fixture storm (not real)', first_sharp_rise_utc: null, consistent_with_utc: null, evidence: FIXTURE_NOTE },
    county_coverage: {},
    provenance: fixtureProvenance,
  },
};
