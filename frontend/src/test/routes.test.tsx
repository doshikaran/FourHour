/**
 * K3 "every route renders against the mock": each route in CONTRACTS.md section 9 is rendered with
 * the mock API and must show its heading and finish every data probe without an error.
 */
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { App, ROUTES } from '../App';
import { createMockApi } from '../mock/mockApi';
import { fixtureClaim, fixturePolicy } from '../mock/fixtures';

afterEach(cleanup);

const CASES: { path: string; pattern: string; heading: string }[] = [
  { path: '/', pattern: '/', heading: 'FourHour' },
  { path: '/enrol', pattern: '/enrol', heading: 'Enrol' },
  { path: `/policies/${fixturePolicy.id}`, pattern: '/policies/:id', heading: 'Policy' },
  { path: `/witness/${fixturePolicy.id}`, pattern: '/witness/:policyId', heading: 'Witness' },
  { path: '/live', pattern: '/live', heading: 'Live replay' },
  { path: `/claims/${fixtureClaim.id}`, pattern: '/claims/:id', heading: 'Claim' },
  { path: '/operator', pattern: '/operator', heading: 'Operator' },
  { path: '/evidence', pattern: '/evidence', heading: 'Evidence' },
];

function renderAt(path: string) {
  window.history.pushState(null, '', path);
  return render(<App api={createMockApi({ latencyMs: 0 })} />);
}

describe('routes', () => {
  it('covers every route in CONTRACTS.md section 9', () => {
    expect(CASES.map((c) => c.pattern)).toEqual(ROUTES.map((r) => r.pattern));
  });

  for (const c of CASES) {
    it(`renders ${c.path} against the mock`, async () => {
      const { container } = renderAt(c.path);
      expect(screen.getByRole('heading', { level: 1, name: c.heading })).toBeTruthy();
      expect(screen.getByText(/Mock data\./)).toBeTruthy();
      await waitFor(() => {
        expect(container.querySelectorAll('.probe').length).toBeGreaterThan(0);
        expect(container.querySelectorAll('.probe[role="status"]').length).toBe(0);
      });
      const errors = [...container.querySelectorAll('.probe-error')].map((e) => e.textContent);
      expect(errors).toEqual([]);
    });
  }

  it('renders the operator grid rows', async () => {
    const { container } = renderAt('/operator');
    await waitFor(() => expect(container.querySelector('.ag-root')).toBeTruthy());
    await waitFor(() => expect(screen.getAllByText(fixturePolicy.id).length).toBeGreaterThan(0));
  });

  it('shows the error state with the endpoint for an unknown policy', async () => {
    renderAt('/policies/pol_doesnotexist');
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toMatch(/Could not load policy pol_doesnotexist/);
    expect(alert.textContent).toMatch(/policy_not_found/);
  });

  it('shows a not-found page for unknown paths', () => {
    renderAt('/nope');
    expect(screen.getByRole('heading', { level: 1, name: 'Page not found' })).toBeTruthy();
  });

  it('opens the replay stream on /live and receives a snapshot', async () => {
    renderAt('/live');
    await waitFor(() => expect(screen.getByText(/\[fixture\]/)).toBeTruthy());
    await waitFor(() => expect(screen.getByTestId('stream-status').textContent).toBe('open'));
    await waitFor(() => expect(screen.getByTestId('stream-counts').textContent).toMatch(/snapshot 1/));
  });
});
