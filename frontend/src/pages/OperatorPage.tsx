/**
 * F6: operator grids (AG Grid Community only). Policies, claims and the ledger, with sorting,
 * column filters, a quick search, grouped columns on claims, status and money cell renderers, and
 * a detail panel for the selected claim's explanation. Row master/detail is an Enterprise feature,
 * so the explanation opens in a panel under the grid instead.
 */
import {
  AllCommunityModule,
  ModuleRegistry,
  themeQuartz,
  type ColDef,
  type ColGroupDef,
  type ICellRendererParams,
  type ValueFormatterParams,
} from 'ag-grid-community';
import { AgGridReact } from 'ag-grid-react';
import { useMemo, useState } from 'react';
import { useApi } from '../api/context';
import type { ClaimListItem, LedgerEntry, LedgerResponse, Policy } from '../api/types';
import { PageHeader } from '../components/Layout';
import {
  AsyncView,
  DecisionPill,
  Empty,
  PayoutStatusPill,
  PolicyStatusPill,
  RULE_TEXT,
  SandboxChip,
  SimulatedChip,
  TestCoverChip,
  TIER_LABEL,
  isTestCover,
} from '../components/ui';
import { formatUtc } from '../lib/format';
import { formatCents } from '../lib/money';
import { Link } from '../lib/router';
import { useAsync } from '../lib/useAsync';

// Community modules only (MIT). No Enterprise module is installed.
ModuleRegistry.registerModules([AllCommunityModule]);

const gridTheme = themeQuartz.withParams({
  fontFamily: 'inherit',
  fontSize: 15,
  headerFontSize: 13,
  rowHeight: 44,
  headerHeight: 42,
  accentColor: 'var(--accent)',
  backgroundColor: 'var(--surface)',
  foregroundColor: 'var(--text)',
  borderColor: 'var(--line)',
  headerBackgroundColor: 'var(--surface-2)',
  chromeBackgroundColor: 'var(--surface-2)',
});

const money = <T,>(p: ValueFormatterParams<T>): string => (typeof p.value === 'number' ? formatCents(p.value) : '');
const signedMoney = <T,>(p: ValueFormatterParams<T>): string =>
  typeof p.value === 'number' ? formatCents(p.value, { signed: true }) : '';
const time = <T,>(p: ValueFormatterParams<T>): string => (typeof p.value === 'string' ? formatUtc(p.value) : '—');

const defaultColDef: ColDef = { sortable: true, resizable: true, filter: true, floatingFilter: true, minWidth: 110 };

type Tab = 'claims' | 'policies' | 'ledger';

export function OperatorPage() {
  const [tab, setTab] = useState<Tab>('claims');
  const [search, setSearch] = useState('');
  const tabs: { id: Tab; label: string }[] = [
    { id: 'claims', label: 'Claims' },
    { id: 'policies', label: 'Policies' },
    { id: 'ledger', label: 'Ledger' },
  ];
  return (
    <>
      <PageHeader
        title="Operator"
        purpose="The pool as the carrier or program administrator sees it: every claim and its decision, every policy, and the append-only ledger."
      />
      <p className="row small">
        <SimulatedChip what="members" /> <SandboxChip /> Members are simulated people; every amount moved through the PayPal sandbox.
      </p>
      <div className="tabs" role="tablist" aria-label="Operator views">
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            role="tab"
            id={`tab-${t.id}`}
            aria-selected={tab === t.id}
            aria-controls={`panel-${t.id}`}
            onClick={() => setTab(t.id)}
          >
            {t.label}
          </button>
        ))}
        <span style={{ flex: 1 }} />
        <label htmlFor="grid-search" className="visually-hidden">
          Search the grid
        </label>
        <input id="grid-search" type="text" placeholder="Search this grid…" value={search} onChange={(e) => setSearch(e.target.value)} />
      </div>
      <div role="tabpanel" id={`panel-${tab}`} aria-labelledby={`tab-${tab}`}>
        {tab === 'claims' && <ClaimsGrid search={search} />}
        {tab === 'policies' && <PoliciesGrid search={search} />}
        {tab === 'ledger' && <LedgerGrid search={search} />}
      </div>
    </>
  );
}

// ---------- claims ----------

