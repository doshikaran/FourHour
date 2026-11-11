/**
 * Replay event stream (CONTRACTS.md section 5) over EventSource.
 *
 * EventSource reconnects by itself after a drop. The server sends a `snapshot` event first on every
 * connect, so the consumer rebuilds its state from the snapshot rather than from what it had.
 * Messages that fail the light shape check are reported through `onMalformed` and never reach `onEvent`.
 */
import { REPLAY_EVENT_NAMES, type ReplayEvent, type ReplayEventName } from './types';

export type StreamStatus = 'connecting' | 'open' | 'reconnecting' | 'closed';

export interface ReplayStreamHandlers {
  onEvent: (event: ReplayEvent) => void;
  onStatus?: (status: StreamStatus) => void;
  onMalformed?: (eventName: string, raw: string, reason: string) => void;
}

export interface ReplayStreamHandle {
  close(): void;
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

const str = (o: Record<string, unknown>, k: string) => typeof o[k] === 'string';
const num = (o: Record<string, unknown>, k: string) => typeof o[k] === 'number' && Number.isFinite(o[k]);
const bool = (o: Record<string, unknown>, k: string) => typeof o[k] === 'boolean';
const rec = (o: Record<string, unknown>, k: string) => isRecord(o[k]);

/** Required keys per event, checked at the top level only. */
const CHECKS: Record<ReplayEventName, (o: Record<string, unknown>) => boolean> = {
  snapshot: (o) =>
    'replay_id' in o &&
    'scenario_id' in o &&
    Array.isArray(o.open_events) &&
    Array.isArray(o.series) &&
    Array.isArray(o.claims),
  tick: (o) => num(o, 'share') && num(o, 'customers_out') && str(o, 'fips'),
  event_opened: (o) => str(o, 'outage_event_id') && (o.threshold === 'lo' || o.threshold === 'hi'),
  event_closed: (o) =>
    str(o, 'outage_event_id') && (o.threshold === 'lo' || o.threshold === 'hi') && num(o, 'duration_hours'),
  witness_entry: (o) => str(o, 'device_id') && str(o, 'policy_id') && str(o, 'kind') && bool(o, 'valid'),
  claim_decided: (o) => rec(o, 'claim'),
  payout_sent: (o) => rec(o, 'payout'),
  payout_confirmed: (o) => rec(o, 'payout'),
  replay_ended: (o) => str(o, 'replay_id'),
};

export type ParseResult = { ok: true; event: ReplayEvent } | { ok: false; reason: string };

/** Parse one SSE message body for a named event. Exported for tests and the mock. */
export function parseReplayEvent(name: string, raw: string): ParseResult {
  if (!(REPLAY_EVENT_NAMES as readonly string[]).includes(name)) return { ok: false, reason: `unknown event "${name}"` };
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    return { ok: false, reason: 'data is not JSON' };
  }
  if (!isRecord(data)) return { ok: false, reason: 'data is not an object' };
  // Every body includes sim_time; only an idle snapshot may carry null (section 5).
  const simOk = str(data, 'sim_time') || (name === 'snapshot' && data.sim_time === null);
  if (!simOk) return { ok: false, reason: 'missing sim_time' };
  const typed = name as ReplayEventName;
  if (!CHECKS[typed](data)) return { ok: false, reason: `missing or wrong fields for ${name}` };
  // Top-level fields checked above; nested rows are trusted to follow CONTRACTS.md.
  return { ok: true, event: { type: typed, data } as unknown as ReplayEvent };
}

export function openEventSourceStream(
  url: string,
  handlers: ReplayStreamHandlers,
  EventSourceImpl: typeof EventSource = EventSource,
): ReplayStreamHandle {
  let closed = false;
  handlers.onStatus?.('connecting');
  const es = new EventSourceImpl(url);
  es.onopen = () => {
    if (!closed) handlers.onStatus?.('open');
  };
  es.onerror = () => {
    if (closed) return;
    // CONNECTING means the browser will retry by itself; CLOSED means it gave up.
    handlers.onStatus?.(es.readyState === EventSourceImpl.CLOSED ? 'closed' : 'reconnecting');
  };
  for (const name of REPLAY_EVENT_NAMES) {
    es.addEventListener(name, (msg: MessageEvent<string>) => {
      if (closed) return;
      const r = parseReplayEvent(name, msg.data);
      if (r.ok) handlers.onEvent(r.event);
      else handlers.onMalformed?.(name, msg.data, r.reason);
    });
  }
  return {
    close() {
      closed = true;
      es.close();
      handlers.onStatus?.('closed');
    },
  };
}
