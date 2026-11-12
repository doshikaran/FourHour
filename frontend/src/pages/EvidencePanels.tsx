/**
 * E1: the evidence panels. Every number here is read from the artifact the backend serves unchanged
 * (CONTRACTS.md section 8); a null value shows "not computed", never a placeholder.
 */
import { AllCommunityModule, ModuleRegistry, themeQuartz, type ColDef, type ValueFormatterParams } from 'ag-grid-community';
import { AgGridReact } from 'ag-grid-react';
import { useMemo, useState, type ReactNode } from 'react';
import type {
  BacktestArtifact,
  BasisRiskArtifact,
  BasisRiskScheme,
  DataQualityArtifact,
  DataQualityRow,
  FrequencyModel,
  FrequencyVariantName,
  PricingTableArtifact,
  SolvencyArtifact,
} from '../api/types';
import { NotComputed, ProvenanceDetails, RealDataChip, SimulatedChip } from '../components/ui';
import { formatCount, formatRate, formatShare, formatUtc } from '../lib/format';
import { formatCents } from '../lib/money';

ModuleRegistry.registerModules([AllCommunityModule]);

const v = (x: number | null | undefined, f: (n: number) => string): ReactNode =>
  x === null || x === undefined || !Number.isFinite(x) ? <NotComputed /> : f(x);
const cents = (x: number | null | undefined) => v(x, (n) => formatCents(n));
const num = (digits: number) => (n: number) => n.toLocaleString('en-US', { maximumFractionDigits: digits });


const MODEL_LABEL: Record<FrequencyModel, string> = {
  nb_glm: 'Negative binomial GLM',
  poisson_glm: 'Poisson GLM',
  historical_mean: 'County historical mean',
};
const VARIANT_LABEL: Record<FrequencyVariantName, string> = {
  exclude_low_coverage: 'Low-coverage county-years excluded (primary)',
  include_low_coverage: 'Low-coverage county-years included',
};

function Kv({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className="kv">
      {rows.map(([k, val]) => (
        <div key={k} style={{ display: 'contents' }}>
          <dt>{k}</dt>
          <dd>{val}</dd>
        </div>
      ))}
    </dl>
  );
}

// ---------- backtest ----------

