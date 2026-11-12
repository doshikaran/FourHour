/**
 * F2: county and cover picker, the quote with its breakdown, member details, and the hand-off to
 * PayPal to save the wallet. Every number comes from POST /api/quotes or the pricing artifact.
 */
import { useState, type FormEvent } from 'react';
import { ApiError } from '../api/client';
import { useApi } from '../api/context';
import { COVER_OPTIONS_CENTS, type County, type QuotableCoverCents, type QuoteResponse, type TriggerParamsArtifact } from '../api/types';
import { PageHeader } from '../components/Layout';
import {
  AsyncView,
  ErrorState,
  Loading,
  NotComputed,
  ProvenanceDetails,
  RealDataChip,
  SandboxChip,
  SimulatedChip,
} from '../components/ui';
import { formatRate, formatShare } from '../lib/format';
import { formatCents } from '../lib/money';
import { navigate, useLocation } from '../lib/router';
import { useAsync, type AsyncState } from '../lib/useAsync';

type TriggerState = AsyncState<TriggerParamsArtifact> & { reload: () => void };

/** A threshold from the trigger artifact as text, or null when it is missing or not computed. */
function thetaText(t: TriggerState, key: 'theta_lo' | 'theta_hi'): string | null {
  if (t.status !== 'ok') return null;
  const v = t.data[key];
  return v === null ? null : formatShare(v);
}

/** Cover × schedule share in integer cents, rounded down (CONTRACTS section 3). */
export function scheduledCents(coverCents: number, share: number): number {
  return Math.floor(Math.round(coverCents * share * 1e6) / 1e6);
}

export function EnrolPage() {
  const api = useApi();
  const { search } = useLocation();
  const counties = useAsync(() => api.counties(), 'counties');
  const trigger = useAsync(() => api.analytics('trigger'), 'analytics:trigger');
  const [fips, setFips] = useState('');
  const [cover, setCover] = useState<QuotableCoverCents>(15000);
  const selected = counties.status === 'ok' ? counties.data.counties.find((c) => c.fips === fips) : undefined;
  // 1.8: a county with offered=false gets no quote request and no enrolment.
  const offeredFips = selected && selected.offered ? fips : '';

  return (
    <>
      <PageHeader
        title="Get cover"
        purpose="Pick your county and cover amount. The quote shows how the premium is built. Then save your PayPal wallet to start."
      />
      {search.get('cancelled') === '1' && (
        <p role="status" className="notice notice-warn">
          You left PayPal before saving your wallet. Nothing was charged. You can start again below.
        </p>
      )}
      <div className="enrol-layout">
        <div className="card">
          <h2>1. Your county and cover</h2>
          <AsyncView state={counties} what="counties">
            {(r) => (
              <CountyAndCover counties={r.counties} fips={fips} setFips={setFips} cover={cover} setCover={setCover} />
            )}
          </AsyncView>
        </div>
        <div className="card quote-card" style={{ minHeight: 300 }}>
          <h2>2. Your quote</h2>
          {selected && !selected.offered ? (
            <NotOfferedState reason={selected.not_offered_reason} />
          ) : fips ? (
            <QuotePanel fips={fips} cover={cover} trigger={trigger} />
          ) : (
            <div className="quote-placeholder"><span aria-hidden="true">↗</span><p>Choose a county to see a quote.</p><small>Your monthly price and payout breakdown will appear here.</small></div>
          )}
        </div>
      </div>
      <div className="card" style={{ marginTop: 20 }}>
        <h2>3. Your details and PayPal</h2>
        <CoverageLimits trigger={trigger} />
        <MemberForm fips={offeredFips} cover={cover} />
      </div>
    </>
  );
}

function CountyAndCover(props: {
  counties: County[];
  fips: string;
  setFips: (f: string) => void;
  cover: QuotableCoverCents;
  setCover: (c: QuotableCoverCents) => void;
}) {
  const { counties, fips, setFips, cover, setCover } = props;
  if (counties.length === 0) return <p className="muted">The backend lists no counties yet.</p>;
  const offered = counties.filter((c) => c.offered);
  const notOffered = counties.filter((c) => !c.offered);
  return (
    <>
      <div className="field">
        <label htmlFor="county">County</label>
        <select id="county" value={fips} onChange={(e) => setFips(e.target.value)}>
          <option value="">Choose a county…</option>
          {offered.map((c) => (
            <option key={c.fips} value={c.fips}>
              {c.name}, {c.state} — outage data coverage {c.data_coverage === null ? 'not computed' : formatRate(c.data_coverage)}
            </option>
          ))}
          {notOffered.length > 0 && (
            <optgroup label="Not offered">
              {notOffered.map((c) => (
                <option key={c.fips} value={c.fips} disabled>
                  {c.name}, {c.state} — not offered
                </option>
              ))}
            </optgroup>
          )}
        </select>
        <span className="hint">Coverage is the share of 15-minute outage readings present in the county's data.</span>
      </div>
      {notOffered.length > 0 && (
        <details className="small" style={{ marginBottom: 12 }}>
          <summary>
            {notOffered.length} {notOffered.length === 1 ? 'county is' : 'counties are'} not offered — why
          </summary>
          <ul>
            {notOffered.map((c) => (
              <li key={c.fips}>
                <strong>
                  {c.name}, {c.state}
                </strong>
                : {c.not_offered_reason ?? 'No reason was given.'}
              </li>
            ))}
          </ul>
          <p className="muted">These counties still appear in the evidence tables.</p>
        </details>
      )}
      <fieldset className="choice-group">
        <legend>Cover amount (value of perishable stock)</legend>
        {COVER_OPTIONS_CENTS.map((c) => (
          <label className="choice" key={c}>
            <input type="radio" name="cover" value={c} checked={cover === c} onChange={() => setCover(c)} />
            <span>{formatCents(c)}</span>
          </label>
        ))}
      </fieldset>
    </>
  );
}

