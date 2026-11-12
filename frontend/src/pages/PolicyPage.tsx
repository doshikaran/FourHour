/**
 * F4: a policy. Status, premium history, witness devices with the link to the witness page, and
 * claims. Lands here after PayPal (?enrolled=1 or ?enrolled=0&error=...).
 */
import { useState } from 'react';
import { useApi } from '../api/context';
import type { PolicyDetailResponse, PolicyId } from '../api/types';
import { PageHeader } from '../components/Layout';
import {
  AsyncView,
  ChargeStatusPill,
  DecisionPill,
  Empty,
  isTestCover,
  PolicyStatusPill,
  SandboxChip,
  SimulatedChip,
  TestCoverChip,
  TIER_LABEL,
} from '../components/ui';
import { formatUtc } from '../lib/format';
import { formatCents } from '../lib/money';
import { Link, useLocation } from '../lib/router';
import { useAsync } from '../lib/useAsync';

export function PolicyPage({ id }: { id: string }) {
  const api = useApi();
  const state = useAsync(() => api.policy(id as PolicyId), `policy:${id}`);
  return (
    <>
      <PageHeader title="Your policy" />
      <AsyncView state={state} what={`policy ${id}`}>
        {(r) => <PolicyView r={r} />}
      </AsyncView>
    </>
  );
}

function EnrolNotice({ r }: { r: PolicyDetailResponse }) {
  const { search } = useLocation();
  const enrolled = search.get('enrolled');
  if (enrolled === '0') {
    return (
      <p role="alert" className="notice notice-error">
        <strong>Enrolment did not finish.</strong> PayPal or the backend reported{' '}
        <code>{search.get('error') ?? 'no error code'}</code>. Nothing further was charged. You can{' '}
        <Link to="/enrol">start again</Link>.
      </p>
    );
  }
  if (enrolled !== '1') return null;
  // Say only what the policy record shows, not what we hope happened.
  const first = r.premium_charges[0];
  if (first?.status === 'completed') {
    return (
      <p role="status" className="notice notice-good">
        <strong>✓ Wallet saved and first premium charged:</strong> {formatCents(first.amount_cents)} for {first.period}.
      </p>
    );
  }
  return (
    <p role="status" className="notice notice-info">
      Wallet saved. The first premium is {first ? `recorded as ${first.status}` : 'not recorded yet'}; refresh in a moment.
    </p>
  );
}

