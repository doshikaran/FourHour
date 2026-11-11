/**
 * Scripted replay for the mock: walks the fixture series on a timer and emits the CONTRACTS.md
 * section 5 events, as the backend's replay engine would. Every connect gets a `snapshot` first.
 * `dropConnections()` simulates the stream dropping and reconnecting, for building and testing
 * the live view's recovery.
 *
 * FIXTURE BEHAVIOUR. The rule outcome here is scripted (R1 at 4 hours), not decided by any engine.
 */
import type { ReplayStreamHandle, ReplayStreamHandlers } from '../api/stream';
import type {
  Claim,
  ClaimListItem,
  OutageEvent,
  OutageEventId,
  Payout,
  ReplayEvent,
  SeriesPoint,
  StartReplayResponse,
  SnapshotEvent,
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
  fixtureSeries,
} from './fixtures';

const STEP_SIM_SECONDS = 15 * 60;
const RECONNECT_DELAY_MS = 1000;

interface Subscriber {
  handlers: ReplayStreamHandlers;
  connected: boolean;
}

export class MockReplayEngine {
  private subscribers = new Set<Subscriber>();
  private timer: ReturnType<typeof setTimeout> | null = null;
  private series: SeriesPoint[] = [];
  private step = 0;
  private replayId: string | null = null;
  private scenarioId: string | null = null;
  private replayCount = 0;
  private openEvent: OutageEvent | null = null;
  private hiSteps = 0;
  private emitted = { claim: false, confirmed: false };
  private pendingConfirm: Payout | null = null;
  readonly claims: Claim[] = [];
  readonly payouts: Payout[] = [];

  constructor(private readonly opts: { connectDelayMs: number }) {}

  start(scenarioId: string, speed: number): StartReplayResponse {
    this.stop(false);
    const scenario = fixtureScenarios.find((s) => s.id === scenarioId);
    if (!scenario) throw new Error(`unknown scenario ${scenarioId}`);
    this.replayCount += 1;
    this.replayId = `fixture-replay-${this.replayCount}`;
    this.scenarioId = scenarioId;
    this.series = fixtureSeries();
    this.step = 0;
    this.openEvent = null;
    this.hiSteps = 0;
    this.emitted = { claim: false, confirmed: false };
    this.pendingConfirm = null;
    // Claims in the snapshot are "decided in this replay", so a new replay starts empty.
    this.claims.length = 0;
    this.payouts.length = 0;
    const intervalMs = Math.max(20, (STEP_SIM_SECONDS * 1000) / Math.max(1, speed));
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

  reset(): void {
    this.stop(false);
    this.scenarioId = null;
    this.series = [];
    this.step = 0;
    this.openEvent = null;
    this.claims.length = 0;
    this.payouts.length = 0;
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
      open_events: this.openEvent ? [{ ...this.openEvent }] : [],
      series: this.series.slice(0, this.step).map((p) => ({
        sim_time: p.t,
        share: p.share,
        customers_out: p.customers_out,
        fips: FIXTURE_FIPS,
      })),
      claims: this.claimItems(),
    };
  }

  /** Claims decided in the current replay, in the GET /api/claims item shape. */
  claimItems(): ClaimListItem[] {
    return this.claims.map((c) => ({
      ...c,
      payout: this.payouts.find((p) => p.claim_id === c.id) ?? null,
      member_name: fixtureMember.name,
      fips: FIXTURE_FIPS,
    }));
  }

  private simTime(): string {
    const p = this.series[Math.max(0, this.step - 1)];
    return p ? p.t : '2000-01-01T00:00:00Z';
  }

  private emit(event: ReplayEvent): void {
    for (const sub of this.subscribers) if (sub.connected) sub.handlers.onEvent(event);
  }

