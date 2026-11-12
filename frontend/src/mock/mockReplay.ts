/**
 * Scripted replay for the mock: walks a fixture series on a timer and emits the CONTRACTS.md
 * section 5 events, as the backend's replay engine would. Every connect gets a `snapshot` first.
 * `dropConnections()` simulates the stream dropping and reconnecting.
 *
 * FIXTURE BEHAVIOUR. Claim outcomes are scripted at fixed steps, not decided by any engine.
 * Payout timing follows decision D7 and CONTRACTS section 7: payout_sent shortly after the claim,
 * then payout_confirmed about 22 seconds later in REAL time, whatever the replay speed, because
 * that is how long the sandbox takes (PENDING -> PROCESSING -> SUCCESS).
 */
import type { ReplayStreamHandle, ReplayStreamHandlers } from '../api/stream';
import type {
  Claim,
  ClaimListItem,
  ClaimRule,
  ClaimTier,
  OutageEvent,
  OutageEventId,
  Payout,
  ReplayEvent,
  SeriesPoint,
  SnapshotEvent,
  StartReplayResponse,
  WitnessEntryId,
  WitnessKind,
} from '../api/types';
import {
  FIXTURE_FIPS,
  FIXTURE_THETA_HI,
  FIXTURE_THETA_LO,
  fixtureDevice,
  fixtureId,
  fixtureMember,
  fixturePolicy,
  fixtureScenarios,
  scenarioSeries,
} from './fixtures';

const RECONNECT_DELAY_MS = 1000;
export const DEFAULT_SEND_DELAY_MS = 1500;
export const DEFAULT_CONFIRM_DELAY_MS = 22_000;

interface Subscriber {
  handlers: ReplayStreamHandlers;
  connected: boolean;
}

type ScriptItem =
  | { step: number; kind: 'witness'; witness: WitnessKind; entryNo: number }
  | { step: number; kind: 'claim'; tier: ClaimTier; rule: ClaimRule; decision: Claim['decision']; amount: number; reason: string; r5EntryNo?: number };

/** Scripted outcomes per fixture scenario, by series step. */
const SCRIPTS: Record<string, ScriptItem[]> = {
  'derecho-harris-2024': [
    { step: 9, kind: 'witness', witness: 'power_lost', entryNo: 1 },
    {
      step: 25,
      kind: 'claim',
      tier: 't4',
      rule: 'R1',
      decision: 'paid',
      amount: 9000,
      reason: 'Fixture: scripted R1 at 4 hours; index and fixture witness agree (made-up, no rule engine ran).',
    },
    {
      step: 105,
      kind: 'claim',
      tier: 't24',
      rule: 'R1',
      decision: 'paid',
      amount: 6000,
      reason: 'Fixture: scripted 24-hour top-up, the difference between the 24-hour and 4-hour shares (made-up).',
    },
    { step: 110, kind: 'witness', witness: 'power_restored', entryNo: 2 },
  ],
  'spoof-quiet': [
    { step: 8, kind: 'witness', witness: 'power_lost', entryNo: 3 },
    {
      step: 24,
      kind: 'claim',
      tier: 't4',
      rule: 'R5',
      decision: 'review',
      amount: 0,
      reason: 'Fixture: the witness reported 4 hours without power while the index stayed below the lenient threshold; flagged for review, no payout (made-up).',
      r5EntryNo: 3,
    },
    { step: 30, kind: 'witness', witness: 'power_restored', entryNo: 4 },
  ],
};

export interface ReplayRecord {
  claim: ClaimListItem;
  event: OutageEvent | null;
  series: SeriesPoint[];
}

export class MockReplayEngine {
  private subscribers = new Set<Subscriber>();
  private timer: ReturnType<typeof setTimeout> | null = null;
  private payoutTimers = new Set<ReturnType<typeof setTimeout>>();
  private series: SeriesPoint[] = [];
  private step = 0;
  private replayId: string | null = null;
  private scenarioId: string | null = null;
  private replayCount = 0;
  private event: OutageEvent | null = null;
  private eventOpen = false;
  private eventSeq = 0;
  private priorQualified = false;
  private provisionalOf: OutageEvent | null = null;
  private hiOpen = false;
  private loRunStart: string | null = null;
  private loRunLast: string | null = null;
  private hiRunStart: string | null = null;
  private hiRunLast: string | null = null;
  /** Claims of the current replay (the snapshot lists these). */
  private current: Claim[] = [];
  /** Every claim from every replay, with payouts. Reset deletes no rows (CONTRACTS 1.4). */
  private readonly records = new Map<string, ReplayRecord>();
  private readonly payouts = new Map<string, Payout>();

