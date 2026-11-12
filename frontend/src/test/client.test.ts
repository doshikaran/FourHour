import { describe, expect, it } from 'vitest';
import { ApiError, createHttpApi } from '../api/client';

type Call = { url: string; init: RequestInit | undefined };

function fakeFetch(status: number, body: string, calls: Call[]) {
  return (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    return Promise.resolve(new Response(body, { status, statusText: status === 200 ? 'OK' : 'Error' }));
  };
}

describe('createHttpApi', () => {
  it('calls the contract paths under the base URL', async () => {
    const calls: Call[] = [];
    const api = createHttpApi({ baseUrl: 'http://x/api/', fetchImpl: fakeFetch(200, '{"counties":[]}', calls) });
    await api.counties();
    await api.quote({ fips: '99001', cover_cents: 15000 });
    await api.deviceEntries('dev_fixtureaaaab');
    await api.analytics('basis-risk');
    await api.chargePremium('pol_fixtureaaaab', { period: '2026-12' });
    await api.demo();
    expect(calls.map((c) => `${c.init?.method} ${c.url}`)).toEqual([
      'GET http://x/api/counties',
      'POST http://x/api/quotes',
      'GET http://x/api/witness/devices/dev_fixtureaaaab/entries?limit=200',
      'GET http://x/api/analytics/basis-risk',
      'POST http://x/api/policies/pol_fixtureaaaab/charge-premium',
      'GET http://x/api/demo',
    ]);
    expect(calls[1]?.init?.body).toBe('{"fips":"99001","cover_cents":15000}');
  });

  it('turns the contract error body into an ApiError', async () => {
    const body = '{"error":{"code":"policy_not_found","message":"No policy pol_x","paypal_debug_id":"dbg123"}}';
    const api = createHttpApi({ baseUrl: 'http://x/api', fetchImpl: fakeFetch(404, body, []) });
    const err = await api.policy('pol_x').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    const e = err as ApiError;
    expect([e.status, e.code, e.message, e.paypalDebugId, e.endpoint]).toEqual([404, 'policy_not_found', 'No policy pol_x', 'dbg123', 'GET /policies/pol_x']);
  });

  it('reports a network failure and a missing base URL clearly', async () => {
    const down = createHttpApi({ baseUrl: 'http://x/api', fetchImpl: () => Promise.reject(new TypeError('Failed to fetch')) });
    await expect(down.health()).rejects.toMatchObject({ code: 'network_error', status: 0 });
    const unset = createHttpApi({ baseUrl: '' });
    await expect(unset.health()).rejects.toMatchObject({ code: 'not_configured' });
  });

  it('reports a non-JSON error body', async () => {
    const api = createHttpApi({ baseUrl: 'http://x/api', fetchImpl: fakeFetch(502, '<html>bad gateway</html>', []) });
    await expect(api.ledger()).rejects.toMatchObject({ code: 'http_error', status: 502 });
  });
});
