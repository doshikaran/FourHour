import { afterEach, describe, expect, it } from 'vitest';
import { parseReplayEvent } from '../api/stream';
import type { ReplayEvent, ReplayEventName } from '../api/types';
import { createMockApi, type MockApi } from '../mock/mockApi';
import { DEFAULT_CONFIRM_DELAY_MS } from '../mock/mockReplay';

describe('parseReplayEvent', () => {
  it('accepts a well-formed tick', () => {
    const r = parseReplayEvent('tick', '{"sim_time":"2000-01-01T00:15:00Z","share":0.1,"customers_out":100,"fips":"99001"}');
    expect(r.ok && r.event.type).toBe('tick');
  });

  it('accepts a tick for a missing interval (share and customers_out null, CONTRACTS 1.3)', () => {
    const r = parseReplayEvent('tick', '{"sim_time":"2000-01-01T00:15:00Z","share":null,"customers_out":null,"fips":"99001"}');
    expect(r.ok && r.event.type === 'tick' && r.event.data.share).toBe(null);
  });

  it('accepts event_merged (CONTRACTS 1.5)', () => {
    const r = parseReplayEvent('event_merged', '{"sim_time":"t","outage_event_id":"evt_a","into_outage_event_id":"evt_b"}');
    expect(r.ok && r.event.type).toBe('event_merged');
  });

  it('accepts an idle snapshot with null sim_time and empty lists', () => {
    const r = parseReplayEvent('snapshot', '{"replay_id":null,"scenario_id":null,"sim_time":null,"open_events":[],"series":[],"claims":[]}');
    expect(r.ok && r.event.type).toBe('snapshot');
  });

  it.each([
    ['snapshot', '{"replay_id":null,"scenario_id":null,"sim_time":null,"open_events":[],"series":[]}', 'missing or wrong fields for snapshot'],
    ['tick', '{"sim_time":null,"share":0.1,"customers_out":1,"fips":"1"}', 'missing sim_time'],
    ['tick', 'not json', 'data is not JSON'],
    ['tick', '{"share":0.1,"customers_out":1,"fips":"1"}', 'missing sim_time'],
    ['tick', '{"sim_time":"t","share":"0.1","customers_out":1,"fips":"1"}', 'missing or wrong fields for tick'],
    ['tick', '{"sim_time":"t","customers_out":1,"fips":"1"}', 'missing or wrong fields for tick'],
    ['event_merged', '{"sim_time":"t","outage_event_id":"evt_a"}', 'missing or wrong fields for event_merged'],
    ['bogus', '{"sim_time":"t"}', 'unknown event "bogus"'],
  ])('rejects %s %s', (name, raw, reason) => {
    expect(parseReplayEvent(name, raw)).toEqual({ ok: false, reason });
  });
});

const until = (cond: () => boolean, ms = 8000) =>
  new Promise<void>((resolve, reject) => {
    const t0 = Date.now();
    const check = () => (cond() ? resolve() : Date.now() - t0 > ms ? reject(new Error('timed out')) : setTimeout(check, 10));
    check();
  });

