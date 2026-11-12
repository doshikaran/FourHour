import { ApiError } from '../api/client';
import { useApi } from '../api/context';
import type { AnalyticsName, TriggerParamsArtifact } from '../api/types';
import { PageHeader } from '../components/Layout';
import { ErrorState, Loading, NotComputed, ProvenanceDetails, RealDataChip, thetaHiWording } from '../components/ui';
import { formatRate, formatShare } from '../lib/format';
import { useAsync } from '../lib/useAsync';

const PANELS: { name: AnalyticsName; title: string; what: string }[] = [
  { name: 'backtest', title: 'Backtest', what: 'Frequency and severity models scored on 2023–2024, years they never saw.' },
  { name: 'pricing', title: 'Pricing', what: 'How each county’s premium is built.' },
  { name: 'solvency', title: 'Solvency', what: 'Simulated years of the whole pool: chance of ruin and capital needed.' },
  { name: 'basis-risk', title: 'Basis risk', what: 'Indian premises data: how often an index misses or wrongly pays an outage.' },
  { name: 'trigger', title: 'Trigger design', what: 'How the two thresholds were chosen on training years.' },
  { name: 'data-quality', title: 'Data quality', what: 'Coverage of the outage data per county and year, and gaps.' },
];

// E1 (phase 5) builds the panels. Until then each section shows whether its artifact exists and
// where it came from; a missing artifact shows "not computed".
export function EvidencePage() {
  return (
    <>
      <PageHeader
        title="Evidence"
        purpose="Backtest, pricing, solvency, basis-risk and data-quality results, each with where it came from."
      />
      <div className="grid-2">
        {PANELS.map((p) => (
          <Panel key={p.name} {...p} />
        ))}
      </div>
    </>
  );
}

function Panel({ name, title, what }: { name: AnalyticsName; title: string; what: string }) {
  const api = useApi();
  const s = useAsync(() => api.analytics(name), `analytics:${name}`);
  return (
    <section className="card" aria-label={title}>
      <h2>{title}</h2>
      <p className="muted">{what}</p>
      {s.status === 'loading' && <Loading what={`the ${title.toLowerCase()} result`} />}
      {s.status === 'error' &&
        (s.error instanceof ApiError && s.error.code === 'artifact_missing' ? (
          <p>
            <NotComputed /> The model output has not been generated yet.
          </p>
        ) : (
          <ErrorState what={`the ${title.toLowerCase()} result`} error={s.error} onRetry={s.reload} />
        ))}
      {s.status === 'ok' && name === 'trigger' && <TriggerPanel t={s.data as TriggerParamsArtifact} />}
      {s.status === 'ok' && name !== 'trigger' && (
        <>
          <p className="small">The detailed panel arrives with task E1.</p>
          <ProvenanceDetails provenance={s.data.provenance} />
        </>
      )}
    </section>
  );
}

const val = (v: number | null, f: (n: number) => string) => (v === null ? <NotComputed /> : f(v));

function TriggerPanel({ t }: { t: TriggerParamsArtifact }) {
  const edge = t.theta_hi_status === 'grid_edge_no_interior_optimum';
  return (
    <>
      <p className="row small">
        <RealDataChip what="model result" />
      </p>
      <dl className="kv">
        <dt>Lenient threshold (theta_lo)</dt>
        <dd>{val(t.theta_lo, formatShare)} of county customers out</dd>
        <dt>Strong threshold (theta_hi)</dt>
        <dd>
          {val(t.theta_hi, formatShare)} of county customers out
          {edge && t.theta_hi !== null ? ' (edge of the search grid, not an optimum)' : ''}
        </dd>
        <dt>Gap bridge</dt>
        <dd>{val(t.gap_bridge_minutes, (n) => `${n} minutes`)}</dd>
        <dt>Feed dropout</dt>
        <dd>{val(t.dropout_max_fraction, (n) => `a fall to ${formatRate(n)} or less of the last reading`)}</dd>
        <dt>Hours clause</dt>
        <dd>{val(t.event_merge_hours, (n) => `${n} hours`)}</dd>
      </dl>
      {t.theta_hi !== null && <p className="small">{thetaHiWording(t.theta_hi_status).long}</p>}
      <h3 style={{ marginTop: 16 }}>Sensitivity to the product assumptions</h3>
      {t.sensitivity.length === 0 ? (
        <p className="muted">No sensitivity rows in the artifact.</p>
      ) : (
        <div className="table-wrap">
          <table className="plain">
            <caption className="visually-hidden">The chosen threshold pair for each combination of product assumptions</caption>
            <thead>
              <tr>
                <th className="num">Witness share</th>
                <th className="num">False confirmation rate</th>
                <th className="num">theta_lo</th>
                <th className="num">theta_hi</th>
                <th>theta_hi status</th>
                <th className="num">Objective</th>
              </tr>
            </thead>
            <tbody>
              {t.sensitivity.map((r, i) => (
                <tr key={i}>
                  <td className="num">{formatRate(r.witness_share)}</td>
                  <td className="num">{formatRate(r.false_confirmation_rate)}</td>
                  <td className="num">{val(r.theta_lo, formatShare)}</td>
                  <td className="num">{val(r.theta_hi, formatShare)}</td>
                  <td>{r.theta_hi_status === 'grid_edge_no_interior_optimum' ? 'Grid edge, no interior optimum' : 'Interior optimum'}</td>
                  <td className="num">{val(r.objective, (n) => n.toString())}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <p className="small muted">
        Objective = expected wrong payouts + expected missed payouts per member-year, in units of one full payout, on
        training years only.
      </p>
      <ProvenanceDetails provenance={t.provenance} />
    </>
  );
}
