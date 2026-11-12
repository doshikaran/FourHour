/** The /live stream reducer: ticks, gaps, merges, claims, payout timing and snapshot recovery. */
import { describe, expect, it } from 'vitest';
import type { Claim, ClaimListItem, Payout, ReplayEvent, SnapshotEvent, TickEvent } from '../api/types';
import { CONFIRMED_HOLD_MS, focusClaim, initialLiveState, liveReducer, type LiveState } from '../live/liveState';

const tick = (t: string, share: number | null): ReplayEvent => ({
  type: 'tick',
  data: { sim_time: t, share, customers_out: share === null ? null : Math.round(share * 1000), fips: '99001' },
});

const claim: Claim = {
  id: 'clm_fixtureaaaab',
  policy_id: 'pol_fixtureaaaab',
  outage_event_id: 'evt_fixtureaaaab',
  tier: 't4',
  rule: 'R1',
  decision: 'paid',
  amount_cents: 9000,
  reason: 'Fixture reason.',
  decided_at: '2000-01-01T06:00:00Z',
  sim_time: '2000-01-01T06:00:00Z',
  witness_entry_id: null,
};
const payout = (status: Payout['status']): Payout => ({
  id: 'pay_fixtureaaaab',
  claim_id: claim.id,
  sender_batch_id: 'clm-x',
  paypal_batch_id: 'B',
  paypal_item_id: 'I',
  amount_cents: 9000,
  status,
  created_at: '2026-11-12T00:00:00Z',
  confirmed_at: status === 'success' ? '2026-11-12T00:00:22Z' : null,
  confirmed_via: status === 'success' ? 'webhook' : null,
});

function run(events: ReplayEvent[], start: LiveState = initialLiveState(), at = 1000): LiveState {
  return events.reduce((s, e, i) => liveReducer(s, { type: 'event', event: e, at: at + i }), start);
}

const snap = (over: Partial<SnapshotEvent> = {}): ReplayEvent => ({
  type: 'snapshot',
  data: { replay_id: 'r1', scenario_id: 'derecho-harris-2024', sim_time: '2000-01-01T00:30:00Z', open_events: [], series: [], claims: [], ...over },
});

