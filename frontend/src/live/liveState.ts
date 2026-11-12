/**
 * The /live stream reducer (F3). Pure: (state, action) -> state, so it is unit-tested.
 *
 * Reconnects: the server sends `snapshot` first on every connect (CONTRACTS section 5). A snapshot
 * for the replay we already show rebuilds series, open events and claims from the server's view,
 * keeping the client-only feed and the times this browser first saw each payout. A snapshot for a
 * different replay starts fresh. An idle snapshot (no replay running) keeps what is on screen and
 * marks the replay ended, so a confirmed payout does not vanish when the stream reconnects.
 */
import type {
  Claim,
  ClaimListItem,
  Fips,
  IsoTime,
  OutageEventId,
  Payout,
  ReplayEvent,
  SnapshotEvent,
  TickEvent,
  WitnessKind,
} from '../api/types';

export interface LiveClaim {
  claim: Claim;
  payout: Payout | null;
  member_name: string | null;
  fips: Fips | null;
  /** Wall-clock ms when this browser first saw the payout sent. Display only. */
  payoutSeenAt: number | null;
  /** Wall-clock ms when this browser first saw the payout reach a final status. Display only. */
  settledSeenAt: number | null;
}

export interface LiveEvent {
  id: OutageEventId;
  startedAt: IsoTime;
  loOpen: boolean;
  hiOpen: boolean;
  /** Times event_opened arrived again for this ID (hours-clause merges). */
  reopenings: number;
}

export interface WitnessMark {
  simTime: IsoTime;
  kind: WitnessKind;
  valid: boolean;
  deviceId: string;
  policyId: string;
}

export type FeedTone = 'info' | 'good' | 'warn' | 'bad' | 'neutral';

export interface FeedItem {
  key: number;
  simTime: IsoTime | null;
  icon: string;
  tone: FeedTone;
  text: string;
}

export interface LiveState {
  replayId: string | null;
  scenarioId: string | null;
  simTime: IsoTime | null;
  /** Known from POST /replay/start when this tab started the replay; otherwise null. */
  simStart: IsoTime | null;
  simEnd: IsoTime | null;
  series: TickEvent[];
  events: LiveEvent[];
  claims: LiveClaim[];
  witness: WitnessMark[];
  heartbeats: number;
  feed: FeedItem[];
  nextKey: number;
  ended: boolean;
  /** Number of snapshots applied (1 on the first connect). */
  snapshots: number;
}

export type LiveAction =
  | { type: 'event'; event: ReplayEvent; at: number }
  | { type: 'started'; replayId: string; scenarioId: string; simStart: IsoTime; simEnd: IsoTime; at: number }
  | { type: 'connection_lost'; at: number }
  /** Member names and counties from GET /api/claims; claim_decided carries only the claim row. */
  | { type: 'enrich'; items: { id: string; member_name: string; fips: Fips }[] };

export const FEED_LIMIT = 150;

export function initialLiveState(): LiveState {
  return {
    replayId: null,
    scenarioId: null,
    simTime: null,
    simStart: null,
    simEnd: null,
    series: [],
    events: [],
    claims: [],
    witness: [],
    heartbeats: 0,
    feed: [],
    nextKey: 1,
    ended: false,
    snapshots: 0,
  };
}

function pushFeed(s: LiveState, item: Omit<FeedItem, 'key'>): LiveState {
  const feed = [...s.feed, { ...item, key: s.nextKey }];
  return { ...s, feed: feed.length > FEED_LIMIT ? feed.slice(feed.length - FEED_LIMIT) : feed, nextKey: s.nextKey + 1 };
}

const isFinal = (p: Payout | null) => p !== null && (p.status === 'success' || p.status === 'failed' || p.status === 'unclaimed');

function upsertClaim(s: LiveState, claim: Claim, extra: Partial<Omit<LiveClaim, 'claim'>>): LiveState {
  const i = s.claims.findIndex((c) => c.claim.id === claim.id);
  if (i === -1) {
    const lc: LiveClaim = {
      claim,
      payout: null,
      member_name: null,
      fips: null,
      payoutSeenAt: null,
      settledSeenAt: null,
      ...extra,
    };
    return { ...s, claims: [...s.claims, lc] };
  }
  const claims = [...s.claims];
  claims[i] = { ...(claims[i] as LiveClaim), claim, ...extra };
  return { ...s, claims };
}

