/**
 * County outage index over time: one series (share of customers out), the two thresholds as
 * reference lines when they are known, and event markers. Missing 15-minute intervals are drawn as
 * a hatched "no data" band and the line breaks there: never a zero, never an interpolated line.
 *
 * Fixed height and a fixed x domain (the scenario window when known), so nothing shifts while
 * ticks arrive. Plain SVG; no chart library.
 */
import { useEffect, useMemo, useRef, useState, type MouseEvent } from 'react';
import type { Fips, IsoTime } from '../api/types';
import { formatClockUtc, formatCountyLocal, formatShare, formatUtc } from '../lib/format';

export interface ChartPoint {
  t: IsoTime;
  share: number | null;
  customers_out: number | null;
}

export interface ChartMarker {
  t: IsoTime;
  label: string;
  kind: 'claim' | 'witness';
  tone: 'good' | 'warn' | 'bad' | 'neutral' | 'info';
}

export interface IndexChartProps {
  points: readonly ChartPoint[];
  /** [start, end] of the x axis. Defaults to the data's own span. */
  domain?: [IsoTime, IsoTime] | null;
  thetaHi: number | null;
  thetaLo: number | null;
  /** Appended to the strong threshold's label, e.g. "grid edge" (CONTRACTS 1.7). */
  thetaHiNote?: string;
  markers?: readonly ChartMarker[];
  fips?: Fips | null;
  height?: number;
  label: string;
}

const M = { top: 18, right: 150, bottom: 40, left: 58 };
const STEP_MS = 15 * 60 * 1000;
const HOUR = 3_600_000;

function niceMax(v: number): number {
  const target = Math.max(0.1, v * 1.15);
  return Math.min(1, Math.ceil(target / 0.05) * 0.05);
}

function hourStep(spanMs: number): number {
  for (const h of [1, 2, 3, 6, 12, 24, 48]) if (spanMs / (h * HOUR) <= 8) return h;
  return 96;
}

