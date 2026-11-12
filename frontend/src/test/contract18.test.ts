/**
 * CONTRACTS 1.8: the demo-guide peak wording, the unknown-device detection used by the witness
 * page, the signed-points formatter on the evidence page, and the 1.8 mock shapes.
 */
import { describe, expect, it } from 'vitest';
import { ApiError } from '../api/client';
import { peakSentence } from '../live/DemoGuide';
import { createMockApi } from '../mock/mockApi';
import { FIXTURE_NOT_OFFERED_FIPS, fixtureAnalytics } from '../mock/fixtures';
import { signedPoints } from '../pages/EvidencePanels';
import { isUnknownDeviceError, isUnknownDeviceHalt, UNKNOWN_DEVICE_HALT } from '../witness/log';

const err = (status: number, code: string) => new ApiError({ status, code, message: 'm', paypalDebugId: null, endpoint: 'POST /witness/entries' });

describe('peakSentence', () => {
  it('words the peak against theta_hi from the numbers only', () => {
    expect(peakSentence(0.274706, 0.9, true)).toBe('This replay peaked: 27.5% of county customers, below the 90.0% index-only line.');
    expect(peakSentence(0.95, 0.9, false)).toMatch(/^Highest so far in this replay: 95\.0% .* at or above the 90\.0%/);
    expect(peakSentence(null, 0.9, false)).toBeNull();
    expect(peakSentence(0.3, null, true)).toBeNull();
  });
});

describe('unknown witness device', () => {
  it('treats a 404 or device_not_found as unknown, and nothing else', () => {
    expect(isUnknownDeviceError(err(404, 'device_not_found'))).toBe(true);
    expect(isUnknownDeviceError(err(404, 'not_found'))).toBe(true);
    expect(isUnknownDeviceError(err(0, 'network_error'))).toBe(false);
    expect(isUnknownDeviceError(err(503, 'database_unavailable'))).toBe(false);
    expect(isUnknownDeviceError(new Error('x'))).toBe(false);
    expect(isUnknownDeviceHalt(`${UNKNOWN_DEVICE_HALT} (dev_x).`)).toBe(true);
    expect(isUnknownDeviceHalt('The server rejected entry 3 (bad_signature).')).toBe(false);
    expect(isUnknownDeviceHalt(null)).toBe(false);
  });
});

describe('signedPoints', () => {
  it('shows the gap in percentage points with a sign, unrounded beyond 2 decimals', () => {
    expect(signedPoints(0.684211 - 0.814744)).toBe('-13.05');
    expect(signedPoints(0.823529 - 0.814744)).toBe('+0.88');
    expect(signedPoints(0)).toBe('0');
  });
});

describe('1.8 mock shapes', () => {
  it('pays and prices the 4-hour tier only, and marks a county not offered', async () => {
    expect(Object.keys(fixtureAnalytics.pricing.payout_schedule)).toEqual(['t4']);
    expect(fixtureAnalytics.pricing.counties[FIXTURE_NOT_OFFERED_FIPS]?.offered).toBe(false);
    expect(fixtureAnalytics.solvency.capital_assumption.description).toMatch(/FIXTURE/);
    const api = createMockApi({ latencyMs: 0 });
    try {
      const q = await api.quote({ fips: '99001', cover_cents: 5000 });
      expect(q.breakdown.expected_payout_share).toBe(0.6);
      expect('prob_24h_given_event' in q.breakdown).toBe(false);
      await expect(api.quote({ fips: FIXTURE_NOT_OFFERED_FIPS, cover_cents: 5000 })).rejects.toMatchObject({ status: 422, code: 'county_not_offered' });
      await expect(
        api.createPolicy({ member_id: 'mem_fixtureaaaab', fips: FIXTURE_NOT_OFFERED_FIPS, cover_cents: 5000 }),
      ).rejects.toMatchObject({ status: 422, code: 'county_not_offered' });
      const d = await api.demo();
      expect(d.policy_id).toMatch(/^pol_fixture/);
    } finally {
      api.dispose();
    }
  });
});
