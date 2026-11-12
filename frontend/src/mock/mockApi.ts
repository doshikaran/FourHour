/**
 * Mock implementation of FourHourApi. Serves fixtures for every endpoint in CONTRACTS.md section 4
 * and a scripted replay stream (section 5). Keeps in-memory state so flows like
 * create member -> create policy -> view policy -> register witness -> upload work.
 * All values are fixtures, not results.
 *
 * Witness uploads are checked for real here (sequence, chain, hash, ECDSA signature), so the mock
 * catches a wrong entry the same way the backend would. A hash that does not match is reported as
 * bad_signature, because the contract has no separate reason for it.
 */
import { ApiError, type FourHourApi } from '../api/client';
import type {
  ClaimListItem,
  CountiesResponse,
  Member,
  Policy,
  PostEntriesResponse,
  WitnessDevice,
  WitnessEntryRow,
  WitnessRejectReason,
} from '../api/types';
import { checkEntry, importPublicJwk } from '../witness/entry';
import {
  FIXTURE_UNPRICED_FIPS,
  T0,
  derechoFixtureSeries,
  fixtureAnalytics,
  fixtureCharge,
  fixtureClaim,
  fixtureCounties,
  fixtureDevice,
  fixtureId,
  fixtureMember,
  fixtureOutageEvent,
  fixturePayout,
  fixturePolicy,
  fixtureProvenance,
  fixtureScenarios,
  fixtureTestPolicy,
  fixtureWitnessEntries,
} from './fixtures';
import { MockReplayEngine } from './mockReplay';

export interface MockApiOptions {
  /** Delay before each response resolves, so loading states are visible. 0 in tests. */
  latencyMs?: number;
  /** Real-time delay from claim to payout_sent, and from payout_sent to payout_confirmed. */
  payoutSendDelayMs?: number;
  payoutConfirmDelayMs?: number;
}

export interface MockApi extends FourHourApi {
  /** Mock-only: simulate the replay stream dropping and reconnecting. */
  dropStream(): void;
  /** Mock-only: make every request fail as if the network were down (witness offline tests). */
  setOffline(offline: boolean): void;
  /** Mock-only: stop timers (tests). */
  dispose(): void;
}

export function isMockApi(api: FourHourApi): api is MockApi {
  return api.mode === 'mock' && 'dropStream' in api;
}

function notFound(endpoint: string, code: string, message: string): ApiError {
  return new ApiError({ status: 404, code, message, paypalDebugId: null, endpoint });
}

const nowIso = () => new Date().toISOString().replace(/\.\d{3}Z$/, 'Z');

