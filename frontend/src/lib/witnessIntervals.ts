/**
 * Witness outage intervals, as CONTRACTS section 6 defines them: from a valid power_lost (or
 * attest_lost) entry to the next valid power_restored (or attest_restored). Heartbeats are never
 * used: a missing heartbeat (hidden tab, sleeping laptop) says nothing about power.
 * During a replay the interval is measured in sim_time; otherwise in device_time.
 */
import type { IsoTime, WitnessEntryRow } from '../api/types';

export interface WitnessInterval {
  start: IsoTime;
  /** Null while power has not been reported back. */
  end: IsoTime | null;
  hours: number | null;
  selfReported: boolean;
  basis: 'sim_time' | 'device_time';
}

const timeOf = (e: WitnessEntryRow) => e.sim_time ?? e.device_time;

export function witnessIntervals(entries: readonly WitnessEntryRow[]): WitnessInterval[] {
  const out: WitnessInterval[] = [];
  let open: WitnessEntryRow | null = null;
  const sorted = [...entries].filter((e) => e.valid).sort((a, b) => a.seq - b.seq);
  for (const e of sorted) {
    const lost = e.kind === 'power_lost' || e.kind === 'attest_lost';
    const back = e.kind === 'power_restored' || e.kind === 'attest_restored';
    if (lost && !open) open = e;
    else if (back && open) {
      const start = timeOf(open);
      const end = timeOf(e);
      out.push({
        start,
        end,
        hours: (Date.parse(end) - Date.parse(start)) / 3_600_000,
        selfReported: open.kind === 'attest_lost' || e.kind === 'attest_restored',
        basis: open.sim_time ? 'sim_time' : 'device_time',
      });
      open = null;
    }
  }
  if (open) {
    out.push({ start: timeOf(open), end: null, hours: null, selfReported: open.kind === 'attest_lost', basis: open.sim_time ? 'sim_time' : 'device_time' });
  }
  return out;
}
