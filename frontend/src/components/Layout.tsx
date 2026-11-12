import type { ReactNode } from 'react';
import { useApi } from '../api/context';
import { Link, useLocation } from '../lib/router';

const NAV: { to: string; label: string }[] = [
  { to: '/', label: 'Home' },
  { to: '/enrol', label: 'Get cover' },
  { to: '/live', label: 'Live replay' },
  { to: '/operator', label: 'Operator' },
  { to: '/evidence', label: 'Evidence' },
];

export const FRAMING =
  'FourHour is the pricing, trigger and claims engine a licensed carrier would run; a sandbox prototype, not an insurance offer.';

export function Layout({ children }: { children: ReactNode }) {
  const api = useApi();
  const { pathname } = useLocation();
  const current = (to: string) => (to === '/' ? pathname === '/' : pathname.startsWith(to));
  return (
    <div className="app">
      {api.mode === 'mock' && (
        <div className="mock-banner" role="note">
          <strong>Mock data.</strong> Every value on this page is a made-up fixture from frontend/src/mock, not a result.
        </div>
      )}
      <header className="site-header">
        <Link to="/" className="brand" aria-label="FourHour home">
          <span className="brand-mark" aria-hidden="true" />
          FourHour
        </Link>
        <nav aria-label="Main">
          <ul>
            {NAV.map((n) => (
              <li key={n.to}>
                <Link to={n.to} aria-current={current(n.to) ? 'page' : undefined}>
                  {n.label}
                </Link>
              </li>
            ))}
          </ul>
        </nav>
      </header>
      <main className="site-main">{children}</main>
      <footer className="site-footer">{FRAMING} PayPal calls go to the PayPal sandbox only.</footer>
    </div>
  );
}

export function PageHeader({ title, purpose, children }: { title: string; purpose?: string; children?: ReactNode }) {
  return (
    <header className="page-header">
      <div className="row">
        <h1>{title}</h1>
        {children}
      </div>
      {purpose && <p className="purpose">{purpose}</p>}
    </header>
  );
}
