import { ApiProvider } from './api/context';
import type { FourHourApi } from './api/client';
import { Layout, PageHeader } from './components/Layout';
import { Link, Router, type RouteDef } from './lib/router';
import { ClaimPage } from './pages/ClaimPage';
import { EnrolPage } from './pages/EnrolPage';
import { EvidencePage } from './pages/EvidencePage';
import { HomePage } from './pages/HomePage';
import { LivePage } from './pages/LivePage';
import { OperatorPage } from './pages/OperatorPage';
import { PolicyPage } from './pages/PolicyPage';
import { WitnessPage } from './pages/WitnessPage';

/** The routes in CONTRACTS.md section 9, in contract order. */
export const ROUTES: RouteDef[] = [
  { pattern: '/', render: () => <HomePage /> },
  { pattern: '/enrol', render: () => <EnrolPage /> },
  { pattern: '/policies/:id', render: (p) => <PolicyPage key={p.id} id={p.id ?? ''} /> },
  { pattern: '/witness/:policyId', render: (p) => <WitnessPage key={p.policyId} policyId={p.policyId ?? ''} /> },
  { pattern: '/live', render: () => <LivePage /> },
  { pattern: '/claims/:id', render: (p) => <ClaimPage key={p.id} id={p.id ?? ''} /> },
  { pattern: '/operator', render: () => <OperatorPage /> },
  { pattern: '/evidence', render: () => <EvidencePage /> },
];

function NotFound() {
  return (
    <>
      <PageHeader title="Page not found" purpose="There is no page at this address." />
      <p>
        <Link to="/">Go to the home page</Link>
      </p>
    </>
  );
}

export function App({ api }: { api: FourHourApi }) {
  return (
    <ApiProvider api={api}>
      <Layout>
        <Router routes={ROUTES} fallback={<NotFound />} />
      </Layout>
    </ApiProvider>
  );
}
