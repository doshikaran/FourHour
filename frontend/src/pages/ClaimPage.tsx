/**
 * F4: one claim explained. Which rule fired and why, the index curve with the thresholds, the
 * witness log the rule engine used, and the payout status.
 */
import { ApiError } from '../api/client';
import { useApi } from '../api/context';
import type { ClaimDetailResponse, ClaimId, TriggerParamsArtifact, WitnessEntryRow } from '../api/types';
import { IndexChart, SeriesTable, type ChartMarker } from '../components/IndexChart';
import { PageHeader } from '../components/Layout';
import {
  AsyncView,
  DecisionPill,
  Empty,
  NotComputed,
  PayoutStatusPill,
  ProvenanceDetails,
  RULE_TEXT,
  SandboxChip,
  SelfReportedChip,
  SimulatedChip,
  TIER_LABEL,
  isSelfReported,
  thetaHiWording,
} from '../components/ui';
import { formatCountyLocal, formatHours, formatShare, formatUtc } from '../lib/format';
import { formatCents } from '../lib/money';
import { useAsync, type AsyncState } from '../lib/useAsync';
import { witnessIntervals } from '../lib/witnessIntervals';

export function ClaimPage({ id }: { id: string }) {
  const api = useApi();
  const state = useAsync(() => api.claim(id as ClaimId), `claim:${id}`);
  const trigger = useAsync(() => api.analytics('trigger'), 'analytics:trigger');
  return (
    <>
      <PageHeader title="Claim explained" />
      <AsyncView state={state} what={`claim ${id}`}>
        {(r) => <ClaimView r={r} trigger={trigger} />}
      </AsyncView>
    </>
  );
}

