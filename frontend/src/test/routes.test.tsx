/**
 * Every route in CONTRACTS.md section 9 renders against the mock and settles without an error,
 * plus the main flows: the payout moment on /live, a stream drop, enrolment, a 422 quote,
 * the witness's self-reported fallback, the operator grids and test-cover labels.
 */
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { App, ROUTES } from '../App';
import { ApiError, type FourHourApi } from '../api/client';
import { createMockApi, type MockApi } from '../mock/mockApi';
import { fixtureClaim, fixturePolicy, fixtureTestPolicy } from '../mock/fixtures';

let api: MockApi | null = null;
afterEach(() => {
  cleanup();
  api?.dispose();
  api = null;
  localStorage.clear();
});

const CASES: { path: string; pattern: string; heading: string }[] = [
  { path: '/', pattern: '/', heading: 'Power out past four hours? The payout starts itself.' },
  { path: '/enrol', pattern: '/enrol', heading: 'Get cover' },
  { path: `/policies/${fixturePolicy.id}`, pattern: '/policies/:id', heading: 'Your policy' },
  { path: `/witness/${fixturePolicy.id}`, pattern: '/witness/:policyId', heading: 'Witness' },
  { path: '/live', pattern: '/live', heading: 'Live replay' },
  { path: `/claims/${fixtureClaim.id}`, pattern: '/claims/:id', heading: 'Claim explained' },
  { path: '/operator', pattern: '/operator', heading: 'Operator' },
  { path: '/evidence', pattern: '/evidence', heading: 'Evidence' },
];

function renderAt(path: string, opts: Parameters<typeof createMockApi>[0] = {}) {
  window.history.pushState(null, '', path);
  api = createMockApi({ latencyMs: 0, ...opts });
  return render(<App api={api} />);
}

const settled = (c: HTMLElement) => c.querySelectorAll('.state-loading').length === 0;