function PolicyView({ r }: { r: PolicyDetailResponse }) {
  const p = r.policy;
  const test = isTestCover(p);
  const witnessPath = `/witness/${encodeURIComponent(p.id)}`;
  return (
    <>
      <EnrolNotice r={r} />
      <div className="grid-2">
        <section className="card" aria-labelledby="pol-h">
          <div className="card-title">
            <h2 id="pol-h">Cover</h2>
            <PolicyStatusPill status={p.status} />
            {test && <TestCoverChip />}
          </div>
          <dl className="kv">
            <dt>Cover</dt>
            <dd>{formatCents(p.cover_cents)}</dd>
            <dt>Premium</dt>
            <dd>{formatCents(p.premium_cents)} a month</dd>
            <dt>County</dt>
            <dd>{p.fips}</dd>
            <dt>Priced by</dt>
            <dd>{test ? 'Not priced: seeded sandbox test cover' : `model ${p.model_version}`}</dd>
            <dt>Started</dt>
            <dd>{formatUtc(p.created_at)}</dd>
            {p.cancelled_at && (
              <>
                <dt>Cancelled</dt>
                <dd>{formatUtc(p.cancelled_at)}</dd>
              </>
            )}
            <dt>Policy ID</dt>
            <dd>
              <code>{p.id}</code>
            </dd>
          </dl>
        </section>
        <section className="card" aria-labelledby="mem-h">
          <div className="card-title">
            <h2 id="mem-h">Member</h2>
            <SimulatedChip what="member" />
          </div>
          <dl className="kv">
            <dt>Name</dt>
            <dd>{r.member.name}</dd>
            <dt>Email</dt>
            <dd>{r.member.email}</dd>
            <dt>Payouts go to</dt>
            <dd>
              {r.member.paypal_email} <SandboxChip />
            </dd>
          </dl>
        </section>
      </div>

      <section className="card" style={{ marginTop: 16 }} aria-labelledby="wit-h">
        <h2 id="wit-h">Witness device</h2>
        <p className="muted">
          A laptop or phone that stays on a charger at your premises keeps a signed log of when its charger loses power. It
          can confirm or contradict a county outage; it can never create one.
        </p>
        {r.devices.length === 0 ? (
          <Empty>
            No device registered yet. Open the witness page on the device that stays plugged in.
          </Empty>
        ) : (
          <div className="table-wrap">
            <table className="plain">
              <thead>
                <tr>
                  <th>Device</th>
                  <th>Registered</th>
                  <th className="num">Entries received</th>
                </tr>
              </thead>
              <tbody>
                {r.devices.map((d) => (
                  <tr key={d.id}>
                    <td>
                      <code>{d.id}</code>
                    </td>
                    <td>{formatUtc(d.registered_at)}</td>
                    <td className="num">{d.last_seq}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <WitnessLink path={witnessPath} />
      </section>

      <section className="card" style={{ marginTop: 16 }} aria-labelledby="prem-h">
        <h2 id="prem-h">Premiums</h2>
        {r.premium_charges.length === 0 ? (
          <Empty>No premium charged yet.</Empty>
        ) : (
          <div className="table-wrap">
            <table className="plain">
              <thead>
                <tr>
                  <th>Period</th>
                  <th className="num">Amount</th>
                  <th>Status</th>
                  <th>PayPal order</th>
                </tr>
              </thead>
              <tbody>
                {r.premium_charges.map((c) => (
                  <tr key={c.id}>
                    <td>{c.period}</td>
                    <td className="num">{formatCents(c.amount_cents)}</td>
                    <td>
                      <ChargeStatusPill status={c.status} />
                    </td>
                    <td>
                      <code>{c.paypal_order_id ?? '—'}</code>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="card" style={{ marginTop: 16 }} aria-labelledby="cl-h">
        <h2 id="cl-h">Claims</h2>
        {r.claims.length === 0 ? (
          <Empty>No claims. Claims are created automatically when an outage qualifies; there is no form.</Empty>
        ) : (
          <div className="table-wrap">
            <table className="plain">
              <thead>
                <tr>
                  <th>Decision</th>
                  <th>Tier</th>
                  <th>Rule</th>
                  <th className="num">Amount</th>
                  <th>Decided</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {r.claims.map((c) => (
                  <tr key={c.id}>
                    <td>
                      <DecisionPill decision={c.decision} />
                    </td>
                    <td>{TIER_LABEL[c.tier]}</td>
                    <td>{c.rule}</td>
                    <td className="num">{formatCents(c.amount_cents)}</td>
                    <td>{formatUtc(c.sim_time ?? c.decided_at)}</td>
                    <td>
                      <Link to={`/claims/${c.id}`}>Explain</Link>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}

function WitnessLink({ path }: { path: string }) {
  const [copied, setCopied] = useState<'idle' | 'ok' | 'failed'>('idle');
  const url = `${window.location.origin}${path}`;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied('ok');
    } catch {
      setCopied('failed');
    }
  };
  return (
    <div className="row" style={{ marginTop: 12 }}>
      <Link to={path} className="button">
        Open the witness page
      </Link>
      <button type="button" className="secondary" onClick={copy}>
        Copy link for another device
      </button>
      <span role="status" className="small">
        {copied === 'ok' ? 'Link copied.' : copied === 'failed' ? `Copy failed. The link is ${url}` : ''}
      </span>
    </div>
  );
}