describe('mock replay stream', () => {
  let api: MockApi | null = null;
  afterEach(() => api?.dispose());

  it('confirms a payout about 22 s after it is sent by default (D7: sandbox takes 20–25 s)', () => {
    expect(DEFAULT_CONFIRM_DELAY_MS).toBeGreaterThanOrEqual(20_000);
    expect(DEFAULT_CONFIRM_DELAY_MS).toBeLessThanOrEqual(25_000);
  });

  it('runs the derecho fixture: thresholds, a gap, a merge, both tiers, payouts confirmed in real time', async () => {
    api = createMockApi({ latencyMs: 0, payoutSendDelayMs: 50, payoutConfirmDelayMs: 400 });
    const events: { e: ReplayEvent; at: number }[] = [];
    const handle = api.openReplayStream({ onEvent: (e) => events.push({ e, at: Date.now() }) });
    await new Promise((r) => setTimeout(r, 10));
    await api.startReplay({ scenario_id: 'derecho-harris-2024', speed: 1_000_000 });
    await until(
      () => events.filter((x) => x.e.type === 'payout_confirmed').length === 2 && events.some((x) => x.e.type === 'replay_ended'),
      9000,
    );
    handle.close();

    const names = events.map((x) => x.e.type);
    const idx = (n: ReplayEventName) => names.indexOf(n);
    expect(names[0]).toBe('snapshot');
    // Missing intervals arrive as null ticks.
    expect(events.some((x) => x.e.type === 'tick' && x.e.data.share === null)).toBe(true);
    // Hours clause: a provisional event merges into the first, then the first is reopened.
    const merged = events.find((x) => x.e.type === 'event_merged');
    if (merged?.e.type !== 'event_merged') throw new Error('no event_merged');
    const firstOpen = events.find((x) => x.e.type === 'event_opened');
    if (firstOpen?.e.type !== 'event_opened') throw new Error('no event_opened');
    expect(merged.e.data.into_outage_event_id).toBe(firstOpen.e.data.outage_event_id);
    expect(merged.e.data.outage_event_id).not.toBe(firstOpen.e.data.outage_event_id);
    const after = events[events.indexOf(merged) + 1];
    expect(after?.e.type === 'event_opened' && after.e.data.outage_event_id).toBe(firstOpen.e.data.outage_event_id);
    // Both tiers, 4-hour first.
    const claims = events.flatMap((x) => (x.e.type === 'claim_decided' ? [x.e.data.claim] : []));
    expect(claims.map((c) => [c.tier, c.rule, c.decision])).toEqual([
      ['t4', 'R1', 'paid'],
      ['t24', 'R1', 'paid'],
    ]);
    const firstId = firstOpen.e.data.outage_event_id;
    expect(claims.every((c) => c.witness_entry_id === null && c.outage_event_id === firstId)).toBe(true);
    // Payout: sent, then confirmed after the configured real-time delay.
    const sent = events.find((x) => x.e.type === 'payout_sent');
    const conf = events.find((x) => x.e.type === 'payout_confirmed');
    if (!sent || !conf || sent.e.type !== 'payout_sent' || conf.e.type !== 'payout_confirmed') throw new Error('no payout events');
    expect(sent.e.data.payout.status).toBe('pending');
    expect(conf.e.data.payout.status).toBe('success');
    expect(conf.at - sent.at).toBeGreaterThanOrEqual(390);
    expect(idx('claim_decided')).toBeLessThan(idx('payout_sent'));
  }, 12_000);

  it('runs the spoof fixture: R5 flagged for review, no outage event, witness_entry_id set, no payout', async () => {
    api = createMockApi({ latencyMs: 0, payoutSendDelayMs: 10, payoutConfirmDelayMs: 10 });
    const events: ReplayEvent[] = [];
    const handle = api.openReplayStream({ onEvent: (e) => events.push(e) });
    await new Promise((r) => setTimeout(r, 10));
    await api.startReplay({ scenario_id: 'spoof-quiet', speed: 1_000_000 });
    await until(() => events.some((e) => e.type === 'replay_ended'));
    handle.close();
    const claim = events.find((e) => e.type === 'claim_decided');
    if (claim?.type !== 'claim_decided') throw new Error('no claim');
    expect(claim.data.claim).toMatchObject({ rule: 'R5', decision: 'review', amount_cents: 0, outage_event_id: null });
    expect(claim.data.claim.witness_entry_id).toMatch(/^wen_/);
    expect(events.some((e) => e.type === 'payout_sent' || e.type === 'event_opened')).toBe(false);
    const detail = await api.claim(claim.data.claim.id);
    expect(detail.outage_event).toBeNull();
  }, 10_000);

  it('sends a fresh snapshot after a drop, carrying the series and the claims decided so far', async () => {
    api = createMockApi({ latencyMs: 0, payoutSendDelayMs: 10, payoutConfirmDelayMs: 10 });
    const events: ReplayEvent[] = [];
    const statuses: string[] = [];
    const handle = api.openReplayStream({ onEvent: (e) => events.push(e), onStatus: (s) => statuses.push(s) });
    await new Promise((r) => setTimeout(r, 10));
    // 6000x = 150 ms per step: the 4-hour claim is at step 25 (~3.9 s); 144 steps take ~21 s.
    await api.startReplay({ scenario_id: 'derecho-harris-2024', speed: 6000 });
    await until(() => events.some((e) => e.type === 'payout_confirmed'));
    api.dropStream();
    await until(() => events.filter((e) => e.type === 'snapshot').length === 2, 3000);
    handle.close();
    expect(statuses).toContain('reconnecting');
    const last = events.filter((e) => e.type === 'snapshot').at(-1);
    if (last?.type !== 'snapshot') throw new Error('no snapshot');
    expect(last.data.replay_id).not.toBeNull();
    expect(last.data.series.length).toBeGreaterThan(20);
    expect(last.data.claims).toHaveLength(1);
    expect(last.data.claims[0]).toMatchObject({ rule: 'R1', tier: 't4', member_name: 'Fixture Member One', fips: '99001' });
    expect(last.data.claims[0]?.payout?.status).toBe('success');
  }, 12_000);

  it('reset keeps earlier claims listed (CONTRACTS 1.4)', async () => {
    api = createMockApi({ latencyMs: 0, payoutSendDelayMs: 10, payoutConfirmDelayMs: 10 });
    await api.startReplay({ scenario_id: 'spoof-quiet', speed: 1_000_000 });
    await until(() => false, 1500).catch(() => undefined);
    const before = (await api.claims()).claims.length;
    await api.resetReplay();
    expect((await api.claims()).claims.length).toBe(before);
    expect(before).toBeGreaterThan(1);
  }, 6000);
});