function ClaimsGrid({ search }: { search: string }) {
  const api = useApi();
  const state = useAsync(() => api.claims(), 'claims');
  const [selected, setSelected] = useState<ClaimListItem | null>(null);
  const columns = useMemo<(ColDef<ClaimListItem> | ColGroupDef<ClaimListItem>)[]>(
    () => [
      {
        headerName: 'Claim',
        children: [
          {
            field: 'id',
            headerName: 'ID',
            minWidth: 190,
            cellRenderer: (p: ICellRendererParams<ClaimListItem, string>) =>
              p.value ? (
                <Link to={`/claims/${p.value}`} className="cell-link">
                  {p.value}
                </Link>
              ) : null,
          },
          { field: 'member_name', headerName: 'Member (simulated)', minWidth: 180 },
          { field: 'fips', headerName: 'County', maxWidth: 130 },
          { field: 'sim_time', headerName: 'Decided (replay clock)', valueFormatter: time, minWidth: 200, sort: 'desc' },
        ],
      },
      {
        headerName: 'Decision',
        children: [
          { field: 'tier', headerName: 'Tier', valueFormatter: (p) => (p.value ? TIER_LABEL[p.value as 't4' | 't24'] : '') },
          { field: 'rule', headerName: 'Rule', maxWidth: 110 },
          {
            field: 'decision',
            headerName: 'Decision',
            cellRenderer: (p: ICellRendererParams<ClaimListItem, ClaimListItem['decision']>) => (p.value ? <DecisionPill decision={p.value} /> : null),
            minWidth: 190,
          },
          { field: 'amount_cents', headerName: 'Amount', type: 'rightAligned', valueFormatter: money, filter: 'agNumberColumnFilter' },
        ],
      },
      {
        headerName: 'Payout (PayPal sandbox)',
        children: [
          {
            colId: 'payout_status',
            headerName: 'Status',
            valueGetter: (p) => p.data?.payout?.status ?? 'none',
            cellRenderer: (p: ICellRendererParams<ClaimListItem>) => <PayoutStatusPill status={p.data?.payout?.status ?? null} />,
            minWidth: 150,
          },
          { colId: 'confirmed_via', headerName: 'Confirmed by', valueGetter: (p) => p.data?.payout?.confirmed_via ?? '—' },
          { colId: 'batch', headerName: 'PayPal batch', valueGetter: (p) => p.data?.payout?.paypal_batch_id ?? '—', minWidth: 170 },
        ],
      },
    ],
    [],
  );
  return (
    <AsyncView state={state} what="claims">
      {(r) =>
        r.claims.length === 0 ? (
          <Empty>No claims yet. Run a replay on the Live page; claims appear here as they are decided.</Empty>
        ) : (
          <>
            <p className="small muted">Select a row (click, or Space on the keyboard) to see why the claim was decided that way.</p>
            <div className="grid-box grid-box-fixed">
              <AgGridReact<ClaimListItem>
                theme={gridTheme}
                rowData={r.claims}
                columnDefs={columns}
                defaultColDef={defaultColDef}
                quickFilterText={search}
                suppressColumnVirtualisation
                getRowId={(p) => p.data.id}
                rowSelection={{ mode: 'singleRow', checkboxes: false, enableClickSelection: true }}
                onSelectionChanged={(e) => setSelected(e.api.getSelectedRows()[0] ?? null)}
              />
            </div>
            <ClaimDetail claim={selected} />
          </>
        )
      }
    </AsyncView>
  );
}

function ClaimDetail({ claim }: { claim: ClaimListItem | null }) {
  if (!claim) return null;
  return (
    <section className="card detail-panel" aria-live="polite" aria-label={`Claim ${claim.id} explained`}>
      <div className="card-title">
        <DecisionPill decision={claim.decision} />
        <strong className="num">{formatCents(claim.amount_cents)}</strong>
        <span className="muted">
          {TIER_LABEL[claim.tier]} · {claim.member_name}
        </span>
        <span className="spacer" />
        <Link to={`/claims/${claim.id}`}>Full explanation with the index curve and witness log</Link>
      </div>
      <p>{claim.reason}</p>
      <p className="small">
        <strong>Rule {claim.rule}.</strong> {RULE_TEXT[claim.rule]}
      </p>
      <p className="small muted">
        Outage event <code>{claim.outage_event_id ?? 'none (R5)'}</code>
        {claim.witness_entry_id ? (
          <>
            {' '}
            · witness entry <code>{claim.witness_entry_id}</code>
          </>
        ) : null}
        {claim.payout ? (
          <>
            {' '}
            · payout key <code>{claim.payout.sender_batch_id}</code>
          </>
        ) : null}
      </p>
    </section>
  );
}