function QuotePanel({ fips, cover, trigger }: { fips: string; cover: QuotableCoverCents; trigger: TriggerState }) {
  const api = useApi();
  const quote = useAsync(() => api.quote({ fips, cover_cents: cover }), `quote:${fips}:${cover}`);
  if (quote.status === 'loading') return <Loading what="your quote" />;
  if (quote.status === 'error') {
    if (quote.error instanceof ApiError && quote.error.code === 'county_not_offered') {
      return <NotOfferedState reason={quote.error.message} />;
    }
    if (quote.error instanceof ApiError && quote.error.code === 'pricing_not_computed') {
      return (
        <div className="state" role="status">
          <p>
            <strong>Premium: <NotComputed /></strong>
          </p>
          <p>The pricing model has not produced every input for this county yet, so no price is shown. Try another county.</p>
        </div>
      );
    }
    return <ErrorState what="your quote" error={quote.error} onRetry={quote.reload} />;
  }
  return <QuoteView q={quote.data} thetaLo={thetaText(trigger, 'theta_lo')} />;
}

function NotOfferedState({ reason }: { reason: string | null }) {
  return (
    <div className="state" role="status">
      <p>
        <strong>Not offered in this county</strong>
      </p>
      <p>{reason ?? 'No reason was given.'}</p>
      <p className="muted">No quote is shown. Choose another county.</p>
    </div>
  );
}