function applyPayout(s: LiveState, payout: Payout, at: number): { state: LiveState; known: boolean } {
  const i = s.claims.findIndex((c) => c.claim.id === payout.claim_id);
  if (i === -1) return { state: s, known: false };
  const prev = s.claims[i] as LiveClaim;
  const claims = [...s.claims];
  claims[i] = {
    ...prev,
    payout,
    payoutSeenAt: prev.payoutSeenAt ?? at,
    settledSeenAt: prev.settledSeenAt ?? (isFinal(payout) ? at : null),
  };
  return { state: { ...s, claims }, known: true };
}

function fromSnapshot(prev: LiveState, snap: SnapshotEvent, at: number): LiveState {
  const sameReplay = prev.replayId !== null && prev.replayId === snap.replay_id;
  const base: LiveState = sameReplay
    ? prev
    : { ...initialLiveState(), feed: prev.feed, nextKey: prev.nextKey };
  const claims: LiveClaim[] = snap.claims.map((item: ClaimListItem) => {
    const { payout, member_name, fips, ...claim } = item;
    const old = sameReplay ? prev.claims.find((c) => c.claim.id === claim.id) : undefined;
    return {
      claim,
      payout,
      member_name,
      fips,
      payoutSeenAt: payout ? (old?.payoutSeenAt ?? at) : null,
      settledSeenAt: isFinal(payout) ? (old?.settledSeenAt ?? at) : null,
    };
  });
  // Claims this browser saw that the snapshot no longer lists stay visible (a reset keeps rows).
  const kept = sameReplay ? prev.claims.filter((c) => !claims.some((n) => n.claim.id === c.claim.id)) : [];
  const events: LiveEvent[] = snap.open_events.map((e) => {
    const old = sameReplay ? prev.events.find((x) => x.id === e.id) : undefined;
    return {
      id: e.id,
      startedAt: e.lo_started_at,
      loOpen: e.lo_ended_at === null,
      hiOpen: e.hi_started_at !== null && e.hi_ended_at === null,
      reopenings: old?.reopenings ?? 0,
    };
  });
  return {
    ...base,
    replayId: snap.replay_id,
    scenarioId: snap.scenario_id,
    simTime: snap.sim_time,
    simStart: sameReplay ? prev.simStart : null,
    simEnd: sameReplay ? prev.simEnd : null,
    series: [...snap.series].sort((a, b) => Date.parse(a.sim_time) - Date.parse(b.sim_time)),
    events,
    claims: [...kept, ...claims],
    witness: sameReplay ? prev.witness : [],
    heartbeats: sameReplay ? prev.heartbeats : 0,
    ended: false,
    snapshots: prev.snapshots + 1,
  };
}

const KIND_TEXT: Record<WitnessKind, string> = {
  heartbeat: 'heartbeat',
  power_lost: 'charger lost power',
  power_restored: 'charger power is back',
  attest_lost: 'member reported power out (self-reported)',
  attest_restored: 'member reported power back (self-reported)',
};