// ---------- policies ----------

function PoliciesGrid({ search }: { search: string }) {
  const api = useApi();
  const state = useAsync(() => api.policies(), 'policies');
  const columns = useMemo<ColDef<Policy>[]>(
    () => [
      {
        field: 'id',
        headerName: 'Policy',
        minWidth: 190,
        cellRenderer: (p: ICellRendererParams<Policy, string>) =>
          p.value ? (
            <Link to={`/policies/${p.value}`} className="cell-link">
              {p.value}
            </Link>
          ) : null,
      },
      {
        field: 'status',
        headerName: 'Status',
        cellRenderer: (p: ICellRendererParams<Policy, Policy['status']>) => (p.value ? <PolicyStatusPill status={p.value} /> : null),
        minWidth: 190,
      },
      { field: 'fips', headerName: 'County', maxWidth: 130 },
      { field: 'cover_cents', headerName: 'Cover', type: 'rightAligned', valueFormatter: money, filter: 'agNumberColumnFilter' },
      { field: 'premium_cents', headerName: 'Premium / month', type: 'rightAligned', valueFormatter: money, filter: 'agNumberColumnFilter' },
      {
        field: 'model_version',
        headerName: 'Priced by',
        minWidth: 160,
        cellRenderer: (p: ICellRendererParams<Policy>) => (p.data && isTestCover(p.data) ? <TestCoverChip /> : <span>{p.value}</span>),
      },
      { field: 'created_at', headerName: 'Started', valueFormatter: time, minWidth: 200, sort: 'desc' },
    ],
    [],
  );
  return (
    <AsyncView state={state} what="policies">
      {(r) =>
        r.policies.length === 0 ? (
          <Empty>No policies yet. Members enrol from the Get cover page.</Empty>
        ) : (
          <div className="grid-box grid-box-fixed">
            <AgGridReact<Policy>
              theme={gridTheme}
              rowData={r.policies}
              columnDefs={columns}
              defaultColDef={defaultColDef}
              quickFilterText={search}
                suppressColumnVirtualisation
              getRowId={(p) => p.data.id}
            />
          </div>
        )
      }
    </AsyncView>
  );
}

// ---------- ledger ----------

function LedgerGrid({ search }: { search: string }) {
  const api = useApi();
  const state = useAsync(() => api.ledger(), 'ledger');
  return (
    <AsyncView state={state} what="the ledger">
      {(r) => <LedgerView r={r} search={search} />}
    </AsyncView>
  );
}

function LedgerView({ r, search }: { r: LedgerResponse; search: string }) {
  const columns = useMemo<ColDef<LedgerEntry>[]>(
    () => [
      { field: 'at', headerName: 'When', valueFormatter: time, minWidth: 200, sort: 'desc' },
      { field: 'kind', headerName: 'Kind', maxWidth: 140 },
      {
        field: 'amount_cents',
        headerName: 'Amount (+ into pool)',
        type: 'rightAligned',
        valueFormatter: signedMoney,
        filter: 'agNumberColumnFilter',
        minWidth: 180,
      },
      { field: 'policy_id', headerName: 'Policy', minWidth: 190 },
      { field: 'ref_id', headerName: 'Reference', minWidth: 190 },
      { field: 'id', headerName: 'Entry', minWidth: 190 },
    ],
    [],
  );
  const pinned = useMemo(() => [{ at: null, kind: 'balance', amount_cents: r.balance_cents, policy_id: '', ref_id: 'Pool balance (from the backend)', id: '' }], [r.balance_cents]);
  return (
    <>
      <p style={{ fontSize: '1.3rem' }}>
        Pool balance <strong className="num">{formatCents(r.balance_cents, { signed: true })}</strong>{' '}
        <span className="small muted">(the sum of all ledger entries, computed by the backend; append-only)</span>
      </p>
      {r.entries.length === 0 ? (
        <Empty>The ledger is empty. Premiums and payouts appear here once PayPal confirms them.</Empty>
      ) : (
        <div className="grid-box grid-box-fixed">
          <AgGridReact<LedgerEntry>
            theme={gridTheme}
            rowData={r.entries}
            columnDefs={columns}
            defaultColDef={defaultColDef}
            quickFilterText={search}
                suppressColumnVirtualisation
            getRowId={(p) => p.data.id}
            pinnedBottomRowData={pinned}
          />
        </div>
      )}
    </>
  );
}