function QuoteView({ q, thetaLo }: { q: QuoteResponse; thetaLo: string | null }) {
  const b = q.breakdown;
  const rows: [string, string, string][] = [
    [
      `Outages expected per year with at least ${thetaLo ?? '(threshold not computed)'} of the county's customers out for 4 hours or more`,
      b.expected_events_per_year.toString(),
      'Frequency model, blended with the state rate',
    ],
    ['Share of cover paid per qualifying outage', formatRate(b.expected_payout_share), 'The 4-hour payout share, the only tier paid'],
    ['Chance your premises is affected', formatRate(b.prob_premises_affected), 'Mean share of customers out during events'],
    ['Expected payout per year', formatCents(b.expected_annual_payout_cents), 'Events × payout share × chance affected × cover'],
    ['County credibility weight', formatRate(b.credibility_weight), 'How much the county’s own history counts'],
    ['Risk margin', formatRate(b.risk_margin_rate), 'From the simulated tail of annual losses'],
    ['Expense load per month', formatCents(b.expense_load_cents), 'Running costs'],
  ];
  return (
    <div>
      <div className="row">
        <RealDataChip what="model result" />
        <span className="small muted">model {q.model_version}</span>
      </div>
      <p className="quote-figure" aria-live="polite">
        {formatCents(q.premium_cents)} <small>a month for {formatCents(q.cover_cents)} cover</small>
      </p>
      <p className="small muted">
        Premium = expected payout per year ÷ 12 × (1 + risk margin) + expense load. The price assumes you register a witness
        device.
      </p>
      <table className="plain">
        <tbody>
          {rows.map(([k, v, why]) => (
            <tr key={k}>
              <th scope="row" style={{ textTransform: 'none', letterSpacing: 0, fontSize: '0.92rem' }}>
                {k}
                <div className="small muted" style={{ fontWeight: 400 }}>
                  {why}
                </div>
              </th>
              <td className="num">{v}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <PayoutSchedule cover={q.cover_cents} />
      <ProvenanceDetails provenance={q.provenance} label="Where this price comes from" />
    </div>
  );
}

function PayoutSchedule({ cover }: { cover: number }) {
  const api = useApi();
  const pricing = useAsync(() => api.analytics('pricing'), 'analytics:pricing');
  return (
    <div style={{ marginTop: 16 }}>
      <h3>What it pays</h3>
      {pricing.status === 'loading' && <Loading what="the payout schedule" />}
      {pricing.status === 'error' &&
        (pricing.error instanceof ApiError && pricing.error.code === 'artifact_missing' ? (
          <p>
            Payout schedule: <NotComputed />
          </p>
        ) : (
          <ErrorState what="the payout schedule" error={pricing.error} onRetry={pricing.reload} />
        ))}
      {pricing.status === 'ok' && (
        <>
          <ul>
            <li>
              Outage of 4 hours or more: {formatRate(pricing.data.payout_schedule.t4)} of cover,{' '}
              <strong>{formatCents(scheduledCents(cover, pricing.data.payout_schedule.t4))}</strong> (refrigerated stock).
            </li>
          </ul>
          <p className="small muted">
            The 4-hour share of cover is a product assumption, not a fitted value. The price is built on this same share.
          </p>
        </>
      )}
    </div>
  );
}

/**
 * What is covered in practice (SPEC section 4, CONTRACTS 1.7). Stated before the PayPal hand-off.
 * Thresholds are read from the trigger artifact, never typed in.
 */
function CoverageLimits({ trigger }: { trigger: TriggerState }) {
  const lo = thetaText(trigger, 'theta_lo');
  const hi = thetaText(trigger, 'theta_hi');
  const show = (v: string | null) => (v === null ? <NotComputed reason="Read from artifacts/trigger_params.json" /> : <strong>{v}</strong>);
  return (
    <section className="notice notice-info" aria-labelledby="limits-h" style={{ color: 'var(--text)' }}>
      <h3 id="limits-h">What this covers in practice</h3>
      {trigger.status === 'loading' && <p className="small muted">Loading the thresholds…</p>}
      {trigger.status === 'error' && !(trigger.error instanceof ApiError && trigger.error.code === 'artifact_missing') && (
        <ErrorState what="the thresholds" error={trigger.error} onRetry={trigger.reload} />
      )}
      <ul style={{ margin: 0 }}>
        <li>
          <strong>Cover in practice requires a registered witness device</strong> (a laptop or phone left on a charger at your
          premises). Without one, a payment needs at least {show(hi)} of the county's customers out, which almost never
          happens.
        </li>
        <li>
          <strong>Outages affecting less than {show(lo)} of the county's customers are not covered</strong>, even if your
          premises is dark. That is a limit of county-level outage data.
        </li>
      </ul>
    </section>
  );
}

type Submit = { status: 'idle' } | { status: 'busy'; step: string } | { status: 'error'; error: Error };

function MemberForm({ fips, cover }: { fips: string; cover: QuotableCoverCents }) {
  const api = useApi();
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [paypalEmail, setPaypalEmail] = useState('');
  const [submit, setSubmit] = useState<Submit>({ status: 'idle' });

  const ready = fips !== '' && name.trim() !== '' && email.includes('@') && paypalEmail.includes('@');

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!ready) return;
    try {
      setSubmit({ status: 'busy', step: 'Creating your member record…' });
      const member = await api.createMember({ name: name.trim(), email: email.trim(), paypal_email: paypalEmail.trim() });
      setSubmit({ status: 'busy', step: 'Creating your policy and asking PayPal for an approval link…' });
      const { approve_url } = await api.createPolicy({ member_id: member.id, fips, cover_cents: cover });
      setSubmit({ status: 'busy', step: 'Opening PayPal…' });
      if (approve_url.startsWith('/')) navigate(approve_url);
      else window.location.assign(approve_url);
    } catch (err: unknown) {
      setSubmit({ status: 'error', error: err instanceof Error ? err : new Error(String(err)) });
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate>
      <p className="row small">
        <SimulatedChip what="member" /> Demo members are simulated people. Use a PayPal sandbox personal account.
      </p>
      <div className="grid-2">
        <div className="field">
          <label htmlFor="m-name">Business or owner name</label>
          <input id="m-name" type="text" autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="field">
          <label htmlFor="m-email">Contact email</label>
          <input id="m-email" type="email" autoComplete="email" value={email} onChange={(e) => setEmail(e.target.value)} />
        </div>
        <div className="field">
          <label htmlFor="m-paypal">
            PayPal email for payouts <span className="hint">(a sandbox personal account)</span>
          </label>
          <input id="m-paypal" type="email" value={paypalEmail} onChange={(e) => setPaypalEmail(e.target.value)} />
        </div>
      </div>
      <p className="small muted row">
        <SandboxChip /> Next you go to PayPal to save your wallet. The first premium is charged when you come back.
      </p>
      {submit.status === 'error' && <ErrorState what="your enrolment" error={submit.error} />}
      <div className="row">
        <button type="submit" className="big" disabled={!ready || submit.status === 'busy'}>
          Continue to PayPal
        </button>
        {submit.status === 'busy' && (
          <span role="status" className="row">
            <span className="spinner" aria-hidden="true" /> {submit.step}
          </span>
        )}
        {!ready && submit.status !== 'busy' && (
          <span className="small muted">{fips ? 'Fill in all three fields.' : 'Choose a county first, then fill in all three fields.'}</span>
        )}
      </div>
    </form>
  );
}