export function createMockApi(opts: MockApiOptions = {}): MockApi {
  const latency = opts.latencyMs ?? 250;
  const replay = new MockReplayEngine({
    connectDelayMs: latency,
    ...(opts.payoutSendDelayMs !== undefined ? { sendDelayMs: opts.payoutSendDelayMs } : {}),
    ...(opts.payoutConfirmDelayMs !== undefined ? { confirmDelayMs: opts.payoutConfirmDelayMs } : {}),
  });
  const members = new Map<string, Member>([[fixtureMember.id, fixtureMember]]);
  const policies = new Map<string, Policy>([
    [fixturePolicy.id, fixturePolicy],
    [fixtureTestPolicy.id, fixtureTestPolicy],
  ]);
  const devices = new Map<string, WitnessDevice>([[fixtureDevice.id, { ...fixtureDevice }]]);
  const entries = new Map<string, WitnessEntryRow[]>([[fixtureDevice.id, []]]);
  const keys = new Map<string, Promise<CryptoKey>>();
  let counter = 10;
  let offline = false;

  const reply = <T>(value: T): Promise<T> =>
    new Promise((resolve) => setTimeout(() => resolve(structuredClone(value)), latency));
  const fail = (err: ApiError): Promise<never> => new Promise((_, reject) => setTimeout(() => reject(err), latency));
  const guard = <T>(endpoint: string, fn: () => Promise<T>): Promise<T> =>
    offline
      ? fail(
          new ApiError({
            status: 0,
            code: 'network_error',
            message: 'Could not reach the backend (mock is set to offline).',
            paypalDebugId: null,
            endpoint,
          }),
        )
      : fn();

  function claimItems(): ClaimListItem[] {
    return [fixtureClaim, ...replay.allClaimItems()];
  }

  async function verifyAndStore(deviceId: string, list: PostEntriesResponse['rejected'], req: Parameters<FourHourApi['postEntries']>[0]) {
    const d = devices.get(deviceId) as WitnessDevice;
    const stored = entries.get(d.id) ?? [];
    const keyPromise = keys.get(d.id);
    let accepted = 0;
    for (const e of req.entries) {
      // A resend of an already accepted seq with the same hash is accepted silently.
      if (e.seq <= d.last_seq && stored.some((s) => s.seq === e.seq && s.hash === e.hash && s.valid)) continue;
      let reason: WitnessRejectReason | null = null;
      const prevTime = stored.filter((s) => s.valid).at(-1)?.device_time;
      if (keyPromise) {
        const check = await checkEntry(e, { seq: d.last_seq, hash: d.last_hash }, await keyPromise);
        if (!check.ok) reason = check.problem === 'hash_mismatch' ? 'bad_signature' : check.problem;
      } else {
        // The pre-seeded fixture device has no real key; only order and chain are checked.
        if (e.seq !== d.last_seq + 1) reason = 'bad_sequence';
        else if (e.prev_hash !== d.last_hash) reason = 'broken_chain';
      }
      if (!reason && prevTime && Date.parse(e.device_time) < Date.parse(prevTime)) reason = 'time_went_backwards';
      const row: WitnessEntryRow = {
        ...e,
        id: fixtureId('wen', ++counter),
        device_id: d.id,
        received_at: nowIso(),
        sim_time: null,
        valid: reason === null,
        reject_reason: reason,
      };
      stored.push(row);
      if (reason) {
        list.push({ seq: e.seq, reason });
        continue;
      }
      d.last_seq = e.seq;
      d.last_hash = e.hash;
      accepted += 1;
    }
    entries.set(d.id, stored);
    return accepted;
  }

  const api: MockApi = {
    mode: 'mock',
    dropStream: () => replay.dropConnections(),
    setOffline: (v) => {
      offline = v;
    },
    dispose: () => replay.dispose(),

    health: () => guard('GET /health', () => reply({ status: 'ok' as const, paypal_env: 'sandbox' as const, artifacts_loaded: [] })),
    counties: () => guard('GET /counties', () => reply<CountiesResponse>({ counties: fixtureCounties })),
    quote: (req) =>
      guard('POST /quotes', () => {
        if (!fixtureCounties.some((c) => c.fips === req.fips)) {
          return fail(notFound('POST /quotes', 'county_not_found', `Fixture mock has no county ${req.fips}.`));
        }
        if (req.fips === FIXTURE_UNPRICED_FIPS) {
          return fail(
            new ApiError({
              status: 422,
              code: 'pricing_not_computed',
              message: 'Fixture: the pricing table has null inputs for this county.',
              paypalDebugId: null,
              endpoint: 'POST /quotes',
            }),
          );
        }
        return reply({
          fips: req.fips,
          cover_cents: req.cover_cents,
          premium_cents: 111,
          model_version: 'fixture-0',
          breakdown: {
            expected_events_per_year: 0.5,
            prob_24h_given_event: 0.5,
            prob_premises_affected: 0.5,
            expected_annual_payout_cents: 100,
            credibility_weight: 0.5,
            risk_margin_rate: 0.5,
            expense_load_cents: 100,
          },
          provenance: fixtureProvenance,
        });
      }),

    createMember: (req) =>
      guard('POST /members', () => {
        const m: Member = { id: fixtureId('mem', ++counter), ...req, created_at: nowIso() };
        members.set(m.id, m);
        return reply(m);
      }),
    createPolicy: (req) =>
      guard('POST /policies', () => {
        if (!members.has(req.member_id)) {
          return fail(notFound('POST /policies', 'member_not_found', `No member ${req.member_id}.`));
        }
        const p: Policy = {
          ...fixturePolicy,
          id: fixtureId('pol', ++counter),
          member_id: req.member_id,
          fips: req.fips,
          cover_cents: req.cover_cents,
          status: 'active',
          created_at: nowIso(),
        };
        policies.set(p.id, p);
        // The mock cannot go to PayPal; it "returns" straight to the vault-return destination.
        return reply({ policy: p, approve_url: `/policies/${p.id}?enrolled=1` });
      }),
    policy: (id) =>
      guard(`GET /policies/${id}`, () => {
        const p = policies.get(id);
        if (!p) return fail(notFound(`GET /policies/${id}`, 'policy_not_found', `No policy ${id}.`));
        const m = members.get(p.member_id) ?? fixtureMember;
        return reply({
          policy: p,
          member: m,
          premium_charges: [{ ...fixtureCharge, id: fixtureId('chg', p.id.length + 1), policy_id: p.id, amount_cents: p.premium_cents, request_id: `prem-${p.id}-2000-01` }],
          devices: [...devices.values()].filter((d) => d.policy_id === p.id),
          claims: claimItems()
            .filter((c) => c.policy_id === p.id)
            .map(({ payout: _payout, member_name: _name, fips: _fips, ...claim }) => claim),
        });
      }),
    policies: () => guard('GET /policies', () => reply({ policies: [...policies.values()] })),
    chargePremium: (id, req) =>
      guard(`POST /policies/${id}/charge-premium`, () => {
        if (!policies.has(id)) return fail(notFound(`POST /policies/${id}/charge-premium`, 'policy_not_found', `No policy ${id}.`));
        return reply({ ...fixtureCharge, policy_id: id, period: req.period, request_id: `prem-${id}-${req.period}` });
      }),
    cancelPolicy: (id) =>
      guard(`POST /policies/${id}/cancel`, () => {
        const p = policies.get(id);
        if (!p) return fail(notFound(`POST /policies/${id}/cancel`, 'policy_not_found', `No policy ${id}.`));
        const cancelled: Policy = { ...p, status: 'cancelled', cancelled_at: nowIso() };
        policies.set(id, cancelled);
        return reply({ policy: cancelled, refund: null });
      }),

    registerDevice: (req) =>
      guard('POST /witness/devices', () => {
        if (!policies.has(req.policy_id)) {
          return fail(notFound('POST /witness/devices', 'policy_not_found', `No policy ${req.policy_id}.`));
        }
        const d: WitnessDevice = {
          id: fixtureId('dev', ++counter),
          policy_id: req.policy_id,
          public_key_jwk: req.public_key_jwk,
          registered_at: nowIso(),
          last_seq: 0,
          last_hash: '0'.repeat(64),
        };
        devices.set(d.id, d);
        entries.set(d.id, []);
        const k = importPublicJwk(req.public_key_jwk);
        k.catch(() => undefined);
        keys.set(d.id, k);
        return reply({ device_id: d.id, genesis_hash: d.last_hash });
      }),
    postEntries: (req) =>
      guard('POST /witness/entries', async () => {
        const d = devices.get(req.device_id);
        if (!d) return fail(notFound('POST /witness/entries', 'device_not_found', `No device ${req.device_id}.`));
        const rejected: PostEntriesResponse['rejected'] = [];
        const accepted = await verifyAndStore(d.id, rejected, req);
        return reply<PostEntriesResponse>({ accepted, rejected, last_seq: d.last_seq, last_hash: d.last_hash });
      }),
    deviceEntries: (id, limit = 200) =>
      guard(`GET /witness/devices/${id}/entries`, () => {
        if (!devices.has(id)) return fail(notFound(`GET /witness/devices/${id}/entries`, 'device_not_found', `No device ${id}.`));
        const list = id === fixtureDevice.id && (entries.get(id) ?? []).length === 0 ? fixtureWitnessEntries : entries.get(id) ?? [];
        return reply({ entries: list.slice(-limit) });
      }),

    scenarios: () => guard('GET /replay/scenarios', () => reply({ scenarios: fixtureScenarios })),
    startReplay: (req) =>
      guard('POST /replay/start', () => {
        try {
          return reply(replay.start(req.scenario_id, req.speed));
        } catch {
          return fail(notFound('POST /replay/start', 'scenario_not_found', `No scenario ${req.scenario_id}.`));
        }
      }),
    stopReplay: () =>
      guard('POST /replay/stop', () => {
        replay.stop(true);
        return reply({ stopped: true as const });
      }),
    resetReplay: () =>
      guard('POST /replay/reset', () => {
        replay.reset();
        return reply({ reset: true as const });
      }),
    openReplayStream: (handlers) => replay.subscribe(handlers),

    claims: () => guard('GET /claims', () => reply({ claims: claimItems() })),
    claim: (id) =>
      guard(`GET /claims/${id}`, () => {
        if (id === fixtureClaim.id) {
          return reply({
            claim: { ...fixtureClaim, payout: fixturePayout },
            outage_event: fixtureOutageEvent,
            index_series: derechoFixtureSeries().slice(0, 36),
            witness_entries: fixtureWitnessEntries,
          });
        }
        const r = replay.record(id);
        if (!r) return fail(notFound(`GET /claims/${id}`, 'claim_not_found', `No claim ${id}.`));
        return reply({
          claim: r.claim,
          outage_event: r.event,
          index_series: r.series,
          witness_entries: r.claim.rule === 'R5' ? fixtureWitnessEntries.slice(0, 1) : fixtureWitnessEntries,
        });
      }),
    ledger: () =>
      guard('GET /ledger', () => {
        const paid = replay.allClaimItems().filter((c) => c.payout?.status === 'success');
        const ledgerEntries = [
          { id: fixtureId('led', 1), at: T0, kind: 'premium' as const, amount_cents: 111, policy_id: fixturePolicy.id, ref_id: fixtureCharge.id },
          { id: fixtureId('led', 2), at: '2000-01-01T06:16:00Z', kind: 'payout' as const, amount_cents: -9000, policy_id: fixturePolicy.id, ref_id: fixturePayout.id },
          ...paid.map((c, i) => ({
            id: fixtureId('led', 10 + i),
            at: c.payout?.confirmed_at ?? T0,
            kind: 'payout' as const,
            amount_cents: -(c.payout?.amount_cents ?? 0),
            policy_id: c.policy_id,
            ref_id: c.payout?.id ?? fixturePayout.id,
          })),
        ];
        return reply({ balance_cents: ledgerEntries.reduce((s, e) => s + e.amount_cents, 0), entries: ledgerEntries });
      }),
    analytics: (name) => guard(`GET /analytics/${name}`, () => reply(fixtureAnalytics[name])),
  };
  return api;
}