export function liveReducer(state: LiveState, action: LiveAction): LiveState {
  if (action.type === 'started') {
    return pushFeed(
      {
        ...initialLiveState(),
        feed: state.feed,
        nextKey: state.nextKey,
        snapshots: state.snapshots,
        replayId: action.replayId,
        scenarioId: action.scenarioId,
        simStart: action.simStart,
        simEnd: action.simEnd,
      },
      { simTime: action.simStart, icon: '▶', tone: 'info', text: `Replay ${action.scenarioId} started.` },
    );
  }
  if (action.type === 'enrich') {
    let changed = false;
    const claims = state.claims.map((c) => {
      const m = action.items.find((i) => i.id === c.claim.id);
      if (!m || (c.member_name === m.member_name && c.fips === m.fips)) return c;
      changed = true;
      return { ...c, member_name: m.member_name, fips: m.fips };
    });
    return changed ? { ...state, claims } : state;
  }
  if (action.type === 'connection_lost') {
    return pushFeed(state, { simTime: state.simTime, icon: '⟳', tone: 'warn', text: 'Stream dropped. Reconnecting…' });
  }

  const ev = action.event;
  const at = action.at;
  switch (ev.type) {
    case 'snapshot': {
      const snap = ev.data;
      if (snap.replay_id === null) {
        const s = { ...state, ended: state.replayId !== null ? true : state.ended, snapshots: state.snapshots + 1 };
        return state.snapshots === 0
          ? s
          : pushFeed(s, { simTime: state.simTime, icon: '⟳', tone: 'neutral', text: 'Reconnected. No replay is running.' });
      }
      const next = fromSnapshot(state, snap, at);
      if (state.snapshots === 0) return next;
      return pushFeed(next, {
        simTime: snap.sim_time,
        icon: '⟳',
        tone: 'info',
        text: 'Reconnected. State rebuilt from the server snapshot.',
      });
    }
    case 'tick': {
      const t = ev.data;
      const last = state.series[state.series.length - 1];
      if (last && Date.parse(t.sim_time) <= Date.parse(last.sim_time)) {
        if (t.sim_time === last.sim_time) return state; // duplicate after a reconnect
        // Time went back: another tab started a new replay we have no ID for. Start over.
        return { ...initialLiveState(), feed: state.feed, nextKey: state.nextKey, snapshots: state.snapshots, series: [t], simTime: t.sim_time };
      }
      return { ...state, series: [...state.series, t], simTime: t.sim_time, ended: false };
    }
    case 'event_opened': {
      const d = ev.data;
      const existing = state.events.find((e) => e.id === d.outage_event_id);
      let events: LiveEvent[];
      let text: string;
      if (!existing) {
        events = [
          ...state.events,
          { id: d.outage_event_id, startedAt: d.sim_time, loOpen: d.threshold === 'lo', hiOpen: d.threshold === 'hi', reopenings: 0 },
        ];
        text = d.threshold === 'lo' ? 'Index crossed the lenient threshold. Outage event opened.' : 'Index crossed the strong threshold.';
      } else {
        const wasOpen = d.threshold === 'lo' ? existing.loOpen : existing.hiOpen;
        const reopened = d.threshold === 'lo' && !existing.loOpen;
        events = state.events.map((e) =>
          e.id === d.outage_event_id
            ? { ...e, loOpen: e.loOpen || d.threshold === 'lo', hiOpen: e.hiOpen || d.threshold === 'hi', reopenings: e.reopenings + (reopened ? 1 : 0) }
            : e,
        );
        text = reopened
          ? 'Index crossed the lenient threshold again within 72 hours. The same outage event is reopened (hours clause).'
          : d.threshold === 'hi'
            ? 'Index crossed the strong threshold.'
            : wasOpen
              ? 'Outage event still open.'
              : 'Outage event reopened.';
      }
      return pushFeed({ ...state, events, simTime: d.sim_time }, { simTime: d.sim_time, icon: '▲', tone: 'warn', text });
    }
    case 'event_closed': {
      const d = ev.data;
      const events = state.events.map((e) =>
        e.id === d.outage_event_id ? { ...e, loOpen: d.threshold === 'lo' ? false : e.loOpen, hiOpen: d.threshold === 'hi' ? false : e.hiOpen } : e,
      );
      const name = d.threshold === 'lo' ? 'lenient' : 'strong';
      return pushFeed(
        { ...state, events, simTime: d.sim_time },
        {
          simTime: d.sim_time,
          icon: '▼',
          tone: 'neutral',
          text: `Index fell below the ${name} threshold after ${formatDuration(d.duration_hours)}.`,
        },
      );
    }
    case 'event_merged': {
      // The provisional event is deleted on the server; drop it. event_opened for the target follows.
      const d = ev.data;
      const events = state.events.filter((e) => e.id !== d.outage_event_id);
      return pushFeed(
        { ...state, events, simTime: d.sim_time },
        {
          simTime: d.sim_time,
          icon: '⇄',
          tone: 'warn',
          text: 'A new run reached 4 hours within 72 hours of the earlier event, so it merges into that event (hours clause).',
        },
      );
    }
    case 'witness_entry': {
      const d = ev.data;
      const mark: WitnessMark = { simTime: d.sim_time, kind: d.kind, valid: d.valid, deviceId: d.device_id, policyId: d.policy_id };
      const s = { ...state, witness: [...state.witness, mark], simTime: d.sim_time };
      if (d.kind === 'heartbeat' && d.valid) return { ...s, heartbeats: s.heartbeats + 1 };
      return pushFeed(s, {
        simTime: d.sim_time,
        icon: d.valid ? '◆' : '✕',
        tone: d.valid ? (d.kind === 'power_lost' || d.kind === 'attest_lost' ? 'warn' : 'info') : 'bad',
        text: d.valid ? `Witness: ${KIND_TEXT[d.kind]}.` : `Witness entry rejected (${KIND_TEXT[d.kind]}).`,
      });
    }
    case 'claim_decided': {
      const c = ev.data.claim;
      const s = upsertClaim(state, c, {});
      const verb = c.decision === 'paid' ? 'approved' : c.decision === 'declined' ? 'declined' : 'flagged for review';
      return pushFeed(
        { ...s, simTime: ev.data.sim_time },
        {
          simTime: ev.data.sim_time,
          icon: c.decision === 'paid' ? '✓' : c.decision === 'declined' ? '✕' : '!',
          tone: c.decision === 'paid' ? 'good' : c.decision === 'declined' ? 'neutral' : 'warn',
          text: `Claim ${verb} under ${c.rule} (${c.tier === 't4' ? '4-hour tier' : '24-hour top-up'}). ${c.reason}`,
        },
      );
    }
    case 'payout_sent': {
      const { state: s, known } = applyPayout(state, ev.data.payout, at);
      return pushFeed(
        { ...s, simTime: ev.data.sim_time },
        { simTime: ev.data.sim_time, icon: '…', tone: 'info', text: known ? 'Payout sent to PayPal. Waiting for confirmation.' : 'Payout sent for a claim not on this screen.' },
      );
    }
    case 'payout_confirmed': {
      const p = ev.data.payout;
      const { state: s } = applyPayout(state, p, at);
      const text =
        p.status === 'success'
          ? `PayPal confirmed the payout${p.confirmed_via ? ` (${p.confirmed_via})` : ''}.`
          : p.status === 'failed'
            ? 'PayPal reports the payout failed.'
            : p.status === 'unclaimed'
              ? 'PayPal reports the payout unclaimed.'
              : 'Payout still pending at PayPal.';
      return pushFeed(
        { ...s, simTime: ev.data.sim_time },
        { simTime: ev.data.sim_time, icon: p.status === 'success' ? '✓' : '!', tone: p.status === 'success' ? 'good' : 'bad', text },
      );
    }
    case 'replay_ended':
      return pushFeed({ ...state, ended: true }, { simTime: ev.data.sim_time, icon: '■', tone: 'neutral', text: 'Replay ended.' });
  }
}