export function BacktestPanel({ a, names }: { a: BacktestArtifact; names: Record<string, string> }) {
  const f = a.frequency;
  const s = a.severity;
  const c = a.credibility;
  const p = a.pool;
  return (
    <>
      <p className="row small">
        <RealDataChip what="model result" /> Fitted on {a.provenance.train_years[0]}–{a.provenance.train_years[1]}, scored on{' '}
        {a.provenance.test_years[0]}–{a.provenance.test_years[1]}.
      </p>

      <FrequencyVerdict a={a} />

      <h3>Event frequency</h3>
      {f === null ? (
        <p>
          <NotComputed />
        </p>
      ) : (
        <>
          <p className="small">
            Qualifying events ({f.threshold}, {v(f.theta, formatShare)} of customers out for 4 hours) per {f.unit.replace(/^qualifying events per /, '')}.
            Lower deviance and log score are better.
          </p>
          <div className="table-wrap">
            <table className="plain">
              <caption className="visually-hidden">Out-of-sample scores of the frequency models</caption>
              <thead>
                <tr>
                  <th>Variant</th>
                  <th>Model</th>
                  <th className="num">Deviance</th>
                  <th className="num">Log score</th>
                </tr>
              </thead>
              <tbody>
                {(Object.keys(f.variants) as FrequencyVariantName[]).flatMap((vn) => {
                  const vr = f.variants[vn];
                  if (!vr)
                    return [
                      <tr key={vn}>
                        <td>{VARIANT_LABEL[vn]}</td>
                        <td colSpan={3}>
                          <NotComputed />
                        </td>
                      </tr>,
                    ];
                  return (Object.keys(MODEL_LABEL) as FrequencyModel[]).map((m, i) => (
                    <tr key={vn + m}>
                      <td>{i === 0 ? `${VARIANT_LABEL[vn]} · ${vr.county_years_fitted} county-years fitted` : ''}</td>
                      <td>{MODEL_LABEL[m]}</td>
                      <td className="num">{v(vr.deviance[m], num(3))}</td>
                      <td className="num">{v(vr.log_score[m], num(4))}</td>
                    </tr>
                  ));
                })}
              </tbody>
            </table>
          </div>
          {(() => {
            const pv = f.variants[f.primary_variant];
            if (!pv) return null;
            return (
              <p className="small">
                Beats the baselines on the test years (primary variant):{' '}
                <strong>{pv.beats_baselines === null ? <NotComputed /> : pv.beats_baselines ? 'yes' : 'no'}</strong>. A score shown as
                not computed is infinite or undefined; the notes under “Where this comes from” say why.
              </p>
            );
          })()}
        </>
      )}

      <h3>Outage length (severity)</h3>
      {s === null ? (
        <p>
          <NotComputed />
        </p>
      ) : (
        <>
          <Kv
            rows={[
              ['Events used', <>
                {v(s.n_events_train, formatCount)} in training, {v(s.n_events_test, formatCount)} in the test years
              </>],
              ['Generalised Pareto fit', <>
                shape {v(s.gpd.shape, num(6))}, scale {v(s.gpd.scale, num(6))} h, from {v(s.gpd.location_hours, num(2))} h
              </>],
              ['Kolmogorov–Smirnov test', <>
                statistic {v(s.ks_statistic, num(6))}, p = {v(s.ks_pvalue, num(6))}. The p-value is optimistic: the parameters
                were estimated from the same events the test is run on.
              </>],
            ]}
          />
          <p className="small">
            What the fit got right and wrong: the chance a qualifying event runs on to 24 hours, as the fitted curve gives it and as
            it was actually seen. This is a check of the fit only; the price and the payout use the 4-hour tier.
          </p>
          <div className="table-wrap">
            <table className="plain">
              <caption className="visually-hidden">Chance an event lasts 24 hours: fitted against seen</caption>
              <thead>
                <tr>
                  <th>Source</th>
                  <th className="num">Chance a qualifying event lasts 24 h or more</th>
                  <th className="num">Gap to fitted (percentage points)</th>
                </tr>
              </thead>
              <tbody>
                {(
                  [
                    ['Fitted curve', s.prob_24h_given_event.fitted],
                    [`Seen in training (${a.provenance.train_years[0]}–${a.provenance.train_years[1]})`, s.prob_24h_given_event.empirical_train],
                    [`Seen in the test years (${a.provenance.test_years[0]}–${a.provenance.test_years[1]})`, s.prob_24h_given_event.empirical_test],
                  ] as [string, number | null][]
                ).map(([label, x], i) => {
                  const fit = s.prob_24h_given_event.fitted;
                  return (
                    <tr key={label}>
                      <td>{label}</td>
                      <td className="num">{v(x, formatRate)}</td>
                      <td className="num">
                        {i === 0 ? '—' : x === null || fit === null ? <NotComputed /> : signedPoints(x - fit)}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </>
      )}

      <h3>Credibility</h3>
      {c === null ? (
        <p>
          <NotComputed />
        </p>
      ) : (
        <>
          <CredibilityVerdict c={c} />
          <p className="small">
            Method {c.method.replace(/_/g, '–')}, k = {v(c.k, num(3))}. A county’s own event rate is blended with its state’s by
            the weight below.
          </p>
          <div className="table-wrap">
            <table className="plain">
              <caption className="visually-hidden">Credibility weights for the priced counties</caption>
              <thead>
                <tr>
                  <th>County</th>
                  <th className="num">County rate</th>
                  <th className="num">State rate</th>
                  <th className="num">Weight</th>
                  <th className="num">Blended rate</th>
                </tr>
              </thead>
              <tbody>
                {Object.entries(c.counties)
                  .filter(([fips]) => a.pool === null || fips in (a.pool.years[0]?.events_by_county ?? {}))
                  .map(([fips, r]) => (
                    <tr key={fips}>
                      <td>
                        {names[fips] ?? `FIPS ${fips}, ${r.state}`}
                      </td>
                      <td className="num">{v(r.county_rate, num(4))}</td>
                      <td className="num">{v(r.state_rate, num(4))}</td>
                      <td className="num">{v(r.weight, formatRate)}</td>
                      <td className="num">{v(r.blended_rate, num(4))}</td>
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
          <p className="small muted">Rates are qualifying events per county-month. Counties shown are the ones in the pool backtest.</p>
        </>
      )}

      <h3>Pool backtest on the test years</h3>
      {p === null ? (
        <p>
          <NotComputed />
        </p>
      ) : (
        <>
          <p className="row small">
            <SimulatedChip what={`${p.members.toLocaleString('en-US')} members`} /> <RealDataChip what="outage events" />
          </p>
          <div className="table-wrap">
            <table className="plain">
              <caption className="visually-hidden">Premiums and payouts of the simulated pool in each test year</caption>
              <thead>
                <tr>
                  <th className="num">Year</th>
                  <th className="num">Premiums</th>
                  <th className="num">Payouts</th>
                  <th className="num">Loss ratio</th>
                  <th className="num">Qualifying events</th>
                  <th className="num">Claims paid</th>
                </tr>
              </thead>
              <tbody>
                {p.years.map((y) => (
                  <tr key={y.year}>
                    <td className="num">{y.year}</td>
                    <td className="num">{cents(y.premiums_cents)}</td>
                    <td className="num">{cents(y.payouts_cents)}</td>
                    <td className="num">{v(y.loss_ratio, formatRate)}</td>
                    <td className="num">{v(y.events, formatCount)}</td>
                    <td className="num">{v(y.claims_paid, formatCount)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
      <ProvenanceDetails provenance={a.provenance} />
    </>
  );
}

/** Percentage points with a sign, from a difference of two shares. */
export function signedPoints(d: number): string {
  const pts = Number((d * 100).toFixed(2));
  return `${pts > 0 ? '+' : ''}${pts}`;
}

/**
 * The frequency result stated first and plainly, driven by beats_baselines and the event counts in
 * the artifact. No wording here turns a "no" into a softer claim.
 */
function FrequencyVerdict({ a }: { a: BacktestArtifact }) {
  const f = a.frequency;
  const pv = f ? f.variants[f.primary_variant] : null;
  const beats = pv ? pv.beats_baselines : null;
  const s = a.severity;
  const test = `${a.provenance.test_years[0]}–${a.provenance.test_years[1]}`;
  return (
    <div className={`notice ${beats === false ? 'notice-warn' : 'notice-info'}`} style={{ color: 'var(--text)' }} role="note">
      <p style={{ margin: 0 }}>
        <strong>
          {beats === null
            ? `Whether the frequency model beat its baselines on ${test}: not computed.`
            : beats
              ? `The frequency model beat its baselines on ${test}.`
              : `The frequency model did not beat its baselines on ${test}.`}
        </strong>
      </p>
      {beats !== true && (
        <p style={{ margin: '6px 0 0' }}>
          Why: qualifying events are rare. {v(s?.n_events_train, formatCount)} in the training years and{' '}
          {v(s?.n_events_test, formatCount)} in the test years, across every county in the model. With so few events a fitted
          model cannot separate itself from a simple rate.
        </p>
      )}
    </div>
  );
}

/** States how many credibility weights are zero, read from the artifact. */
function CredibilityVerdict({ c }: { c: NonNullable<BacktestArtifact['credibility']> }) {
  const known = Object.values(c.counties)
    .map((r) => r.weight)
    .filter((w): w is number => w !== null);
  if (known.length === 0) return null;
  const zero = known.filter((w) => w === 0).length;
  return (
    <p className="small">
      <strong>
        {zero === known.length
          ? `Every credibility weight is 0 (${zero} of ${known.length} counties).`
          : `${zero} of ${known.length} credibility weights are 0.`}
      </strong>{' '}
      {zero > 0 &&
        'A weight of 0 means a county’s own history adds nothing beyond its state’s rate at this event count, so it is priced at the state rate.'}
    </p>
  );
}

// ---------- pricing ----------

export function PricingPanel({ a, names }: { a: PricingTableArtifact; names: Record<string, string> }) {
  return (
    <>
      <p className="row small">
        <RealDataChip what="model result" /> Model version <code>{a.model_version}</code>
      </p>
      <Kv
        rows={[
          ['Risk margin', v(a.rates.risk_margin_rate, formatRate)],
          ['Expense load', <>{cents(a.rates.expense_load_cents)} a month</>],
          ['Payout schedule', <>
            {v(a.payout_schedule.t4, formatRate)} of cover at 4 hours, the tier that is priced and paid
          </>],
        ]}
      />
      <div className="table-wrap">
        <table className="plain">
          <caption className="visually-hidden">Pricing inputs per county</caption>
          <thead>
            <tr>
              <th>County</th>
              <th className="num">Events a year</th>
              <th className="num">Chance premises affected</th>
              <th className="num">Credibility weight</th>
              <th>Offered</th>
            </tr>
          </thead>
          <tbody>
            {Object.entries(a.counties).map(([fips, c]) => (
              <tr key={fips}>
                <td>{names[fips] ?? fips}</td>
                <td className="num">{v(c.expected_events_per_year, num(6))}</td>
                <td className="num">{v(c.prob_premises_affected, formatRate)}</td>
                <td className="num">{v(c.credibility_weight, formatRate)}</td>
                <td>{c.offered === false ? <>No. {c.not_offered_reason ?? ''}</> : c.offered ? 'Yes' : <NotComputed />}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="small muted">
        Premium = events a year × payout share × chance premises affected × cover ÷ 12 × (1 + risk margin) + expense load. The
        quote on Get cover shows it for one county and cover amount.
      </p>
      <ProvenanceDetails provenance={a.provenance} />
    </>
  );
}

// ---------- solvency ----------

export function SolvencyPanel({ a }: { a: SolvencyArtifact }) {
  return (
    <>
      <p className="row small">
        <SimulatedChip what={`${a.members.toLocaleString('en-US')} members`} /> <RealDataChip what="outage events" />
      </p>
      <Kv
        rows={[
          ['Simulated years', formatCount(a.simulations)],
          ['Chance of ruin in a year', <>
            {v(a.ruin_probability, formatRate)}
            <div className="small muted">
              {a.capital_assumption ? (
                <>
                  Starting capital {cents(a.capital_assumption.initial_capital_cents)}. {a.capital_assumption.description}
                </>
              ) : (
                <>Capital assumption: not in this artifact, so read this figure with care.</>
              )}
            </div>
          </>],
          ['Capital for a 1-in-200 year', cents(a.capital_1_in_200_cents)],
          ['Stop-loss layer', <>
            price {cents(a.stop_loss.price_cents)}; attaches at {cents(a.stop_loss.attachment_cents)} of annual pool loss; limit{' '}
            {cents(a.stop_loss.limit_cents)}
          </>],
        ]}
      />
      <div className="table-wrap">
        <table className="plain">
          <caption className="visually-hidden">Annual pool loss by quantile</caption>
          <thead>
            <tr>
              <th className="num">Year as bad as 1 in</th>
              <th className="num">Pool loss</th>
            </tr>
          </thead>
          <tbody>
            {a.annual_loss_quantiles.map((q) => (
              <tr key={q.q}>
                <td className="num">{q.q >= 1 ? '—' : Math.round(1 / (1 - q.q)).toLocaleString('en-US')}</td>
                <td className="num">{cents(q.loss_cents)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="small muted">Read the notes under “Where this comes from” before quoting these: they state the limits of the simulation.</p>
      <ProvenanceDetails provenance={a.provenance} />
    </>
  );
}

// ---------- basis risk ----------

const SCHEME_LABEL: Record<BasisRiskScheme, string> = {
  index_only: 'District index only',
  rules_perfect_witness: 'Index and witness (witness always present)',
  rules_10pct_missing: 'Index and witness, 10% of witnesses missing',
  rules_30pct_missing: 'Index and witness, 30% of witnesses missing',
};

export function BasisRiskPanel({ a }: { a: BasisRiskArtifact }) {
  return (
    <>
      <p className="row small">
        <RealDataChip what="ESMI premises monitors, India" />
      </p>
      <p className="small">
        Thresholds {v(a.theta_lo, formatShare)} and {v(a.theta_hi, formatShare)} of monitors out, fitted {a.fit_period.start} to{' '}
        {a.fit_period.end}, scored {a.score_period.start} to {a.score_period.end}. A premises event is a monitor without power for 4
        hours or more.
      </p>
      <div className="evidence-split">
      <div className="table-wrap">
        <table className="plain">
          <caption className="visually-hidden">Missed and wrong payouts by scheme</caption>
          <thead>
            <tr>
              <th>Scheme</th>
              <th className="num">Premises events</th>
              <th className="num">Paid</th>
              <th className="num">Missed</th>
              <th className="num">Wrongly paid</th>
            </tr>
          </thead>
          <tbody>
            {(Object.keys(SCHEME_LABEL) as BasisRiskScheme[]).map((k) => {
              const r = a.schemes[k];
              return (
                <tr key={k}>
                  <td>{SCHEME_LABEL[k]}</td>
                  {r === null || r === undefined ? (
                    <td colSpan={4}>
                      <NotComputed />
                    </td>
                  ) : (
                    <>
                      <td className="num">{v(r.premises_events, formatCount)}</td>
                      <td className="num">{v(r.paid, formatCount)}</td>
                      <td className="num">
                        {v(r.missed, formatCount)} ({v(r.missed_rate, formatRate)})
                      </td>
                      <td className="num">
                        {v(r.wrong, formatCount)} ({v(r.wrong_rate, formatRate)})
                      </td>
                    </>
                  )}
                </tr>
              );
            })}
          </tbody>
        </table>
        <p className="small muted">Missed is a share of premises events; wrongly paid is a share of payments.</p>
      </div>
      <aside className="notice notice-warn" style={{ color: 'var(--text)', margin: 0 }} aria-label="Caveats for this table">
        <h3 style={{ marginTop: 0 }}>Read this with the table</h3>
        <ul className="small" style={{ margin: 0, paddingLeft: 18 }}>
          <li>
            <strong>Indian premises data.</strong> These are ESMI voltage monitors at individual premises in India, not US
            customers. The result shows the mechanism, not the size of basis risk for a US county.
          </li>
          <li>
            <strong>Few monitors per district.</strong> {formatCount(a.districts_used.length)} districts, each needing at least{' '}
            {v(a.min_active_monitors, formatCount)} monitors reporting at once. The median reporting in each is listed below.
          </li>
          <li>
            <strong>The perfect-witness row is an upper bound.</strong> There the witness is the premises’ own monitor, so it is
            right by construction. A real phone on a charger will do worse; the missing-witness rows show how fast it degrades.
          </li>
          <li>
            <strong>A district index from a handful of monitors is noisier than a county utility count.</strong> One premises
            moves it by several percent; a US county count is over tens of thousands of customers or more.
          </li>
        </ul>
      </aside>
      </div>
      <div className="table-wrap" style={{ marginTop: 12 }}>
        <table className="plain">
          <caption className="small" style={{ textAlign: 'left' }}>
            Districts used
          </caption>
          <thead>
            <tr>
              <th>District</th>
              <th>State</th>
              <th className="num">Median monitors reporting</th>
            </tr>
          </thead>
          <tbody>
            {a.districts_used.map((d) => (
              <tr key={`${d.state}-${d.district}`}>
                <td>{d.district}</td>
                <td>{d.state}</td>
                <td className="num">{v(d.median_active_monitors, formatCount)}</td>
              </tr>
            ))}
            {a.districts_used.length === 0 && (
              <tr>
                <td colSpan={3}>
                  <NotComputed />
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <ProvenanceDetails provenance={a.provenance} />
    </>
  );
}

// ---------- data quality ----------

const dqTheme = themeQuartz.withParams({
  fontFamily: 'inherit',
  fontSize: 14,
  headerFontSize: 13,
  rowHeight: 38,
  headerHeight: 40,
  accentColor: 'var(--accent)',
  backgroundColor: 'var(--surface)',
  foregroundColor: 'var(--text)',
  borderColor: 'var(--line)',
  headerBackgroundColor: 'var(--surface-2)',
});
const pct = (p: ValueFormatterParams<DataQualityRow, number | null>) => formatShare(p.value);
const cnt = (p: ValueFormatterParams<DataQualityRow, number | null>) => formatCount(p.value);

export function DataQualityPanel({ a }: { a: DataQualityArtifact }) {
  const [search, setSearch] = useState('');
  const columns = useMemo<ColDef<DataQualityRow>[]>(
    () => [
      { field: 'county', headerName: 'County', minWidth: 140 },
      { field: 'state', headerName: 'State', maxWidth: 110 },
      { field: 'fips', headerName: 'FIPS', maxWidth: 120 },
      { field: 'year', headerName: 'Year', maxWidth: 110, filter: 'agNumberColumnFilter' },
      { field: 'coverage_share', headerName: 'Coverage', valueFormatter: pct, filter: 'agNumberColumnFilter', type: 'rightAligned' },
      { field: 'county_customers', headerName: 'Customers', valueFormatter: cnt, filter: 'agNumberColumnFilter', type: 'rightAligned' },
      { field: 'max_customers_out', headerName: 'Most out', valueFormatter: cnt, filter: 'agNumberColumnFilter', type: 'rightAligned' },
      {
        field: 'in_model_universe',
        headerName: 'In model',
        maxWidth: 130,
        valueFormatter: (p: ValueFormatterParams<DataQualityRow, boolean>) => (p.value ? 'yes' : 'no'),
      },
      {
        field: 'excluded_from_fitting',
        headerName: 'Excluded from fit',
        maxWidth: 160,
        valueFormatter: (p: ValueFormatterParams<DataQualityRow, boolean>) => (p.value ? 'yes' : 'no'),
      },
      { field: 'exclusion_reason', headerName: 'Why', minWidth: 260, flex: 1, tooltipField: 'exclusion_reason' },
    ],
    [],
  );
  const g = a.gap_bridge_evidence;
  return (
    <>
      <p className="row small">
        <RealDataChip what="EAGLE-I outage records" />
      </p>
      <Kv
        rows={[
          ['Coverage floor for fitting', v(a.coverage_floor, formatRate)],
          ['Gaps short enough to bridge', <>
            {v(g.share_of_gaps_within_bridge, formatRate)} of gaps in {v(g.events_examined, formatCount)} events examined
          </>],
          ['Timestamps', <>
            assumed {a.timestamp_check.assumed};{' '}
            {a.timestamp_check.consistent_with_utc === null ? <NotComputed /> : a.timestamp_check.consistent_with_utc ? 'consistent' : 'not consistent'}{' '}
            with the {a.timestamp_check.storm} (first sharp rise {formatUtc(a.timestamp_check.first_sharp_rise_utc)})
          </>],
        ]}
      />
      {a.findings && a.findings.length > 0 && (
        <>
          <h3>Data checks and what they found</h3>
          <ul className="small">
            {a.findings.map((f) => (
              <li key={f.id}>{f.finding}</li>
            ))}
          </ul>
        </>
      )}
      <h3>Every county and year</h3>
      <p className="small muted">
        {a.rows.length.toLocaleString('en-US')} county-years. Coverage is the share of 15-minute readings present. Sort, filter or
        search.
      </p>
      <label className="visually-hidden" htmlFor="dq-search">
        Search the data-quality grid
      </label>
      <input
        id="dq-search"
        type="search"
        placeholder="Search county, state, FIPS or reason"
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        style={{ marginBottom: 8, maxWidth: 360 }}
      />
      <div style={{ height: 420 }}>
        <AgGridReact<DataQualityRow>
          theme={dqTheme}
          rowData={a.rows}
          columnDefs={columns}
          defaultColDef={{ sortable: true, resizable: true, filter: true, minWidth: 100 }}
          quickFilterText={search}
          getRowId={(p) => `${p.data.fips}-${p.data.year}`}
        />
      </div>
      <h3>Storm windows</h3>
      <ul className="small">
        {a.storm_windows.map((w) => (
          <li key={w.id}>{w.notes}</li>
        ))}
      </ul>
      <ProvenanceDetails provenance={a.provenance} />
    </>
  );
}
