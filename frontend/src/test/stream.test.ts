import { describe, expect, it } from 'vitest';
import { parseReplayEvent } from '../api/stream';
import type { ReplayEvent, ReplayEventName } from '../api/types';
import { createMockApi } from '../mock/mockApi';

describe('parseReplayEvent', () => {
  it('accepts a well-formed tick', () => {
    const r = parseReplayEvent('tick', '{"sim_time":"2000-01-01T00:15:00Z","share":0.1,"customers_out":100,"fips":"99001"}');
    expect(r.ok && r.event.type).toBe('tick');
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
    ['bogus', '{"sim_time":"t"}', 'unknown event "bogus"'],
  ])('rejects %s %s', (name, raw, reason) => {
    expect(parseReplayEvent(name, raw)).toEqual({ ok: false, reason });
  });
});

describe('mock replay stream', () => {
  it('sends a snapshot first, scripts a claim and payout, and resends a snapshot after a drop', async () => {
    const api = createMockApi({ latencyMs: 0 });
    const events: ReplayEvent[] = [];
    const statuses: string[] = [];
    const handle = api.openReplayStream({ onEvent: (e) => events.push(e), onStatus: (s) => statuses.push(s) });
    await new Promise((r) => setTimeout(r, 10));
    const idle = events[0];
    expect(idle?.type).toBe('snapshot');
    expect(idle?.type === 'snapshot' && idle.data).toEqual({
      replay_id: null,
      scenario_id: null,
      sim_time: null,
      open_events: [],
      series: [],
      claims: [],
    });

    // speed 9000 = 100 ms per 15-minute step, about 3.6 s for the 36 steps, so the 1 s
    // disconnect below lands mid-replay and the client is back before replay_ended.
    await api.startReplay({ scenario_id: 'fixture-replay', speed: 9000 });
    await new Promise((r) => setTimeout(r, 300));
    api.dropStream();
    await new Promise((r) => setTimeout(r, 1200));
    await new Promise<void>((resolve) => {
      const check = () => (events.some((e) => e.type === 'replay_ended') ? resolve() : setTimeout(check, 50));
      check();
    });
    handle.close();

    const names = events.map((e) => e.type);
    const has = (n: ReplayEventName) => names.includes(n);
    expect(names.filter((n) => n === 'snapshot').length).toBe(2);
    expect(statuses).toContain('reconnecting');
    // The second snapshot carries the series so far, so a reconnecting client can rebuild.
    const second = events.filter((e) => e.type === 'snapshot')[1];
    expect(second?.type === 'snapshot' && second.data.series.length).toBeGreaterThan(0);
    expect(second?.type === 'snapshot' && second.data.series[0]).toMatchObject({ fips: '99001' });
    expect(second?.type === 'snapshot' && typeof second.data.series[0]?.sim_time).toBe('string');
    expect(has('replay_ended')).toBe(true);
    // Events emitted while disconnected are lost, which is why the snapshot exists. Whatever
    // arrived must still be in order of simulated time.
    const times = events.filter((e) => e.type === 'tick').map((e) => e.data.sim_time);
    expect([...times].sort()).toEqual(times);
  }, 10_000);

  it('emits claim_decided, payout_sent and payout_confirmed in order when connected throughout', async () => {
    const api = createMockApi({ latencyMs: 0 });
    const names: ReplayEventName[] = [];
    const handle = api.openReplayStream({ onEvent: (e) => names.push(e.type) });
    await new Promise((r) => setTimeout(r, 10));
    await api.startReplay({ scenario_id: 'fixture-replay', speed: 1_000_000 });
    await new Promise<void>((resolve) => {
      const check = () => (names.includes('replay_ended') ? resolve() : setTimeout(check, 50));
      check();
    });
    handle.close();
    const i = (n: ReplayEventName) => names.indexOf(n);
    expect(i('event_opened')).toBeGreaterThan(0);
    expect(i('claim_decided')).toBeGreaterThan(i('event_opened'));
    expect(i('payout_sent')).toBeGreaterThan(i('claim_decided'));
    expect(i('payout_confirmed')).toBeGreaterThan(i('payout_sent'));
    expect(i('event_closed')).toBeGreaterThan(i('payout_confirmed'));
    expect(i('replay_ended')).toBe(names.length - 1);
  }, 10_000);

  it('a reconnect after the payout gets a snapshot carrying the claims decided so far', async () => {
    const api = createMockApi({ latencyMs: 0 });
    const events: ReplayEvent[] = [];
    const handle = api.openReplayStream({ onEvent: (e) => events.push(e) });
    await new Promise((r) => setTimeout(r, 10));
    // speed 6000 = 150 ms per step: payout confirmed near step 22 (about 3.3 s), replay ends at
    // step 36 (about 5.4 s), so the reconnect 1 s after the drop lands while it is still running.
    await api.startReplay({ scenario_id: 'fixture-replay', speed: 6000 });
    await new Promise<void>((resolve) => {
      const check = () => (events.some((e) => e.type === 'payout_confirmed') ? resolve() : setTimeout(check, 10));
      check();
    });
    api.dropStream();
    await new Promise((r) => setTimeout(r, 1100));
    handle.close();
    const snaps = events.filter((e) => e.type === 'snapshot');
    const last = snaps[snaps.length - 1];
    expect(snaps.length).toBe(2);
    if (last?.type !== 'snapshot') throw new Error('no snapshot');
    expect(last.data.replay_id).not.toBeNull();
    expect(last.data.claims.length).toBe(1);
    expect(last.data.claims[0]).toMatchObject({ rule: 'R1', member_name: 'Fixture Member One', fips: '99001' });
    expect(last.data.claims[0]?.payout?.status).toBe('success');
  }, 10_000);
});