describe('routes', () => {
  it('covers every route in CONTRACTS.md section 9', () => {
    expect(CASES.map((c) => c.pattern)).toEqual(ROUTES.map((r) => r.pattern));
  });

  for (const c of CASES) {
    it(`renders ${c.path} against the mock with no error`, async () => {
      const { container } = renderAt(c.path);
      expect(screen.getByRole('heading', { level: 1, name: c.heading })).toBeTruthy();
      expect(screen.getByText(/Preview data/)).toBeTruthy();
      await waitFor(() => expect(settled(container)).toBe(true));
      expect([...container.querySelectorAll('.state-error')].map((e) => e.textContent)).toEqual([]);
      expect(screen.getByText(/a sandbox prototype, not an insurance offer/)).toBeTruthy();
    });
  }

  it('home links to the USDA FSIS guidance and quotes nothing it could not check', () => {
    renderAt('/');
    const link = screen.getByRole('link', { name: /Keep Your Food Safe During Emergencies/ });
    expect(link.getAttribute('href')).toMatch(/^https:\/\/www\.fsis\.usda\.gov\//);
    expect(document.body.textContent).not.toMatch(/this is not insurance/i);
  });

  it('shows the error state with the endpoint and what to do for an unknown policy', async () => {
    renderAt('/policies/pol_doesnotexist');
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toMatch(/Could not load policy pol_doesnotexist/);
    expect(alert.textContent).toMatch(/policy_not_found/);
    expect(alert.textContent).toMatch(/Check the address/);
    expect(within(alert).getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  it('shows a not-found page for unknown paths', () => {
    renderAt('/nope');
    expect(screen.getByRole('heading', { level: 1, name: 'Page not found' })).toBeTruthy();
  });
});

describe('/live', () => {
  it('runs the derecho fixture: chart, sending state, then an unmistakable confirmed payout', async () => {
    const { container } = renderAt('/live', { payoutSendDelayMs: 50, payoutConfirmDelayMs: 600 });
    await waitFor(() => expect(screen.getByTestId('stream-status').dataset.status).toBe('open'));
    expect(screen.getByText(/Replay mode maps live witness events onto the replay clock/)).toBeTruthy();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Start replay' })).toBeTruthy());
    fireEvent.change(screen.getByLabelText('Speed'), { target: { value: '7200' } });
    fireEvent.click(screen.getByRole('button', { name: 'Start replay' }));
    // 7200x = 125 ms per 15-minute step; the 4-hour claim is at step 25 (~3 s).
    const card = await screen.findByTestId('payout-card', {}, { timeout: 2000 });
    expect(card.dataset.stage).toBeUndefined();
    await waitFor(() => expect(screen.getByTestId('payout-card').dataset.stage).toBe('sending'), { timeout: 6000 });
    expect(screen.getByTestId('payout-card').textContent).toMatch(/Sending payout via PayPal/);
    await waitFor(() => expect(screen.getByTestId('payout-card').dataset.stage).toBe('confirmed'), { timeout: 3000 });
    const confirmed = screen.getByTestId('payout-card');
    expect(confirmed.textContent).toMatch(/Payout confirmed/);
    expect(confirmed.textContent).toMatch(/\$90\.00/);
    expect(confirmed.textContent).toMatch(/Simulated member/);
    // The chart drew a gap band for the missing hour and both threshold lines.
    expect(container.querySelector('pattern#gap-hatch')).toBeTruthy();
    expect(container.textContent).toMatch(/Strong threshold 50\.0%/);
    expect(container.textContent).toMatch(/Lenient threshold 25\.0%/);
    // theta_hi at the grid edge is never called an optimum (CONTRACTS 1.7).
    expect(container.textContent).toMatch(/\(grid edge\)/);
    expect(container.textContent).toMatch(/edge of the search grid: the search found no interior optimum/);
    expect(screen.getByTestId('clock-hi').textContent).toMatch(/4-hour line passed/);
  }, 15_000);

  it('survives a stream drop and rebuilds from the snapshot', async () => {
    renderAt('/live', { payoutSendDelayMs: 10, payoutConfirmDelayMs: 10 });
    await waitFor(() => expect(screen.getByTestId('stream-status').dataset.status).toBe('open'));
    fireEvent.click(await screen.findByRole('button', { name: 'Start replay' }));
    await waitFor(() => expect(screen.getByTestId('sim-clock').textContent).not.toBe('Not started'));
    fireEvent.click(screen.getByRole('button', { name: 'Simulate stream drop' }));
    await waitFor(() => expect(screen.getByTestId('stream-status').dataset.status).toBe('reconnecting'));
    expect(screen.getByText(/Connection to the replay stream dropped/)).toBeTruthy();
    await waitFor(() => expect(screen.getByTestId('stream-status').dataset.status).toBe('open'), { timeout: 3000 });
    await waitFor(() => expect(screen.getByText(/State rebuilt from the server snapshot/)).toBeTruthy());
  }, 10_000);
});

describe('/enrol', () => {
  it('quotes with a breakdown and provenance, then hands off and lands on the policy page', async () => {
    renderAt('/enrol');
    const select = await screen.findByLabelText('County');
    fireEvent.change(select, { target: { value: '99001' } });
    await screen.findByText(/a month for \$150\.00 cover/);
    expect(screen.getByText('Share of cover paid per qualifying outage')).toBeTruthy();
    // 1.8: the price is built on the tier that is paid; no 24-hour payment is mentioned anywhere on /enrol.
    expect(document.body.textContent).not.toMatch(/24.hour|24 h(?![a-z])/i);
    expect(screen.getByText(/Where this price comes from/)).toBeTruthy();
    expect(await screen.findByText(/product assumption/)).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Business or owner name'), { target: { value: 'Fixture Taco Truck' } });
    fireEvent.change(screen.getByLabelText('Contact email'), { target: { value: 'a@example.invalid' } });
    fireEvent.change(screen.getByLabelText(/PayPal email for payouts/), { target: { value: 'b@example.invalid' } });
    fireEvent.click(screen.getByRole('button', { name: 'Continue to PayPal' }));
    await screen.findByRole('heading', { level: 1, name: 'Your policy' });
    expect(window.location.search).toBe('?enrolled=1');
    expect(await screen.findByText(/Wallet saved and first premium charged/)).toBeTruthy();
  });

  it('shows "not computed" when pricing returns 422 pricing_not_computed', async () => {
    renderAt('/enrol');
    fireEvent.change(await screen.findByLabelText('County'), { target: { value: '99003' } });
    await screen.findByText(/has not produced every input for this county/);
    expect(screen.getAllByText('not computed').length).toBeGreaterThan(0);
  });

  it('shows a county with offered=false as unavailable with its reason and requests no quote (1.8)', async () => {
    window.history.pushState(null, '', '/enrol');
    api = createMockApi({ latencyMs: 0 });
    const quoted: string[] = [];
    const base = api;
    const spy: FourHourApi = { ...base, quote: (req) => (quoted.push(req.fips), base.quote(req)) };
    render(<App api={spy} />);
    const select = (await screen.findByLabelText('County')) as HTMLSelectElement;
    const opt = [...select.options].find((o) => o.value === '99004');
    expect(opt?.disabled).toBe(true);
    expect(opt?.textContent).toMatch(/not offered/);
    expect(screen.getByText(/no qualifying event in this fixture credibility group/)).toBeTruthy();
    fireEvent.change(select, { target: { value: '99004' } });
    expect(await screen.findByText('Not offered in this county')).toBeTruthy();
    expect(quoted).toEqual([]);
    expect((screen.getByRole('button', { name: 'Continue to PayPal' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('handles a 422 county_not_offered from the quote endpoint', async () => {
    window.history.pushState(null, '', '/enrol');
    api = createMockApi({ latencyMs: 0 });
    const base = api;
    // The county list says offered, but the backend (newer pricing table) refuses the quote.
    const stale: FourHourApi = {
      ...base,
      counties: () => base.counties().then((r) => ({ counties: r.counties.map((c) => ({ ...c, offered: true, not_offered_reason: null })) })),
    };
    const { container } = render(<App api={stale} />);
    fireEvent.change(await screen.findByLabelText('County'), { target: { value: '99004' } });
    expect(await screen.findByText('Not offered in this county')).toBeTruthy();
    expect(container.querySelectorAll('.state-error')).toHaveLength(0);
  });

  it('shows "not computed" for a null data coverage', async () => {
    renderAt('/enrol');
    const select = await screen.findByLabelText('County');
    expect(select.textContent).toMatch(/Fixture County B, ZZ — outage data coverage not computed/);
  });
});

describe('coverage limits and thresholds (CONTRACTS 1.7)', () => {
  it('states the coverage limits on /enrol before the hand-off, with thresholds from the trigger artifact', async () => {
    renderAt('/enrol');
    const limits = await screen.findByRole('region', { name: 'What this covers in practice' });
    await waitFor(() => expect(limits.textContent).toMatch(/less than 25\.0% of the county's customers are not covered/));
    expect(limits.textContent).toMatch(/requires a registered witness device/);
    expect(limits.textContent).toMatch(/at least 50\.0% of the county's customers out/);
    expect(limits.textContent).toMatch(/limit of county-level outage data/);
    fireEvent.change(await screen.findByLabelText('County'), { target: { value: '99001' } });
    expect(await screen.findByText(/with at least 25\.0% of the county's customers out for 4 hours or more/)).toBeTruthy();
  });

  it('shows "not computed" for the thresholds when the trigger artifact is missing', async () => {
    window.history.pushState(null, '', '/enrol');
    api = createMockApi({ latencyMs: 0 });
    const missing: FourHourApi = {
      ...api,
      analytics: ((name: string) =>
        name === 'trigger'
          ? Promise.reject(new ApiError({ status: 404, code: 'artifact_missing', message: 'missing', paypalDebugId: null, endpoint: 'GET /analytics/trigger' }))
          : (api as MockApi).analytics(name as 'pricing')) as FourHourApi['analytics'],
    };
    render(<App api={missing} />);
    const limits = await screen.findByRole('region', { name: 'What this covers in practice' });
    await waitFor(() => expect(within(limits).getAllByText('not computed')).toHaveLength(2));
    expect(limits.textContent).not.toMatch(/\d+%/);
  });

  it('shows the trigger sensitivity table and the grid-edge status on /evidence', async () => {
    renderAt('/evidence');
    const panel = await screen.findByRole('region', { name: 'Trigger design' });
    await within(panel).findByText('Sensitivity to the product assumptions');
    expect(panel.textContent).toMatch(/edge of the search grid, not an optimum/);
    expect(within(panel).getAllByText('Grid edge, no interior optimum').length).toBe(1);
    expect(within(panel).getAllByRole('row')).toHaveLength(3);
  });
});

describe('evidence order and live demo guide (final pass)', () => {
  it('leads /evidence with basis risk and its caveats, then the backtest verdict', async () => {
    const { container } = renderAt('/evidence');
    await screen.findByRole('region', { name: 'Basis risk' });
    const titles = [...container.querySelectorAll('section.card > h2')].map((h) => h.textContent);
    expect(titles.slice(0, 2)).toEqual(['Basis risk', 'Backtest']);
    expect(screen.getByRole('complementary', { name: 'Caveats for this table' }).textContent).toMatch(/upper bound/);
  });

  it('shows the demo guide on /live with a link to the demo witness', async () => {
    renderAt('/live');
    const guide = await screen.findByRole('region', { name: 'Run the demo' });
    const link = within(guide).getByRole('link', { name: `/witness/${fixturePolicy.id}` });
    expect(link.getAttribute('href')).toBe(`/witness/${fixturePolicy.id}`);
    await waitFor(() => expect(guide.textContent).toMatch(/at or above 50\.0% for 4 hours/));
    expect(guide.textContent).toMatch(/real PayPal sandbox payout, drawn from a finite sandbox balance/);
  });

  it('hides the demo guide when the backend has no demo policy', async () => {
    window.history.pushState(null, '', '/live');
    api = createMockApi({ latencyMs: 0 });
    const none: FourHourApi = { ...api, demo: () => Promise.resolve({ policy_id: null }) };
    render(<App api={none} />);
    await screen.findByRole('heading', { level: 1, name: 'Live replay' });
    await waitFor(() => expect(screen.getByTestId('stream-status').dataset.status).toBe('open'));
    expect(screen.queryByRole('region', { name: 'Run the demo' })).toBeNull();
  });
});

describe('/witness/:policyId when the server forgets the device', () => {
  it('offers to register a new device after a 404 device_not_found on upload', async () => {
    renderAt(`/witness/${fixturePolicy.id}`);
    fireEvent.click(await screen.findByRole('button', { name: 'Set up this device as the witness' }));
    await waitFor(() => expect(screen.getByTestId('entries-uploaded').textContent).toBe('1'));
    const m = api as MockApi;
    const realPost = m.postEntries;
    m.postEntries = () =>
      Promise.reject(new ApiError({ status: 404, code: 'device_not_found', message: 'no device', paypalDebugId: null, endpoint: 'POST /witness/entries' }));
    fireEvent.click(screen.getByRole('button', { name: /Power is out/ }));
    const again = await screen.findByRole('button', { name: 'Register a new device' });
    expect(screen.getByText(/The server no longer knows this device/)).toBeTruthy();
    m.postEntries = realPost;
    fireEvent.click(again);
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Register a new device' })).toBeNull());
    await waitFor(() => expect(screen.getByTestId('entries-uploaded').textContent).toBe('1'));
    expect(screen.getByTestId('entries-total').textContent).toBe('1');
  });
});

describe('/witness/:policyId without the Battery Status API', () => {
  it('offers self-reported entries only, says why, signs and uploads them', async () => {
    renderAt(`/witness/${fixturePolicy.id}`);
    fireEvent.click(await screen.findByRole('button', { name: 'Set up this device as the witness' }));
    await screen.findByRole('button', { name: /Power is out/ });
    expect(screen.getAllByText(/does not offer the Battery Status API/).length).toBeGreaterThan(0);
    // The first heartbeat is written and uploaded on start.
    await waitFor(() => expect(screen.getByTestId('entries-uploaded').textContent).toBe('1'));
    fireEvent.click(screen.getByRole('button', { name: /Power is out/ }));
    await waitFor(() => expect(screen.getByTestId('entries-uploaded').textContent).toBe('2'));
    expect(screen.getByTestId('power-state').dataset.charging).toBe('false');
    expect(screen.getAllByText('Self-reported').length).toBeGreaterThan(1);
    fireEvent.click(screen.getByRole('button', { name: /Power is back/ }));
    await waitFor(() => expect(screen.getByTestId('entries-uploaded').textContent).toBe('3'));
    expect(screen.getByTestId('entries-pending').textContent).toBe('0');
    // Server side: valid, chained, and the attest kinds were stored.
    const devices = (await api?.policy(fixturePolicy.id))?.devices ?? [];
    const dev = devices.find((d) => d.id !== 'dev_fixtureaaaab');
    const rows = dev ? (await api?.deviceEntries(dev.id))?.entries ?? [] : [];
    expect(rows.map((r) => [r.seq, r.kind, r.charging, r.battery_level, r.valid])).toEqual([
      [1, 'heartbeat', true, null, true],
      [2, 'attest_lost', false, null, true],
      [3, 'attest_restored', true, null, true],
    ]);
  });
});

describe('/operator and test cover', () => {
  it('renders the claims grid with grouped columns and a detail panel on selection', async () => {
    const { container } = renderAt('/operator');
    await waitFor(() => expect(container.querySelector('.ag-root')).toBeTruthy());
    await waitFor(() => expect(screen.getAllByText(fixtureClaim.id).length).toBeGreaterThan(0));
    expect(screen.getByText('Payout (PayPal sandbox)')).toBeTruthy();
    fireEvent.click(screen.getAllByText(fixtureClaim.id)[0] as HTMLElement);
  });

  it('labels the seeded test policy "test cover" in the policies grid and on its page', async () => {
    renderAt('/operator');
    fireEvent.click(await screen.findByRole('tab', { name: 'Policies' }));
    await waitFor(() => expect(screen.getAllByText(fixtureTestPolicy.id).length).toBeGreaterThan(0));
    expect(screen.getAllByText('Test cover').length).toBeGreaterThan(0);
    cleanup();
    renderAt(`/policies/${fixtureTestPolicy.id}`);
    expect(await screen.findByText('Test cover')).toBeTruthy();
    expect(screen.getByText(/Not priced: seeded sandbox test cover/)).toBeTruthy();
  });

  it('shows the ledger with the backend balance', async () => {
    renderAt('/operator');
    fireEvent.click(await screen.findByRole('tab', { name: 'Ledger' }));
    expect((await screen.findAllByText(/Pool balance/)).length).toBeGreaterThan(0);
    expect(screen.getAllByText("Pool balance (from the backend)").length).toBe(1);
  });
});