  private advance(): void {
    const point = this.series[this.step];
    const prev = this.series[this.step - 1];
    if (!point || !this.replayId) {
      this.stop(true);
      return;
    }
    this.step += 1;
    const t = point.t;
    this.emit({ type: 'tick', data: { sim_time: t, share: point.share, customers_out: point.customers_out, fips: FIXTURE_FIPS } });

    const prevShare = prev ? prev.share : 0;
    const eventId: OutageEventId = fixtureId('evt', 100 + this.replayCount);

    if (prevShare < FIXTURE_THETA_LO && point.share >= FIXTURE_THETA_LO) {
      this.openEvent = {
        id: eventId,
        fips: FIXTURE_FIPS,
        scenario_id: this.scenarioId,
        lo_started_at: t,
        lo_ended_at: null,
        hi_started_at: null,
        hi_ended_at: null,
        peak_share: point.share,
      };
      this.emit({ type: 'event_opened', data: { sim_time: t, outage_event_id: eventId, threshold: 'lo' } });
    }
    if (this.openEvent) this.openEvent.peak_share = Math.max(this.openEvent.peak_share, point.share);
    if (prevShare < FIXTURE_THETA_HI && point.share >= FIXTURE_THETA_HI && this.openEvent) {
      this.openEvent.hi_started_at = t;
      this.emit({ type: 'event_opened', data: { sim_time: t, outage_event_id: eventId, threshold: 'hi' } });
      this.emit({
        type: 'witness_entry',
        data: { sim_time: t, device_id: fixtureDevice.id, policy_id: fixturePolicy.id, kind: 'power_lost', valid: true },
      });
    }
    if (point.share >= FIXTURE_THETA_HI) this.hiSteps += 1;

    if (this.pendingConfirm) {
      const confirmed: Payout = { ...this.pendingConfirm, status: 'success', confirmed_at: t, confirmed_via: 'poll' };
      this.pendingConfirm = null;
      this.payouts.splice(this.payouts.length - 1, 1, confirmed);
      this.emit({ type: 'payout_confirmed', data: { sim_time: t, payout: confirmed } });
    }

    if (this.hiSteps === 16 && !this.emitted.claim) {
      this.emitted.claim = true;
      const claimId = fixtureId('clm', 100 + this.replayCount);
      const claim: Claim = {
        id: claimId,
        policy_id: fixturePolicy.id,
        outage_event_id: eventId,
        tier: 't4',
        rule: 'R1',
        decision: 'paid',
        amount_cents: 9000,
        reason: 'Fixture claim: scripted R1 at 4 hours (made-up example, no rule engine ran).',
        decided_at: t,
        sim_time: t,
      };
      const payout: Payout = {
        id: fixtureId('pay', 100 + this.replayCount),
        claim_id: claimId,
        sender_batch_id: `clm-${fixturePolicy.id}-${eventId}-t4`,
        paypal_batch_id: 'FIXTURE-BATCH',
        paypal_item_id: 'FIXTURE-ITEM',
        amount_cents: 9000,
        status: 'pending',
        created_at: t,
        confirmed_at: null,
        confirmed_via: null,
      };
      this.claims.push(claim);
      this.payouts.push(payout);
      this.pendingConfirm = payout;
      this.emit({ type: 'claim_decided', data: { sim_time: t, claim } });
      this.emit({ type: 'payout_sent', data: { sim_time: t, payout } });
    }

    if (prevShare >= FIXTURE_THETA_HI && point.share < FIXTURE_THETA_HI && this.openEvent) {
      this.openEvent.hi_ended_at = t;
      this.emit({
        type: 'event_closed',
        data: { sim_time: t, outage_event_id: eventId, threshold: 'hi', duration_hours: (this.hiSteps * STEP_SIM_SECONDS) / 3600 },
      });
      this.emit({
        type: 'witness_entry',
        data: { sim_time: t, device_id: fixtureDevice.id, policy_id: fixturePolicy.id, kind: 'power_restored', valid: true },
      });
    }
    if (prevShare >= FIXTURE_THETA_LO && point.share < FIXTURE_THETA_LO && this.openEvent) {
      const started = Date.parse(this.openEvent.lo_started_at);
      this.openEvent = null;
      this.emit({
        type: 'event_closed',
        data: { sim_time: t, outage_event_id: eventId, threshold: 'lo', duration_hours: (Date.parse(t) - started) / 3_600_000 },
      });
    }
  }
}
