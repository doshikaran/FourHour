/**
 * Display formatting for times, shares and durations. API times are UTC (CONTRACTS section 1);
 * the UI may add county local time, always labelled with its zone.
 */
import type { Fips, IsoTime } from '../api/types';

/**
 * IANA zones for the three demo counties (SPEC section 2). This is reference geography, not a
 * result. Any other county (including the mock's Fixture counties) is shown in UTC only.
 */
const COUNTY_TIME_ZONES: Record<string, string> = {
  '48201': 'America/Chicago',
  '12086': 'America/New_York',
  '17031': 'America/Chicago',
};

export function countyTimeZone(fips: Fips | null | undefined): string | null {
  return fips ? COUNTY_TIME_ZONES[fips] ?? null : null;
}

function parts(iso: IsoTime, timeZone: string, withYear: boolean): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return new Intl.DateTimeFormat('en-US', {
    timeZone,
    ...(withYear ? { year: 'numeric' } : {}),
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
    timeZoneName: 'short',
  }).format(d);
}

/** "May 16, 2024, 23:45 UTC". */
export function formatUtc(iso: IsoTime | null | undefined, opts: { year?: boolean } = {}): string {
  if (!iso) return '—';
  return parts(iso, 'UTC', opts.year ?? true);
}

/** County local time with its zone abbreviation, or null when the county has no known zone. */
export function formatCountyLocal(iso: IsoTime | null | undefined, fips: Fips | null | undefined, opts: { year?: boolean } = {}): string | null {
  const tz = countyTimeZone(fips);
  if (!iso || !tz) return null;
  return parts(iso, tz, opts.year ?? true);
}

/** Short HH:MM in UTC, for feeds and axes. */
export function formatClockUtc(iso: IsoTime): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${String(d.getUTCHours()).padStart(2, '0')}:${String(d.getUTCMinutes()).padStart(2, '0')}`;
}

/** A share 0..1 as a percentage. Shares below 10% get one decimal. */
export function formatShare(share: number | null | undefined): string {
  if (share === null || share === undefined || !Number.isFinite(share)) return 'not computed';
  const pct = share * 100;
  return `${pct < 10 ? pct.toFixed(1) : pct.toFixed(pct < 100 ? 1 : 0)}%`;
}

/** A probability or rate 0..1 as a percentage with up to 2 decimals. */
export function formatRate(rate: number | null | undefined): string {
  if (rate === null || rate === undefined || !Number.isFinite(rate)) return 'not computed';
  return `${Number((rate * 100).toFixed(2))}%`;
}

/** Hours as "3 h 15 min". */
export function formatHours(hours: number): string {
  const totalMin = Math.max(0, Math.round(hours * 60));
  const h = Math.floor(totalMin / 60);
  const m = totalMin % 60;
  if (h === 0) return `${m} min`;
  return m === 0 ? `${h} h` : `${h} h ${m} min`;
}

/** Hours as a clock "3:15". */
export function formatHoursClock(hours: number): string {
  const totalMin = Math.max(0, Math.floor(hours * 60));
  return `${Math.floor(totalMin / 60)}:${String(totalMin % 60).padStart(2, '0')}`;
}

export function formatCount(n: number | null | undefined): string {
  if (n === null || n === undefined || !Number.isFinite(n)) return 'not computed';
  return n.toLocaleString('en-US');
}
