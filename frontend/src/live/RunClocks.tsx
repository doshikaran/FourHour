/**
 * The 4-hour clocks: how long the current run has been at or above each threshold, against the
 * 4-hour line. The prototype pays the 4-hour tier only. Display only; the decision comes from claim_decided.
 */
import { NotComputed } from '../components/ui';
import { formatClockUtc, formatHours, formatHoursClock } from '../lib/format';
import { summariseRuns, type IndexReading } from './runs';

function Ring({ fraction, done }: { fraction: number; done: boolean }) {
  const r = 40;
  const c = 2 * Math.PI * r;
  const f = Math.max(0, Math.min(1, fraction));
  return (
    <svg viewBox="0 0 96 96" aria-hidden="true">
      <circle cx="48" cy="48" r={r} fill="none" stroke="var(--line)" strokeWidth="9" />
      <circle
        cx="48"
        cy="48"
        r={r}
        fill="none"
        stroke={done ? 'var(--good)' : 'var(--series-1)'}
        strokeWidth="9"
        strokeLinecap="round"
        strokeDasharray={`${c * f} ${c}`}
        transform="rotate(-90 48 48)"
      />
      {done && (
        <text x="48" y="56" textAnchor="middle" fontSize="26" fontWeight="700" fill="var(--good)">
          ✓
        </text>
      )}
    </svg>
  );
}

export function RunClock({
  title,
  theta,
  gapBridgeMinutes,
  dropoutMaxFraction,
  series,
}: {
  title: string;
  theta: number | null;
  gapBridgeMinutes: number | null;
  dropoutMaxFraction: number | null;
  series: readonly IndexReading[];
}) {
  if (theta === null || gapBridgeMinutes === null) {
    return (
      <div className="clock">
        <Ring fraction={0} done={false} />
        <div>
          <div className="small muted">{title}</div>
          <div className="clock-value">
            <NotComputed reason="Thresholds come from artifacts/trigger_params.json" />
          </div>
          <div className="small muted">Thresholds are not computed yet, so there is no clock.</div>
        </div>
      </div>
    );
  }
  const runs = summariseRuns(series, theta, { gapBridgeMinutes, dropoutMaxFraction });
  const cur = runs.current;
  const hours = cur?.hours ?? 0;
  const done = hours >= 4;
  return (
    <div className="clock" data-testid={`clock-${title.startsWith('Strong') ? 'hi' : 'lo'}`}>
      <Ring fraction={done ? 1 : hours / 4} done={done} />
      <div>
        <div className="small muted">{title}</div>
        <div className="clock-value">
          {cur ? (
            <>
              {formatHoursClock(hours)} <span className="small muted">of 4:00</span>
            </>
          ) : (
            <span className="muted">Not above</span>
          )}
        </div>
        <div className="small">
          {cur
            ? `${hours >= 4 ? '4-hour line passed. ' : ''}Run since ${formatClockUtc(cur.startedAt)} UTC${cur.inDropout ? ` · possible feed dropout, kept open for up to ${gapBridgeMinutes} min` : cur.trailingGapMinutes > 0 ? ` · last ${cur.trailingGapMinutes} min missing, bridged up to ${gapBridgeMinutes} min` : ''}`
            : runs.lastClosed
              ? `Last run lasted ${formatHours(runs.lastClosed.hours)}.`
              : 'No run yet.'}
        </div>
      </div>
    </div>
  );
}
