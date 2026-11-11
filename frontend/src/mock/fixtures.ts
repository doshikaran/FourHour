/**
 * FIXTURE DATA. Every value here is made up so pages can render before the backend exists.
 * None of it is a model result, a price or real outage data. Names say "Fixture" and the app shows
 * a "Mock data" banner whenever these are served. Thresholds and shares are round on purpose.
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
  { fips: '99002', name: 'Fixture County B', state: 'ZZ', data_coverage: 0.5 },
  { fips: '99003', name: 'Fixture County C', state: 'ZZ', data_coverage: 0.5 },
];

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
  scenario_id: 'fixture-replay',
  lo_started_at: '2000-01-01T01:00:00Z',
  lo_ended_at: '2000-01-01T07:00:00Z',
  hi_started_at: '2000-01-01T01:30:00Z',
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
  created_at: '2000-01-01T05:30:00Z',
  confirmed_at: '2000-01-01T05:31:00Z',
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
  decided_at: '2000-01-01T05:30:00Z',
  sim_time: '2000-01-01T05:30:00Z',
  payout: fixturePayout,
  member_name: fixtureMember.name,
  fips: FIXTURE_FIPS,
};

export const fixtureWitnessEntries: WitnessEntryRow[] = [
  {
    id: fixtureId('wen', 1),
    device_id: fixtureDevice.id,
    seq: 1,
    device_time: '2000-01-01T01:00:00Z',
    kind: 'power_lost',
    charging: false,
    battery_level: 0.5,
    online: true,
    prev_hash: '0'.repeat(64),
    hash: 'f'.repeat(64),
    sig: 'FIXTURE-SIGNATURE',
    received_at: '2000-01-01T01:00:01Z',
    sim_time: '2000-01-01T01:30:00Z',
    valid: true,
    reject_reason: null,
  },
];

export const fixtureScenarios: ScenarioSummary[] = [
  {
    id: 'fixture-replay',
    title: 'Fixture replay (not real data)',
    fips: FIXTURE_FIPS,
    start: '2000-01-01T00:00:00Z',
    end: '2000-01-01T09:00:00Z',
    description: 'A made-up outage curve served by the frontend mock so the live page can be built before real scenarios exist.',
    is_fixture: true,
  },
];

export const FIXTURE_THETA_HI = 0.5;
export const FIXTURE_THETA_LO = 0.25;
export const FIXTURE_COUNTY_CUSTOMERS = 1000;

/** A made-up index curve at 15-minute steps: rises, holds above theta_hi for 5 hours, falls. */
export function fixtureSeries(): SeriesPoint[] {
  const shape = [
    0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.55, 0.6, 0.6, 0.6, 0.6, 0.6, 0.6, 0.6, 0.6, 0.6, 0.6, 0.6, 0.6, 0.6, 0.6, 0.6, 0.6,
    0.6, 0.6, 0.55, 0.5, 0.4, 0.3, 0.2, 0.1, 0, 0, 0, 0, 0,
  ];
  const start = Date.parse('2000-01-01T00:00:00Z');
  return shape.map((share, i) => ({
    t: new Date(start + i * 15 * 60 * 1000).toISOString().replace(/\.\d{3}Z$/, 'Z'),
    customers_out: Math.round(share * FIXTURE_COUNTY_CUSTOMERS),
    share,
  }));
}

export const fixtureAnalytics: AnalyticsArtifacts = {
  trigger: {
    theta_hi: FIXTURE_THETA_HI,
    theta_lo: FIXTURE_THETA_LO,
    gap_bridge_minutes: 30,
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
  backtest: { frequency: null, severity: null, credibility: null, pool: null, provenance: fixtureProvenance },
  solvency: {
    members: 0,
    simulations: 0,
    ruin_probability: null,
    capital_1_in_200_cents: null,
    stop_loss: { attachment_cents: null, limit_cents: null, price_cents: null },
    annual_loss_quantiles: null,
    provenance: fixtureProvenance,
  },
  'basis-risk': { provenance: fixtureProvenance },
  'data-quality': { provenance: fixtureProvenance },
};
