import { ApiError } from '../api/client';
import { useApi } from '../api/context';
import type {
  AnalyticsArtifacts,
  AnalyticsName,
  BacktestArtifact,
  BasisRiskArtifact,
  DataQualityArtifact,
  PricingTableArtifact,
  SolvencyArtifact,
  TriggerParamsArtifact,
} from '../api/types';
import { BacktestPanel, BasisRiskPanel, DataQualityPanel, PricingPanel, SolvencyPanel } from './EvidencePanels';
import { PageHeader } from '../components/Layout';
import { ErrorState, Loading, NotComputed, ProvenanceDetails, RealDataChip, thetaHiWording } from '../components/ui';
import { formatRate, formatShare } from '../lib/format';
import { useAsync } from '../lib/useAsync';

// Honest order (final pass): basis risk first, then the backtest with its negative result, then
// solvency, pricing, trigger design and data quality.
const PANELS: { name: AnalyticsName; title: string; what: string }[] = [
  { name: 'basis-risk', title: 'Basis risk', what: 'Indian premises data: how often an index misses or wrongly pays an outage, with and without a witness.' },
  { name: 'backtest', title: 'Backtest', what: 'Frequency and severity models scored on 2023–2024, years they never saw.' },
  { name: 'solvency', title: 'Solvency', what: 'Simulated years of the whole pool: chance of ruin, capital needed and a stop-loss layer.' },
  { name: 'pricing', title: 'Pricing', what: 'How each county’s premium is built, and which counties are not offered.' },
  { name: 'trigger', title: 'Trigger design', what: 'How the two thresholds were chosen on training years.' },
  { name: 'data-quality', title: 'Data quality', what: 'Coverage of the outage data per county and year, gaps, and data checks.' },
];

// E1: each panel reads its artifact; a missing artifact shows "not computed". One panel per row so
// the tables stay readable.

export function EvidencePage() {
  return (
    <>
      <PageHeader
        title="Evidence"
        purpose="Backtest, pricing, solvency, basis-risk and data-quality results, each with where it came from."
      />
      <nav className="evidence-nav" aria-label="Evidence sections">
        {PANELS.map((p) => <a key={p.name} href={`#evidence-${p.name}`}>{p.title}<span aria-hidden="true">↗</span></a>)}
      </nav>
      <div className="stack evidence-panels">
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
  const counties = useAsync(() => api.counties(), 'counties');
  const names: Record<string, string> = {};
  if (counties.status === 'ok') for (const c of counties.data.counties) names[c.fips] = `${c.name}, ${c.state}`;
  return (
    <section id={`evidence-${name}`} className="card evidence-panel" aria-label={title}>
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
      {s.status === 'ok' && <Body name={name} data={s.data} names={names} />}
    </section>
  );
}

function Body({ name, data, names }: { name: AnalyticsName; data: AnalyticsArtifacts[AnalyticsName]; names: Record<string, string> }) {
  switch (name) {
    case 'trigger':
      return <TriggerPanel t={data as TriggerParamsArtifact} />;
    case 'backtest':
      return <BacktestPanel a={data as BacktestArtifact} names={names} />;
    case 'pricing':
      return <PricingPanel a={data as PricingTableArtifact} names={names} />;
    case 'solvency':
      return <SolvencyPanel a={data as SolvencyArtifact} />;
    case 'basis-risk':
      return <BasisRiskPanel a={data as BasisRiskArtifact} />;
    case 'data-quality':
      return <DataQualityPanel a={data as DataQualityArtifact} />;
  }
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