function ClaimView({ r, trigger }: { r: ClaimDetailResponse; trigger: AsyncState<TriggerParamsArtifact> }) {
  const c = r.claim;
  const th = trigger.status === 'ok' ? trigger.data : null;
  const intervals = witnessIntervals(r.witness_entries);
  const markers: ChartMarker[] = [
    ...r.witness_entries
      .filter((w) => w.kind !== 'heartbeat' && w.valid)
      .map((w): ChartMarker => ({ t: w.sim_time ?? w.device_time, kind: 'witness', tone: 'neutral', label: `Witness: ${w.kind}` })),
    { t: c.sim_time ?? c.decided_at, kind: 'claim', tone: c.decision === 'paid' ? 'good' : 'warn', label: `${c.rule} ${c.decision}` },
  ];
  const e = r.outage_event;

  return (
    <>
      <section className="card">
        <div className="card-title">
          <DecisionPill decision={c.decision} />
          <h2 style={{ fontSize: '2rem' }} className="num">
            {formatCents(c.amount_cents)}
          </h2>
          <span className="muted">{TIER_LABEL[c.tier]}</span>
          <span className="spacer" />
          <span>
            {c.member_name} <SimulatedChip what="member" />
          </span>
        </div>
        <p style={{ fontSize: '1.15rem' }}>{c.reason}</p>
        <p>
          <strong>Rule {c.rule}.</strong> {RULE_TEXT[c.rule]}
        </p>
        <p className="small muted">
          Decided at {formatUtc(c.sim_time ?? c.decided_at)}
          {c.sim_time ? ' on the replay clock' : ''}. Claim <code>{c.id}</code>.
        </p>
      </section>

      <div className="grid-2" style={{ marginTop: 16 }}>
        <section className="card" aria-labelledby="po-h">
          <div className="card-title">
            <h2 id="po-h">Payout</h2>
            <PayoutStatusPill status={c.payout?.status ?? null} />
            <SandboxChip />
          </div>
          {c.payout ? (
            <dl className="kv">
              <dt>Amount</dt>
              <dd>{formatCents(c.payout.amount_cents)}</dd>
              <dt>Sent</dt>
              <dd>{formatUtc(c.payout.created_at)}</dd>
              <dt>Confirmed</dt>
              <dd>{c.payout.confirmed_at ? `${formatUtc(c.payout.confirmed_at)} by ${c.payout.confirmed_via ?? 'PayPal'}` : 'Not yet'}</dd>
              <dt>PayPal batch</dt>
              <dd>
                <code>{c.payout.paypal_batch_id ?? '—'}</code>
              </dd>
              <dt>Idempotency key</dt>
              <dd>
                <code>{c.payout.sender_batch_id}</code>
              </dd>
            </dl>
          ) : (
            <p className="muted">{c.decision === 'paid' ? 'The payout has not been created yet.' : 'This decision pays nothing.'}</p>
          )}
        </section>
        <section className="card" aria-labelledby="ev-h">
          <h2 id="ev-h">Outage event</h2>
          {e ? (
            <dl className="kv">
              <dt>Above lenient threshold</dt>
              <dd>
                {formatUtc(e.lo_started_at)} → {e.lo_ended_at ? formatUtc(e.lo_ended_at) : 'still open'}
              </dd>
              <dt>Above strong threshold</dt>
              <dd>
                {e.hi_started_at ? `${formatUtc(e.hi_started_at)} → ${e.hi_ended_at ? formatUtc(e.hi_ended_at) : 'still open'}` : 'never'}
              </dd>
              <dt>Peak</dt>
              <dd>{formatShare(e.peak_share)} of customers out</dd>
              {formatCountyLocal(e.lo_started_at, e.fips) && (
                <>
                  <dt>Started, county time</dt>
                  <dd>{formatCountyLocal(e.lo_started_at, e.fips)}</dd>
                </>
              )}
            </dl>
          ) : (
            <p>No index event. Under R5 the witness reported an outage while the county index stayed below the lenient threshold.</p>
          )}
        </section>
      </div>

      <section className="card" style={{ marginTop: 16 }} aria-labelledby="ix-h">
        <h2 id="ix-h">County outage index{e ? ' during the event' : ' during the witness interval'}</h2>
        {trigger.status === 'error' && (
          <p className="small">
            Thresholds: <NotComputed reason={trigger.error instanceof ApiError ? trigger.error.code : trigger.error.message} />
          </p>
        )}
        {th && th.theta_hi !== null && <p className="small">{thetaHiWording(th.theta_hi_status).long}</p>}
        {th && <ProvenanceDetails provenance={th.provenance} label="Threshold provenance" />}
        {r.index_series.length === 0 ? (
          <Empty>No index data for this window.</Empty>
        ) : (
          <>
            <IndexChart
              points={r.index_series}
              thetaHi={th?.theta_hi ?? null}
              thetaLo={th?.theta_lo ?? null}
              thetaHiNote={thetaHiWording(th?.theta_hi_status).short}
              markers={markers}
              fips={c.fips}
              height={300}
              label={`County outage index for claim ${c.id}`}
            />
            <SeriesTable points={r.index_series} fips={c.fips} />
          </>
        )}
      </section>

      <section className="card" style={{ marginTop: 16 }} aria-labelledby="wl-h">
        <h2 id="wl-h">Witness log used</h2>
        {intervals.length > 0 && (
          <ul>
            {intervals.map((iv) => (
              <li key={iv.start}>
                Power out from {formatUtc(iv.start)} to {iv.end ? formatUtc(iv.end) : 'not reported back'}
                {iv.hours !== null ? ` (${formatHours(iv.hours)})` : ''}
                {iv.basis === 'sim_time' ? ', on the replay clock' : ''} {iv.selfReported && <SelfReportedChip />}
              </li>
            ))}
          </ul>
        )}
        <p className="small muted">
          Outage intervals come only from “power lost” and “power back” entries. Heartbeats show the page was open; a missing
          heartbeat is not treated as a power cut.
        </p>
        {r.witness_entries.length === 0 ? <Empty>No witness entries were used for this decision.</Empty> : <WitnessTable entries={r.witness_entries} />}
      </section>
    </>
  );
}

const KIND_LABEL: Record<WitnessEntryRow['kind'], string> = {
  heartbeat: 'Heartbeat',
  power_lost: 'Charger lost power',
  power_restored: 'Charger power back',
  attest_lost: 'Member: power is out',
  attest_restored: 'Member: power is back',
};

function WitnessTable({ entries }: { entries: WitnessEntryRow[] }) {
  return (
    <div className="table-wrap">
      <table className="plain">
        <thead>
          <tr>
            <th className="num">Seq</th>
            <th>Entry</th>
            <th>Device time</th>
            <th>Replay time</th>
            <th>Check</th>
            <th>Hash</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((w) => (
            <tr key={w.id}>
              <td className="num">{w.seq}</td>
              <td>
                {KIND_LABEL[w.kind]} {isSelfReported(w.kind) && <SelfReportedChip />}
              </td>
              <td>{formatUtc(w.device_time)}</td>
              <td>{w.sim_time ? formatUtc(w.sim_time) : '—'}</td>
              <td>{w.valid ? '✓ signature and chain valid' : `✕ rejected: ${w.reject_reason ?? 'unknown'}`}</td>
              <td>
                <code title={w.hash}>{w.hash.slice(0, 10)}…</code>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
