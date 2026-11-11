/**
 * Mock implementation of FourHourApi. Serves fixtures for every endpoint in CONTRACTS.md section 4
 * and a scripted replay stream (section 5). Keeps a little in-memory state so flows like
 * create member -> create policy -> view policy work. All values are fixtures, not results.
 */
import { ApiError, type FourHourApi } from '../api/client';
import type {
  ClaimListItem,
  Member,
  Policy,
  PostEntriesResponse,
  WitnessDevice,
  WitnessEntryRow,
  WitnessRejectReason,
} from '../api/types';
import {
  T0,
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
  fixtureSeries,
  fixtureWitnessEntries,
} from './fixtures';
import { MockReplayEngine } from './mockReplay';

export interface MockApiOptions {
  /** Delay before each response resolves, so loading states are visible. 0 in tests. */
  latencyMs?: number;
}

export interface MockApi extends FourHourApi {
  /** Mock-only: simulate the replay stream dropping and reconnecting. */
  dropStream(): void;
}

export function isMockApi(api: FourHourApi): api is MockApi {
  return api.mode === 'mock' && 'dropStream' in api;
}

function notFound(endpoint: string, code: string, message: string): ApiError {
  return new ApiError({ status: 404, code, message, paypalDebugId: null, endpoint });
}

export function createMockApi(opts: MockApiOptions = {}): MockApi {
  const latency = opts.latencyMs ?? 250;
  const replay = new MockReplayEngine({ connectDelayMs: latency });
  const members = new Map<string, Member>([[fixtureMember.id, fixtureMember]]);
  const policies = new Map<string, Policy>([[fixturePolicy.id, fixturePolicy]]);
  const devices = new Map<string, WitnessDevice>([[fixtureDevice.id, { ...fixtureDevice }]]);
  const entries = new Map<string, WitnessEntryRow[]>([[fixtureDevice.id, []]]);
  let counter = 10;

  const reply = <T>(value: T): Promise<T> =>
    new Promise((resolve) => setTimeout(() => resolve(structuredClone(value)), latency));
  const fail = (err: ApiError): Promise<never> =>
    new Promise((_, reject) => setTimeout(() => reject(err), latency));

  function claimItems(): ClaimListItem[] {
    return [fixtureClaim, ...replay.claimItems()];
  }

  const api: MockApi = {
    mode: 'mock',
    dropStream: () => replay.dropConnections(),

    health: () => reply({ status: 'ok', paypal_env: 'sandbox' as const, artifacts_loaded: [] }),
    counties: () => reply({ counties: fixtureCounties }),
    quote: (req) => {
      if (!fixtureCounties.some((c) => c.fips === req.fips)) {
        return fail(notFound('POST /quotes', 'county_not_found', `Fixture mock has no county ${req.fips}.`));
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
    },

    createMember: (req) => {
      const m: Member = { id: fixtureId('mem', ++counter), ...req, created_at: T0 };
      members.set(m.id, m);
      return reply(m);
    },
    createPolicy: (req) => {
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
      };
      policies.set(p.id, p);
      // The mock cannot go to PayPal; it "returns" straight to the vault-return destination.
      return reply({ policy: p, approve_url: `/policies/${p.id}?enrolled=1` });
    },
    policy: (id) => {
      const p = policies.get(id);
      if (!p) return fail(notFound(`GET /policies/${id}`, 'policy_not_found', `No policy ${id}.`));
      const m = members.get(p.member_id) ?? fixtureMember;
      return reply({
        policy: p,
        member: m,
        premium_charges: [{ ...fixtureCharge, policy_id: p.id }],
        devices: [...devices.values()].filter((d) => d.policy_id === p.id),
        claims: claimItems()
          .filter((c) => c.policy_id === p.id)
          .map(({ payout: _payout, member_name: _name, fips: _fips, ...claim }) => claim),
      });
    },
    policies: () => reply({ policies: [...policies.values()] }),
    chargePremium: (id, req) => {
      if (!policies.has(id)) return fail(notFound(`POST /policies/${id}/charge-premium`, 'policy_not_found', `No policy ${id}.`));
      return reply({ ...fixtureCharge, policy_id: id, period: req.period, request_id: `prem-${id}-${req.period}` });
    },
    cancelPolicy: (id) => {
      const p = policies.get(id);
      if (!p) return fail(notFound(`POST /policies/${id}/cancel`, 'policy_not_found', `No policy ${id}.`));
      const cancelled: Policy = { ...p, status: 'cancelled', cancelled_at: T0 };
      policies.set(id, cancelled);
      return reply({ policy: cancelled, refund: null });
    },

    registerDevice: (req) => {
      if (!policies.has(req.policy_id)) {
        return fail(notFound('POST /witness/devices', 'policy_not_found', `No policy ${req.policy_id}.`));
      }
      const d: WitnessDevice = {
        id: fixtureId('dev', ++counter),
        policy_id: req.policy_id,
        public_key_jwk: req.public_key_jwk,
        registered_at: T0,
        last_seq: 0,
        last_hash: '0'.repeat(64),
      };
      devices.set(d.id, d);
      entries.set(d.id, []);
      return reply({ device_id: d.id, genesis_hash: d.last_hash });
    },
    postEntries: (req) => {
      const d = devices.get(req.device_id);
      if (!d) return fail(notFound('POST /witness/entries', 'device_not_found', `No device ${req.device_id}.`));
      // The mock checks order and chain only. It does NOT verify hashes or signatures.
      const res: PostEntriesResponse = { accepted: 0, rejected: [], last_seq: d.last_seq, last_hash: d.last_hash };
      const stored = entries.get(d.id) ?? [];
      for (const e of req.entries) {
        let reason: WitnessRejectReason | null = null;
        if (e.seq !== d.last_seq + 1) reason = 'bad_sequence';
        else if (e.prev_hash !== d.last_hash) reason = 'broken_chain';
        if (reason) {
          if (!(e.seq <= d.last_seq && stored.some((s) => s.seq === e.seq && s.hash === e.hash))) {
            res.rejected.push({ seq: e.seq, reason });
          }
          continue;
        }
        stored.push({ ...e, id: fixtureId('wen', ++counter), device_id: d.id, received_at: T0, sim_time: null, valid: true, reject_reason: null });
        d.last_seq = e.seq;
        d.last_hash = e.hash;
        res.accepted += 1;
      }
      entries.set(d.id, stored);
      res.last_seq = d.last_seq;
      res.last_hash = d.last_hash;
      return reply(res);
    },
    deviceEntries: (id, limit = 200) => {
      if (!devices.has(id)) return fail(notFound(`GET /witness/devices/${id}/entries`, 'device_not_found', `No device ${id}.`));
      const list = id === fixtureDevice.id && (entries.get(id) ?? []).length === 0 ? fixtureWitnessEntries : entries.get(id) ?? [];
      return reply({ entries: list.slice(-limit) });
    },

    scenarios: () => reply({ scenarios: fixtureScenarios }),
    startReplay: (req) => {
      try {
        return reply(replay.start(req.scenario_id, req.speed));
      } catch {
        return fail(notFound('POST /replay/start', 'scenario_not_found', `No scenario ${req.scenario_id}.`));
      }
    },
    stopReplay: () => {
      replay.stop(true);
      return reply({ stopped: true as const });
    },
    resetReplay: () => {
      replay.reset();
      return reply({ reset: true as const });
    },
    openReplayStream: (handlers) => replay.subscribe(handlers),

    claims: () => reply({ claims: claimItems() }),
    claim: (id) => {
      const c = claimItems().find((x) => x.id === id);
      if (!c) return fail(notFound(`GET /claims/${id}`, 'claim_not_found', `No claim ${id}.`));
      return reply({
        claim: c,
        outage_event: fixtureOutageEvent,
        index_series: fixtureSeries(),
        witness_entries: fixtureWitnessEntries,
      });
    },
    ledger: () =>
      reply({
        balance_cents: 111 - 9000,
        entries: [
          { id: fixtureId('led', 1), at: T0, kind: 'premium' as const, amount_cents: 111, policy_id: fixturePolicy.id, ref_id: fixtureCharge.id },
          { id: fixtureId('led', 2), at: '2000-01-01T05:31:00Z', kind: 'payout' as const, amount_cents: -9000, policy_id: fixturePolicy.id, ref_id: fixturePayout.id },
        ],
      }),
    analytics: (name) => reply(fixtureAnalytics[name]),
  };
  return api;
}