function formatDuration(hours: number): string {
  const m = Math.round(hours * 60);
  return m < 60 ? `${m} min` : `${Math.floor(m / 60)} h${m % 60 ? ` ${m % 60} min` : ''}`;
}

/** How long a just-confirmed payout stays in focus before a later one takes over. */
export const CONFIRMED_HOLD_MS = 8000;

/**
 * The claim the payout panel focuses on, in order: a payout that settled in the last
 * CONFIRMED_HOLD_MS (so the confirmation is always seen, even when a top-up is already sending);
 * else the oldest payout still sending; else the latest claim with a payout; else the latest claim.
 */
export function focusClaim(s: LiveState, now: number = Date.now()): LiveClaim | null {
  let recent: LiveClaim | null = null;
  for (const c of s.claims) {
    if (c.settledSeenAt !== null && now - c.settledSeenAt < CONFIRMED_HOLD_MS && (!recent || (recent.settledSeenAt ?? 0) <= c.settledSeenAt)) recent = c;
  }
  if (recent) return recent;
  const sending = s.claims.find((c) => c.payout && !isFinal(c.payout));
  if (sending) return sending;
  for (let i = s.claims.length - 1; i >= 0; i--) {
    const c = s.claims[i] as LiveClaim;
    if (c.payout) return c;
  }
  return s.claims[s.claims.length - 1] ?? null;
}
