/**
 * Index runs from a 15-minute series, by the definitions in CONTRACTS.md section 3:
 *
 * - Run duration: a run is readings at or above a threshold; its duration is the last observed
 *   reading at or above it minus the first, with bridged gaps counted inside.
 * - Gaps: missing intervals of up to `gap_bridge_minutes` between readings at or above the
 *   threshold do not end the run.
 * - Dropouts (1.6): a reading below the threshold that is at most `dropout_max_fraction` of the last
 *   reading at or above it (and any such readings right after it) is treated as missing, when a
 *   reading at or above the threshold follows within `gap_bridge_minutes` of that last reading.
 *   Live, nobody can see ahead, so the run stays open through such a reading until that window passes.
 *
 * This drives the clocks on /live as a display. The claim decision always comes from the backend's
 * rule engine (claim_decided), never from this function.
 */
import type { IsoTime } from '../api/types';

export const STEP_MINUTES = 15;

export interface IndexReading {
  t: IsoTime;
  share: number | null;
}

export interface RunInfo {
  startedAt: IsoTime;
  /** Last observed reading at or above the threshold. */
  lastAt: IsoTime;
  hours: number;
}

export interface RunSummary {
  /** The run still open at the end of the series, if any. */
  current: (RunInfo & { trailingGapMinutes: number; inDropout: boolean }) | null;
  /** The most recent run that has closed. */
  lastClosed: RunInfo | null;
  /** Longest run seen, open or closed. */
  longestHours: number;
}

export interface RunRules {
  gapBridgeMinutes: number;
  /** Null switches the dropout rule off (the artifact did not supply it). */
  dropoutMaxFraction: number | null;
}

const ms = (t: IsoTime) => Date.parse(t);
const hoursBetween = (a: IsoTime, b: IsoTime) => (ms(b) - ms(a)) / 3_600_000;
const minutesBetween = (a: IsoTime, b: IsoTime) => (ms(b) - ms(a)) / 60_000;

export function summariseRuns(series: readonly IndexReading[], theta: number, rules: RunRules | number): RunSummary {
  const { gapBridgeMinutes: bridge, dropoutMaxFraction: dropFrac } =
    typeof rules === 'number' ? { gapBridgeMinutes: rules, dropoutMaxFraction: null } : rules;
  let start: IsoTime | null = null;
  let last: IsoTime | null = null;
  let lastShare = 0;
  let inDropout = false;
  let lastClosed: RunInfo | null = null;
  let longest = 0;

  const close = () => {
    if (start && last) {
      lastClosed = { startedAt: start, lastAt: last, hours: hoursBetween(start, last) };
      longest = Math.max(longest, lastClosed.hours);
    }
    start = null;
    last = null;
    inDropout = false;
  };

  for (const p of series) {
    if (p.share === null) continue; // a missing interval: decided by the next observed reading
    if (p.share >= theta) {
      if (start && last) {
        // A dropout is bridged when the run is back within `bridge` minutes of its last reading;
        // a plain gap when at most `bridge` minutes of intervals are missing.
        const broken = inDropout ? minutesBetween(last, p.t) > bridge : minutesBetween(last, p.t) - STEP_MINUTES > bridge;
        if (broken) close();
      }
      if (!start) start = p.t;
      last = p.t;
      lastShare = p.share;
      inDropout = false;
      longest = Math.max(longest, hoursBetween(start, last));
    } else if (start && dropFrac !== null && p.share <= dropFrac * lastShare) {
      inDropout = true; // possibly a partial-feed dropout: decided when the window passes
    } else {
      close();
    }
  }

  let current: RunSummary['current'] = null;
  const end = series[series.length - 1];
  if (start && last && end) {
    const trailing = minutesBetween(last, end.t);
    if (trailing > bridge) close();
    else current = { startedAt: start, lastAt: last, hours: hoursBetween(start, last), trailingGapMinutes: trailing, inDropout };
  }
  return { current, lastClosed, longestHours: longest };
}