  constructor(private readonly opts: { connectDelayMs: number; sendDelayMs?: number; confirmDelayMs?: number }) {}

  start(scenarioId: string, speed: number): StartReplayResponse {
    this.stop(false);
    const scenario = fixtureScenarios.find((s) => s.id === scenarioId);
    const series = scenarioSeries(scenarioId);
    if (!scenario || !series) throw new Error(`unknown scenario ${scenarioId}`);
    this.replayCount += 1;
    this.replayId = `fixture-replay-${this.replayCount}`;
    this.scenarioId = scenarioId;
    this.series = series;
    this.step = 0;
    this.event = null;
    this.eventOpen = false;
    this.eventSeq = 0;
    this.priorQualified = false;
    this.provisionalOf = null;
    this.hiOpen = false;
    this.loRunStart = this.loRunLast = this.hiRunStart = this.hiRunLast = null;
    this.current = [];
    const intervalMs = Math.max(20, (15 * 60 * 1000) / Math.max(1, speed));
    const run = () => {
      this.advance();
      if (this.replayId) this.timer = setTimeout(run, intervalMs);
    };
    this.timer = setTimeout(run, intervalMs);
    return { replay_id: this.replayId, sim_start: scenario.start, sim_end: scenario.end };
  }

  stop(emitEnded = true): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    if (this.replayId && emitEnded) {
      this.emit({ type: 'replay_ended', data: { sim_time: this.simTime(), replay_id: this.replayId } });
    }
    this.replayId = null;
  }

  /** Clears in-memory replay state. Claims and payouts already recorded stay (CONTRACTS 1.4). */
  reset(): void {
    this.stop(false);
    this.scenarioId = null;
    this.series = [];
    this.step = 0;
    this.event = null;
    this.current = [];
  }

  /** Cancel pending payout timers (tests). Payouts already sent are not undone. */
  dispose(): void {
    this.stop(false);
    for (const t of this.payoutTimers) clearTimeout(t);
    this.payoutTimers.clear();
  }

  subscribe(handlers: ReplayStreamHandlers): ReplayStreamHandle {
    const sub: Subscriber = { handlers, connected: false };
    this.subscribers.add(sub);
    this.connect(sub, 'connecting');
    return {
      close: () => {
        this.subscribers.delete(sub);
        sub.connected = false;
        handlers.onStatus?.('closed');
      },
    };
  }

  /** Simulate every open stream dropping; each reconnects after a second and gets a fresh snapshot. */
  dropConnections(): void {
    for (const sub of this.subscribers) {
      sub.connected = false;
      sub.handlers.onStatus?.('reconnecting');
      setTimeout(() => {
        if (this.subscribers.has(sub)) this.connect(sub, null);
      }, RECONNECT_DELAY_MS);
    }
  }

  /** Every claim recorded so far, newest last, in the GET /api/claims item shape. */
  allClaimItems(): ClaimListItem[] {
    return [...this.records.values()].map((r) => this.item(r.claim));
  }

  record(id: string): ReplayRecord | null {
    const r = this.records.get(id);
    return r ? { ...r, claim: this.item(r.claim) } : null;
  }

  private item(c: Claim): ClaimListItem {
    const { payout: _p, member_name: _m, fips: _f, ...claim } = c as ClaimListItem;
    return { ...claim, payout: this.payouts.get(c.id) ?? null, member_name: fixtureMember.name, fips: FIXTURE_FIPS };
  }

  private connect(sub: Subscriber, initial: 'connecting' | null): void {
    if (initial) sub.handlers.onStatus?.(initial);
    setTimeout(() => {
      if (!this.subscribers.has(sub)) return;
      sub.connected = true;
      sub.handlers.onStatus?.('open');
      sub.handlers.onEvent({ type: 'snapshot', data: this.snapshot() });
    }, this.opts.connectDelayMs);
  }

  /** Section 5 snapshot. Idle (no replay running): nulls and empty lists. */
  private snapshot(): SnapshotEvent {
    if (!this.replayId) {
      return { replay_id: null, scenario_id: null, sim_time: null, open_events: [], series: [], claims: [] };
    }
    return {
      sim_time: this.simTime(),
      replay_id: this.replayId,
      scenario_id: this.scenarioId,
      open_events: this.event && this.eventOpen ? [{ ...this.event }] : [],
      series: this.series.slice(0, this.step).map((p) => ({
        sim_time: p.t,
        share: p.share,
        customers_out: p.customers_out,
        fips: FIXTURE_FIPS,
      })),
      claims: this.current.map((c) => this.item(c)),
    };
  }

  private simTime(): string {
    const p = this.series[Math.max(0, this.step - 1)];
    return p ? p.t : '2000-01-01T00:00:00Z';
  }

  private emit(event: ReplayEvent): void {
    for (const sub of this.subscribers) if (sub.connected) sub.handlers.onEvent(event);
  }

  private later(ms: number, fn: () => void): void {
    const t = setTimeout(() => {
      this.payoutTimers.delete(t);
      fn();
    }, ms);
    this.payoutTimers.add(t);
  }

  private advance(): void {
    const point = this.series[this.step];
    if (!point || !this.replayId) {
      this.stop(true);
      return;
    }
    const stepIndex = this.step;
    this.step += 1;
    const t = point.t;
    this.emit({ type: 'tick', data: { sim_time: t, share: point.share, customers_out: point.customers_out, fips: FIXTURE_FIPS } });
    if (point.share !== null) this.thresholds(t, point.share);
    for (const item of SCRIPTS[this.scenarioId ?? ''] ?? []) {
      if (item.step === stepIndex) this.runScript(item, t);
    }
  }

  /**
   * Threshold crossings. A null reading (missing interval) never crosses anything.
   * Hours clause (CONTRACTS 1.5): a new run within 72 hours of a qualifying event opens a
   * provisional event; if it reaches 4 hours it merges into the earlier one (event_merged, then
   * event_opened for the earlier ID). The fixture series does this once.
   */
  private thresholds(t: string, share: number): void {
    if (share >= FIXTURE_THETA_LO) {
      if (!this.eventOpen) {
        this.eventSeq += 1;
        const id: OutageEventId = fixtureId('evt', 100 * this.replayCount + this.eventSeq);
        const prior = this.event;
        const mergeable =
          prior && prior.lo_ended_at && this.priorQualified && Date.parse(t) - Date.parse(prior.lo_ended_at) <= 72 * 3_600_000;
        this.provisionalOf = mergeable ? prior : null;
        this.event = {
          id,
          fips: FIXTURE_FIPS,
          scenario_id: this.scenarioId,
          lo_started_at: t,
          lo_ended_at: null,
          hi_started_at: null,
          hi_ended_at: null,
          peak_share: share,
        };
        this.eventOpen = true;
        this.loRunStart = t;
        this.emit({ type: 'event_opened', data: { sim_time: t, outage_event_id: id, threshold: 'lo' } });
      }
      this.loRunLast = t;
      if (this.provisionalOf && this.event && hours(this.loRunStart, t) >= 4) {
        const into = this.provisionalOf;
        this.emit({ type: 'event_merged', data: { sim_time: t, outage_event_id: this.event.id, into_outage_event_id: into.id } });
        into.lo_ended_at = null;
        into.peak_share = Math.max(into.peak_share, this.event.peak_share);
        this.event = into;
        this.provisionalOf = null;
        this.emit({ type: 'event_opened', data: { sim_time: t, outage_event_id: into.id, threshold: 'lo' } });
      }
    }
    const ev = this.event;
    if (ev && this.eventOpen) ev.peak_share = Math.max(ev.peak_share, share);
    if (share >= FIXTURE_THETA_HI && ev && this.eventOpen) {
      if (!this.hiOpen) {
        this.hiOpen = true;
        this.hiRunStart = t;
        ev.hi_started_at = ev.hi_started_at ?? t;
        ev.hi_ended_at = null;
        this.emit({ type: 'event_opened', data: { sim_time: t, outage_event_id: ev.id, threshold: 'hi' } });
      }
      this.hiRunLast = t;
    } else if (this.hiOpen && ev) {
      this.hiOpen = false;
      ev.hi_ended_at = this.hiRunLast;
      this.emit({
        type: 'event_closed',
        data: { sim_time: t, outage_event_id: ev.id, threshold: 'hi', duration_hours: hours(this.hiRunStart, this.hiRunLast) },
      });
    }
    if (share < FIXTURE_THETA_LO && this.eventOpen && ev) {
      this.eventOpen = false;
      ev.lo_ended_at = this.loRunLast;
      const runHours = hours(this.loRunStart, this.loRunLast);
      if (this.provisionalOf) {
        // A short run stays its own event; the earlier qualifying event stays the merge target.
        this.provisionalOf = null;
      } else {
        this.priorQualified = runHours >= 4;
      }
      this.emit({
        type: 'event_closed',
        data: { sim_time: t, outage_event_id: ev.id, threshold: 'lo', duration_hours: runHours },
      });
    }
  }

  private runScript(item: ScriptItem, t: string): void {
    if (item.kind === 'witness') {
      this.emit({
        type: 'witness_entry',
        data: { sim_time: t, device_id: fixtureDevice.id, policy_id: fixturePolicy.id, kind: item.witness, valid: true },
      });
      return;
    }
    const n = 100 * this.replayCount + item.step;
    const isR5 = item.rule === 'R5';
    const eventId = isR5 ? null : (this.event?.id ?? null);
    const claim: Claim = {
      id: fixtureId('clm', n),
      policy_id: fixturePolicy.id,
      outage_event_id: eventId,
      tier: item.tier,
      rule: item.rule,
      decision: item.decision,
      amount_cents: item.amount,
      reason: item.reason,
      decided_at: t,
      sim_time: t,
      witness_entry_id: isR5 && item.r5EntryNo !== undefined ? (fixtureId('wen', 1000 + item.r5EntryNo) as WitnessEntryId) : null,
    };
    this.current.push(claim);
    const window = this.series.slice(0, this.step);
    this.records.set(claim.id, { claim: claim as ClaimListItem, event: this.event && !isR5 ? { ...this.event } : null, series: window });
    this.emit({ type: 'claim_decided', data: { sim_time: t, claim } });
    if (item.decision !== 'paid') return;

    const payout: Payout = {
      id: fixtureId('pay', n),
      claim_id: claim.id,
      sender_batch_id: `clm-${fixturePolicy.id}-${eventId ?? 'none'}-${item.tier}`,
      paypal_batch_id: null,
      paypal_item_id: null,
      amount_cents: item.amount,
      status: 'created',
      created_at: new Date().toISOString().replace(/\.\d{3}Z$/, 'Z'),
      confirmed_at: null,
      confirmed_via: null,
    };
    this.payouts.set(claim.id, payout);
    this.later(this.opts.sendDelayMs ?? DEFAULT_SEND_DELAY_MS, () => {
      const sent: Payout = { ...payout, status: 'pending', paypal_batch_id: `FIXTURE-BATCH-${n}`, paypal_item_id: `FIXTURE-ITEM-${n}` };
      this.payouts.set(claim.id, sent);
      this.emit({ type: 'payout_sent', data: { sim_time: this.simTime(), payout: sent } });
      this.later(this.opts.confirmDelayMs ?? DEFAULT_CONFIRM_DELAY_MS, () => {
        const done: Payout = {
          ...sent,
          status: 'success',
          confirmed_at: new Date().toISOString().replace(/\.\d{3}Z$/, 'Z'),
          confirmed_via: 'poll',
        };
        this.payouts.set(claim.id, done);
        this.emit({ type: 'payout_confirmed', data: { sim_time: this.simTime(), payout: done } });
      });
    });
  }
}

function hours(a: string | null, b: string | null): number {
  return a && b ? (Date.parse(b) - Date.parse(a)) / 3_600_000 : 0;
}
