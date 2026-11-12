/** Run clocks (CONTRACTS section 3 "Run duration") and witness intervals (section 6). */
import { describe, expect, it } from 'vitest';
import type { WitnessEntryRow } from '../api/types';
import { summariseRuns, type IndexReading } from '../live/runs';
import { witnessIntervals } from '../lib/witnessIntervals';

const T0 = Date.parse('2000-01-01T00:00:00Z');
const series = (shares: (number | null)[]): IndexReading[] =>
  shares.map((share, i) => ({ t: new Date(T0 + i * 900_000).toISOString().replace('.000', ''), share }));

describe('summariseRuns', () => {
  it('measures a run from first to last reading at or above the threshold', () => {
    const s = summariseRuns(series([0, 0.6, 0.6, 0.6, 0.6, 0.6]), 0.5, 120);
    expect(s.current?.hours).toBe(1);
    expect(s.current?.startedAt).toBe('2000-01-01T00:15:00Z');
  });

  it('qualifies at the reading 4 hours after the first', () => {
    const s = summariseRuns(series([0.6, ...Array(16).fill(0.6)]), 0.5, 120);
    expect(s.current?.hours).toBe(4);
  });

  it('bridges a gap of up to the bridge length and counts it inside the run', () => {
    const s = summariseRuns(series([0.6, null, null, null, null, null, null, null, null, 0.6]), 0.5, 120);
    expect(s.current?.hours).toBeCloseTo(2.25);
  });

  it('ends a run on a gap longer than the bridge', () => {
    const s = summariseRuns(series([0.6, 0.6, ...Array(9).fill(null), 0.6]), 0.5, 120);
    expect(s.current?.startedAt).toBe('2000-01-01T02:45:00Z');
    expect(s.lastClosed?.hours).toBeCloseTo(0.25);
  });

  it('ends a run on an observed reading below the threshold', () => {
    const s = summariseRuns(series([0.6, 0.6, 0.6, 0.2, 0.1]), 0.5, 120);
    expect(s.current).toBeNull();
    expect(s.lastClosed?.hours).toBe(0.5);
  });

  const rules = { gapBridgeMinutes: 120, dropoutMaxFraction: 0.1 };

  it('treats a fall of 90% or more as a dropout when the run is back within the bridge', () => {
    // 0.6 -> 0.05 (at most 10% of 0.6) for 1 h, back to 0.6: one run of 2 h.
    const s = summariseRuns(series([0.6, 0.6, 0.05, 0.04, 0.05, 0.05, 0.6, 0.6, 0.6]), 0.5, rules);
    expect(s.current?.startedAt).toBe('2000-01-01T00:00:00Z');
    expect(s.current?.hours).toBe(2);
    expect(s.lastClosed).toBeNull();
  });

  it('ends the run when a dropout lasts past the bridge, at the last reading above the threshold', () => {
    const s = summariseRuns(series([0.6, 0.6, ...Array(9).fill(0.02), 0.6]), 0.5, rules);
    expect(s.lastClosed).toEqual({ startedAt: '2000-01-01T00:00:00Z', lastAt: '2000-01-01T00:15:00Z', hours: 0.25 });
    expect(s.current?.startedAt).toBe('2000-01-01T02:45:00Z');
  });

  it('a fall to more than the dropout fraction ends the run', () => {
    const s = summariseRuns(series([0.6, 0.6, 0.1, 0.6]), 0.5, rules);
    expect(s.lastClosed?.hours).toBe(0.25);
  });

  it('keeps the run open through a dropout at the live edge until the window passes', () => {
    expect(summariseRuns(series([0.6, 0.6, 0.03, 0.03]), 0.5, rules).current).toMatchObject({ inDropout: true, hours: 0.25 });
    expect(summariseRuns(series([0.6, 0.6, ...Array(9).fill(0.03)]), 0.5, rules).current).toBeNull();
    // With the rule off (artifact value missing), the same fall ends the run.
    expect(summariseRuns(series([0.6, 0.6, 0.03]), 0.5, { gapBridgeMinutes: 120, dropoutMaxFraction: null }).current).toBeNull();
  });

  it('keeps a run open through a trailing gap within the bridge, then closes it', () => {
    expect(summariseRuns(series([0.6, 0.6, null, null]), 0.5, 120).current?.trailingGapMinutes).toBe(30);
    expect(summariseRuns(series([0.6, 0.6, ...Array(9).fill(null)]), 0.5, 120).current).toBeNull();
  });
});

const entry = (seq: number, kind: WitnessEntryRow['kind'], t: string, valid = true): WitnessEntryRow => ({
  id: `wen_fixture${seq}`,
  device_id: 'dev_fixtureaaaab',
  seq,
  device_time: t,
  kind,
  charging: kind === 'power_restored',
  battery_level: null,
  online: true,
  prev_hash: '',
  hash: '',
  sig: '',
  received_at: t,
  sim_time: null,
  valid,
  reject_reason: valid ? null : 'bad_signature',
});

describe('witnessIntervals', () => {
  it('pairs lost with the next restored and ignores heartbeats entirely', () => {
    const iv = witnessIntervals([
      entry(1, 'heartbeat', '2026-11-12T00:00:00Z'),
      entry(2, 'power_lost', '2026-11-12T01:00:00Z'),
      // No heartbeats for hours: that is not evidence of anything.
      entry(3, 'power_restored', '2026-11-12T06:00:00Z'),
    ]);
    expect(iv).toEqual([{ start: '2026-11-12T01:00:00Z', end: '2026-11-12T06:00:00Z', hours: 5, selfReported: false, basis: 'device_time' }]);
  });

  it('never makes an interval from missing heartbeats alone', () => {
    expect(witnessIntervals([entry(1, 'heartbeat', '2026-11-12T00:00:00Z'), entry(2, 'heartbeat', '2026-11-12T09:00:00Z')])).toEqual([]);
  });

  it('marks self-reported intervals, leaves an unrestored interval open, and skips rejected entries', () => {
    const iv = witnessIntervals([
      entry(1, 'attest_lost', '2026-11-12T01:00:00Z'),
      entry(2, 'attest_restored', '2026-11-12T02:00:00Z', false),
    ]);
    expect(iv).toEqual([{ start: '2026-11-12T01:00:00Z', end: null, hours: null, selfReported: true, basis: 'device_time' }]);
  });

  it('uses sim_time during a replay', () => {
    const a = { ...entry(1, 'power_lost', '2026-11-12T01:00:00Z'), sim_time: '2024-05-16T23:00:00Z' };
    const b = { ...entry(2, 'power_restored', '2026-11-12T01:05:00Z'), sim_time: '2024-05-17T05:00:00Z' };
    expect(witnessIntervals([a, b])[0]).toMatchObject({ hours: 6, basis: 'sim_time' });
  });
});
