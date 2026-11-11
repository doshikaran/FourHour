import { useApi } from '../api/context';
import type { ClaimId } from '../api/types';
import { PageHeader } from '../components/Layout';
import { Probe } from '../components/Probe';
import { formatCents } from '../lib/money';

export function ClaimPage({ id }: { id: string }) {
  const api = useApi();
  return (
    <>
      <PageHeader title="Claim" purpose="One claim explained: rule, index curve, witness log, payout status." />
      <Probe
        what={`claim ${id}`}
        loadKey={`claim:${id}`}
        load={() => api.claim(id as ClaimId)}
        summary={(r) =>
          `${r.claim.rule} ${r.claim.decision} ${formatCents(r.claim.amount_cents)}, payout ${r.claim.payout?.status ?? 'none'}, ` +
          `${r.index_series.length} index points, ${r.witness_entries.length} witness entries`
        }
      />
    </>
  );
}