describe('liveReducer', () => {
  it('appends ticks, keeps null ticks as gaps, and ignores duplicates', () => {
    const s = run([snap(), tick('2000-01-01T00:00:00Z', 0.1), tick('2000-01-01T00:15:00Z', null), tick('2000-01-01T00:15:00Z', null), tick('2000-01-01T00:30:00Z', 0.2)]);
    expect(s.series.map((p) => p.share)).toEqual([0.1, null, 0.2]);
    expect(s.simTime).toBe('2000-01-01T00:30:00Z');
  });

  it('treats a repeated event_opened as reopening, and event_merged drops the provisional event', () => {
    const s = run([
      snap(),
      { type: 'event_opened', data: { sim_time: 'a', outage_event_id: 'evt_first', threshold: 'lo' } },
      { type: 'event_closed', data: { sim_time: 'b', outage_event_id: 'evt_first', threshold: 'lo', duration_hours: 26 } },
      { type: 'event_opened', data: { sim_time: 'c', outage_event_id: 'evt_prov', threshold: 'lo' } },
      { type: 'event_merged', data: { sim_time: 'd', outage_event_id: 'evt_prov', into_outage_event_id: 'evt_first' } },
      { type: 'event_opened', data: { sim_time: 'd', outage_event_id: 'evt_first', threshold: 'lo' } },
    ]);
    expect(s.events.map((e) => e.id)).toEqual(['evt_first']);
    expect(s.events[0]).toMatchObject({ loOpen: true, reopenings: 1 });
    expect(s.feed.some((f) => /hours clause/.test(f.text))).toBe(true);
  });

  it('tracks a claim through sending to confirmed, recording when this browser saw each step', () => {
    let s = run([snap(), { type: 'claim_decided', data: { sim_time: claim.sim_time as string, claim } }]);
    expect(focusClaim(s)?.payout).toBeNull();
    s = liveReducer(s, { type: 'event', event: { type: 'payout_sent', data: { sim_time: 'x', payout: payout('pending') } }, at: 5000 });
    expect(focusClaim(s)).toMatchObject({ payoutSeenAt: 5000, settledSeenAt: null });
    s = liveReducer(s, { type: 'event', event: { type: 'payout_confirmed', data: { sim_time: 'y', payout: payout('success') } }, at: 27000 });
    const f = focusClaim(s);
    expect(f?.payout?.status).toBe('success');
    expect(((f?.settledSeenAt ?? 0) - (f?.payoutSeenAt ?? 0)) / 1000).toBe(22);
  });

  it('rebuilds from a snapshot of the same replay after a drop, keeping the feed and seen-at times', () => {
    const item: ClaimListItem = { ...claim, payout: payout('pending'), member_name: 'Fixture Member One', fips: '99001' };
    let s = run([snap(), tick('2000-01-01T00:00:00Z', 0.1), { type: 'claim_decided', data: { sim_time: 'x', claim } }]);
    s = liveReducer(s, { type: 'event', event: { type: 'payout_sent', data: { sim_time: 'x', payout: payout('pending') } }, at: 5000 });
    s = liveReducer(s, { type: 'connection_lost', at: 6000 });
    const series: TickEvent[] = [
      { sim_time: '2000-01-01T00:00:00Z', share: 0.1, customers_out: 100, fips: '99001' },
      { sim_time: '2000-01-01T00:15:00Z', share: null, customers_out: null, fips: '99001' },
      { sim_time: '2000-01-01T00:30:00Z', share: 0.3, customers_out: 300, fips: '99001' },
    ];
    s = liveReducer(s, { type: 'event', event: snap({ series, claims: [{ ...item, payout: payout('success') }] }), at: 30000 });
    expect(s.series).toHaveLength(3);
    expect(s.claims).toHaveLength(1);
    expect(s.claims[0]).toMatchObject({ member_name: 'Fixture Member One', payoutSeenAt: 5000, settledSeenAt: 30000 });
    expect(s.feed.some((f) => /dropped/.test(f.text))).toBe(true);
    expect(s.feed.some((f) => /rebuilt from the server snapshot/.test(f.text))).toBe(true);
  });

  it('starts fresh on a snapshot for a different replay', () => {
    let s = run([snap(), tick('2000-01-01T00:00:00Z', 0.1), { type: 'claim_decided', data: { sim_time: 'x', claim } }]);
    s = run([snap({ replay_id: 'r2', series: [] })], s);
    expect(s.replayId).toBe('r2');
    expect(s.series).toEqual([]);
    expect(s.claims).toEqual([]);
  });

  it('keeps what is on screen when an idle snapshot arrives after the replay ended', () => {
    let s = run([snap(), tick('2000-01-01T00:00:00Z', 0.1), { type: 'claim_decided', data: { sim_time: 'x', claim } }]);
    s = run([snap({ replay_id: null, scenario_id: null, sim_time: null })], s);
    expect(s.ended).toBe(true);
    expect(s.claims).toHaveLength(1);
    expect(s.series).toHaveLength(1);
  });

  it('a started action resets the screen for the new replay', () => {
    let s = run([snap(), tick('2000-01-01T00:00:00Z', 0.1)]);
    s = liveReducer(s, { type: 'started', replayId: 'r9', scenarioId: 'spoof-quiet', simStart: 'a', simEnd: 'b', at: 1 });
    expect(s).toMatchObject({ replayId: 'r9', scenarioId: 'spoof-quiet', series: [], simStart: 'a', simEnd: 'b' });
  });

  it('holds a just-confirmed payout in focus, then moves to the oldest payout still sending', () => {
    const c2: Claim = { ...claim, id: 'clm_fixtureaaaac', tier: 't24' };
    const p2: Payout = { ...payout('pending'), id: 'pay_fixtureaaaac', claim_id: c2.id };
    let s = run([snap(), { type: 'claim_decided', data: { sim_time: 'x', claim } }]);
    s = liveReducer(s, { type: 'event', event: { type: 'payout_sent', data: { sim_time: 'x', payout: payout('pending') } }, at: 1_000 });
    s = liveReducer(s, { type: 'event', event: { type: 'claim_decided', data: { sim_time: 'y', claim: c2 } }, at: 20_000 });
    s = liveReducer(s, { type: 'event', event: { type: 'payout_sent', data: { sim_time: 'y', payout: p2 } }, at: 21_000 });
    // Both sending: the older (4-hour) one is in focus.
    expect(focusClaim(s, 22_000)?.claim.id).toBe(claim.id);
    s = liveReducer(s, { type: 'event', event: { type: 'payout_confirmed', data: { sim_time: 'z', payout: payout('success') } }, at: 23_000 });
    // Just confirmed: held in focus although the top-up is still sending.
    expect(focusClaim(s, 23_000 + CONFIRMED_HOLD_MS - 1)?.claim.id).toBe(claim.id);
    // After the hold, the top-up still sending takes over.
    expect(focusClaim(s, 23_000 + CONFIRMED_HOLD_MS)?.claim.id).toBe(c2.id);
  });

  it('adds member names and counties from the claims list', () => {
    let s = run([snap(), { type: 'claim_decided', data: { sim_time: 'x', claim } }]);
    s = liveReducer(s, { type: 'enrich', items: [{ id: claim.id, member_name: 'Fixture Member One', fips: '99001' }] });
    expect(s.claims[0]).toMatchObject({ member_name: 'Fixture Member One', fips: '99001' });
  });
});
