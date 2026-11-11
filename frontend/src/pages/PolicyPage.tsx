import { useApi } from '../api/context';
import type { PolicyId } from '../api/types';
import { PageHeader } from '../components/Layout';
import { Probe } from '../components/Probe';
import { Link, useLocation } from '../lib/router';
import { formatCents } from '../lib/money';

export function PolicyPage({ id }: { id: string }) {
  const api = useApi();
  const { search } = useLocation();
  const enrolled = search.get('enrolled');
  return (
    <>
      <PageHeader
        title="Policy"
        purpose="Policy status, premium history, witness device status and a link to the witness page, claims."
      />
      {enrolled === '1' && (
        <p role="status" className="notice">
          Wallet saved and first premium requested.
        </p>
      )}
      {enrolled === '0' && (
        <p role="alert" className="notice notice-error">
          Enrolment did not finish ({search.get('error') ?? 'no error code given'}). You can try again from the enrol page.
        </p>
      )}
      <Probe
        what={`policy ${id}`}
        loadKey={`policy:${id}`}
        load={() => api.policy(id as PolicyId)}
        summary={(r) =>
          `${r.policy.status}, cover ${formatCents(r.policy.cover_cents)}, premium ${formatCents(r.policy.premium_cents)}/month, ` +
          `${r.premium_charges.length} charges, ${r.devices.length} devices, ${r.claims.length} claims`
        }
      />
      <p>
        <Link to={`/witness/${encodeURIComponent(id)}`}>Witness page for this policy</Link>
      </p>
    </>
  );
}
