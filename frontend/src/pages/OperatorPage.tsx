import { AllCommunityModule, ModuleRegistry, type ColDef } from 'ag-grid-community';
import { AgGridReact } from 'ag-grid-react';
import { useApi } from '../api/context';
import type { Policy } from '../api/types';
import { PageHeader } from '../components/Layout';
import { Probe } from '../components/Probe';
import { Link } from '../lib/router';
import { formatCents } from '../lib/money';

// Community modules only (MIT). No Enterprise module is installed.
ModuleRegistry.registerModules([AllCommunityModule]);

const policyColumns: ColDef<Policy>[] = [
  { field: 'id', headerName: 'Policy' },
  { field: 'fips', headerName: 'County FIPS' },
  { field: 'status', headerName: 'Status' },
  { field: 'cover_cents', headerName: 'Cover', valueFormatter: (p) => (typeof p.value === 'number' ? formatCents(p.value) : '') },
  { field: 'premium_cents', headerName: 'Premium', valueFormatter: (p) => (typeof p.value === 'number' ? formatCents(p.value) : '') },
];

// K3 empty page with one plain grid to prove AG Grid Community builds and renders. F6 builds the
// policies, claims and ledger grids properly.
export function OperatorPage() {
  const api = useApi();
  return (
    <>
      <PageHeader title="Operator" purpose="Policies, claims and ledger grids." />
      <Probe
        what="policies"
        loadKey="policies"
        load={() => api.policies()}
        summary={(r) => (
          <>
            {r.policies.length} policies.{' '}
            {r.policies[0] && <Link to={`/policies/${r.policies[0].id}`}>Open the first one</Link>}
            <div className="grid-box">
              <AgGridReact<Policy> rowData={r.policies} columnDefs={policyColumns} domLayout="autoHeight" />
            </div>
          </>
        )}
      />
      <Probe
        what="claims"
        loadKey="claims"
        load={() => api.claims()}
        summary={(r) => (
          <>
            {r.claims.length} claims. {r.claims[0] && <Link to={`/claims/${r.claims[0].id}`}>Open the first one</Link>}
          </>
        )}
      />
      <Probe what="ledger" loadKey="ledger" load={() => api.ledger()} summary={(r) => `${r.entries.length} entries, balance ${formatCents(r.balance_cents)}`} />
    </>
  );
}
