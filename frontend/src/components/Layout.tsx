import type { ReactNode } from 'react';
import { useApi } from '../api/context';
import { Link } from '../lib/router';

const NAV: { to: string; label: string }[] = [
  { to: '/', label: 'Home' },
  { to: '/enrol', label: 'Enrol' },
  { to: '/live', label: 'Live replay' },
  { to: '/operator', label: 'Operator' },
  { to: '/evidence', label: 'Evidence' },
];

export function Layout({ children }: { children: ReactNode }) {
  const api = useApi();
  return (
    <div className="app">
      {api.mode === 'mock' && (
        <div className="mock-banner" role="note">
          <strong>Mock data.</strong> Every value on this page is a made-up fixture from frontend/src/mock, not a result.
        </div>
      )}
      <header className="site-header">
        <Link to="/" className="brand">
          FourHour
        </Link>
        <nav aria-label="Main">
          <ul>
            {NAV.map((n) => (
              <li key={n.to}>
                <Link to={n.to}>{n.label}</Link>
              </li>
            ))}
          </ul>
        </nav>
      </header>
      <main className="site-main">{children}</main>
      <footer className="site-footer">
        FourHour is the pricing, trigger and claims engine a licensed carrier would run; a sandbox prototype, not an
        insurance offer.
      </footer>
    </div>
  );
}

export function PageHeader({ title, purpose }: { title: string; purpose: string }) {
  return (
    <header className="page-header">
      <h1>{title}</h1>
      <p className="purpose">{purpose}</p>
    </header>
  );
}