export function IndexChart({ points, domain, thetaHi, thetaLo, thetaHiNote = '', markers = [], fips, height = 340, label }: IndexChartProps) {
  const boxRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(900);
  const [hover, setHover] = useState<number | null>(null);

  useEffect(() => {
    const el = boxRef.current;
    if (!el) return;
    const measure = () => setWidth(Math.max(320, Math.round(el.getBoundingClientRect().width) || 900));
    measure();
    if (typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const geo = useMemo(() => {
    const first = points[0];
    const last = points[points.length - 1];
    const x0 = domain ? Date.parse(domain[0]) : first ? Date.parse(first.t) : 0;
    let x1 = domain ? Date.parse(domain[1]) : last ? Date.parse(last.t) : x0 + HOUR;
    if (x1 <= x0) x1 = x0 + HOUR;
    let maxShare = 0;
    for (const p of points) if (p.share !== null && p.share > maxShare) maxShare = p.share;
    const yMax = niceMax(Math.max(maxShare, thetaHi ?? 0, thetaLo ?? 0));
    const iw = width - M.left - M.right;
    const ih = height - M.top - M.bottom;
    const x = (t: number) => M.left + ((t - x0) / (x1 - x0)) * iw;
    const y = (s: number) => M.top + ih - (s / yMax) * ih;

    // Line segments split at missing intervals; gap bands cover each run of nulls.
    const segments: string[] = [];
    const areas: string[] = [];
    const gaps: { from: number; to: number }[] = [];
    let seg: { px: number; py: number }[] = [];
    let gapStart: number | null = null;
    const flush = () => {
      if (seg.length) {
        segments.push(seg.map((p, i) => `${i ? 'L' : 'M'}${p.px.toFixed(1)},${p.py.toFixed(1)}`).join(''));
        const base = y(0).toFixed(1);
        const f = seg[0] as { px: number };
        const l = seg[seg.length - 1] as { px: number };
        areas.push(
          `M${f.px.toFixed(1)},${base}` + seg.map((p) => `L${p.px.toFixed(1)},${p.py.toFixed(1)}`).join('') + `L${l.px.toFixed(1)},${base}Z`,
        );
      }
      seg = [];
    };
    for (const p of points) {
      const t = Date.parse(p.t);
      if (p.share === null) {
        flush();
        if (gapStart === null) gapStart = t;
        continue;
      }
      if (gapStart !== null) {
        gaps.push({ from: gapStart, to: t - STEP_MS });
        gapStart = null;
      }
      seg.push({ px: x(t), py: y(p.share) });
    }
    flush();
    if (gapStart !== null && last) gaps.push({ from: gapStart, to: Date.parse(last.t) });

    const step = hourStep(x1 - x0) * HOUR;
    const xTicks: number[] = [];
    for (let t = Math.ceil(x0 / step) * step; t <= x1; t += step) xTicks.push(t);
    const yTicks: number[] = [];
    const yStep = yMax <= 0.2 ? 0.05 : yMax <= 0.5 ? 0.1 : 0.2;
    for (let v = 0; v <= yMax + 1e-9; v += yStep) yTicks.push(Number(v.toFixed(2)));
    return { x0, x1, x, y, iw, ih, yMax, segments, areas, gaps, xTicks, yTicks, multiDay: x1 - x0 > 24 * HOUR };
  }, [points, domain, thetaHi, thetaLo, width, height]);

  const onMove = (e: MouseEvent<SVGRectElement>) => {
    if (!points.length) return;
    const rect = (e.currentTarget.ownerSVGElement as SVGSVGElement).getBoundingClientRect();
    const px = e.clientX - rect.left;
    const t = geo.x0 + ((px - M.left) / geo.iw) * (geo.x1 - geo.x0);
    let best = 0;
    let bestD = Infinity;
    points.forEach((p, i) => {
      const d = Math.abs(Date.parse(p.t) - t);
      if (d < bestD) {
        bestD = d;
        best = i;
      }
    });
    setHover(best);
  };

  const hp = hover !== null ? points[hover] : undefined;
  const last = [...points].reverse().find((p) => p.share !== null);
  const lineLabel = (theta: number | null, name: string, note: string) =>
    theta === null ? null : `${name} ${formatShare(theta)}${note ? ` (${note})` : ''}`;

  return (
    <div>
      <div className="chart-box" ref={boxRef} style={{ height }}>
        <svg role="img" aria-label={label} width={width} height={height}>
          <defs>
            <pattern id="gap-hatch" width="8" height="8" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
              <line x1="0" y1="0" x2="0" y2="8" stroke="var(--line-strong)" strokeWidth="2" />
            </pattern>
          </defs>
          {geo.yTicks.map((v) => (
            <g key={v}>
              <line x1={M.left} x2={M.left + geo.iw} y1={geo.y(v)} y2={geo.y(v)} stroke="var(--line)" strokeWidth={1} />
              <text x={M.left - 8} y={geo.y(v)} textAnchor="end" dominantBaseline="middle" fontSize="12" fill="var(--muted)">
                {Math.round(v * 100)}%
              </text>
            </g>
          ))}
          {geo.xTicks.map((t) => {
            const iso = new Date(t).toISOString();
            const d = new Date(t);
            const day = d.getUTCHours() === 0 && geo.multiDay;
            return (
              <text key={t} x={geo.x(t)} y={M.top + geo.ih + 22} textAnchor="middle" fontSize="12" fill="var(--muted)">
                {day ? `${d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' })}` : formatClockUtc(iso)}
              </text>
            );
          })}
          <text x={M.left + geo.iw} y={M.top + geo.ih + 36} textAnchor="end" fontSize="11" fill="var(--muted)">
            UTC
          </text>
          {geo.gaps.map((g) => {
            const gx = geo.x(g.from - STEP_MS / 2);
            const gw = Math.max(4, geo.x(g.to + STEP_MS / 2) - gx);
            return (
              <g key={g.from}>
                <rect x={gx} y={M.top} width={gw} height={geo.ih} fill="url(#gap-hatch)" opacity={0.55} />
                {gw > 34 && (
                  <text x={gx + gw / 2} y={M.top + 12} textAnchor="middle" fontSize="11" fill="var(--text-2)">
                    no data
                  </text>
                )}
              </g>
            );
          })}
          {geo.areas.map((d, i) => (
            <path key={`a${i}`} d={d} fill="var(--series-1-fill)" />
          ))}
          {geo.segments.map((d, i) => (
            <path key={`s${i}`} d={d} fill="none" stroke="var(--series-1)" strokeWidth={2.5} strokeLinejoin="round" strokeLinecap="round" />
          ))}
          {[
            { v: thetaHi, name: 'Strong threshold', dash: '8 5', note: thetaHiNote },
            { v: thetaLo, name: 'Lenient threshold', dash: '2 4', note: '' },
          ].map(
            (th) =>
              th.v !== null &&
              th.v <= geo.yMax && (
                <g key={th.name}>
                  <line
                    x1={M.left}
                    x2={M.left + geo.iw}
                    y1={geo.y(th.v)}
                    y2={geo.y(th.v)}
                    stroke="var(--threshold)"
                    strokeWidth={2}
                    strokeDasharray={th.dash}
                  />
                  <text x={M.left + geo.iw + 8} y={geo.y(th.v)} dominantBaseline="middle" fontSize="12" fontWeight={600} fill="var(--text-2)">
                    {lineLabel(th.v, th.name, '')}
                  </text>
                  {th.note && (
                    <text x={M.left + geo.iw + 8} y={geo.y(th.v) + 15} dominantBaseline="middle" fontSize="11" fill="var(--text-2)">
                      ({th.note})
                    </text>
                  )}
                </g>
              ),
          )}
          {markers.map((m, i) => {
            const mx = geo.x(Date.parse(m.t));
            if (m.kind === 'witness') {
              const my = M.top + geo.ih;
              return (
                <g key={`m${i}`}>
                  <title>{m.label}</title>
                  <path d={`M${mx},${my - 7}L${mx + 6},${my}L${mx},${my + 7}L${mx - 6},${my}Z`} fill="var(--surface)" stroke="var(--text)" strokeWidth={2} />
                </g>
              );
            }
            return (
              <g key={`m${i}`}>
                <line x1={mx} x2={mx} y1={M.top} y2={M.top + geo.ih} stroke={`var(--${m.tone === 'neutral' ? 'text-2' : m.tone})`} strokeWidth={2} />
                <rect x={mx + 4} y={M.top + 18 + (i % 2) * 22} width={m.label.length * 7 + 12} height={20} rx={4} fill="var(--surface)" stroke="var(--line-strong)" />
                <text x={mx + 10} y={M.top + 32 + (i % 2) * 22} fontSize="12" fontWeight={600} fill="var(--text)">
                  {m.label}
                </text>
              </g>
            );
          })}
          {last && last.share !== null && (
            <circle cx={geo.x(Date.parse(last.t))} cy={geo.y(last.share)} r={5} fill="var(--series-1)" stroke="var(--surface)" strokeWidth={2} />
          )}
          {hp && (
            <line x1={geo.x(Date.parse(hp.t))} x2={geo.x(Date.parse(hp.t))} y1={M.top} y2={M.top + geo.ih} stroke="var(--text-2)" strokeWidth={1} />
          )}
          <rect
            x={M.left}
            y={M.top}
            width={geo.iw}
            height={geo.ih}
            fill="transparent"
            onMouseMove={onMove}
            onMouseLeave={() => setHover(null)}
          />
        </svg>
        {hp && (
          <div
            className="chart-tooltip"
            style={{ left: geo.x(Date.parse(hp.t)), top: hp.share !== null ? geo.y(hp.share) : M.top + geo.ih / 2 }}
          >
            <div>
              <strong>{hp.share === null ? 'Missing interval (no data)' : `${formatShare(hp.share)} of customers out`}</strong>
            </div>
            {hp.customers_out !== null && <div>{hp.customers_out.toLocaleString('en-US')} customers</div>}
            <div className="muted">{formatUtc(hp.t)}</div>
            {formatCountyLocal(hp.t, fips) && <div className="muted">{formatCountyLocal(hp.t, fips)}</div>}
          </div>
        )}
      </div>
      <div className="chart-legend" aria-hidden="true">
        <span>
          <span className="legend-swatch" />
          Share of county customers without power
        </span>
        {thetaHi !== null && (
          <span>
            <span className="legend-swatch dashed" />
            Strong threshold (theta_hi){thetaHiNote ? `, ${thetaHiNote}` : ''}
          </span>
        )}
        {thetaLo !== null && (
          <span>
            <span className="legend-swatch dotted" />
            Lenient threshold (theta_lo)
          </span>
        )}
        <span>
          <span className="legend-swatch gap" />
          Hatched band: missing 15-minute data, drawn as a gap
        </span>
        {markers.some((m) => m.kind === 'witness') && <span>◇ Witness entry</span>}
      </div>
    </div>
  );
}

/** The same series as a table, for screen readers and for checking values. */
export function SeriesTable({ points, fips }: { points: readonly ChartPoint[]; fips?: Fips | null }) {
  const local = (t: string) => formatCountyLocal(t, fips, { year: false });
  return (
    <details>
      <summary>Show the index as a table ({points.length} readings)</summary>
      <div className="table-wrap" style={{ maxHeight: 320, overflowY: 'auto' }}>
        <table className="plain">
          <thead>
            <tr>
              <th>Time (UTC)</th>
              {fips && local(points[0]?.t ?? '') && <th>County local</th>}
              <th className="num">Share out</th>
              <th className="num">Customers out</th>
            </tr>
          </thead>
          <tbody>
            {points.map((p) => (
              <tr key={p.t}>
                <td>{formatUtc(p.t, { year: false })}</td>
                {fips && local(p.t) && <td>{local(p.t)}</td>}
                <td className="num">{p.share === null ? 'missing' : formatShare(p.share)}</td>
                <td className="num">{p.customers_out === null ? 'missing' : p.customers_out.toLocaleString('en-US')}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </details>
  );
}
