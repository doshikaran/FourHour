/**
 * F3: the live replay. Index chart with both thresholds, the 4-hour clocks, the payout moment,
 * claims as they are decided, and the event feed. Survives the stream dropping: the reducer
 * rebuilds from the server's snapshot on every reconnect.
 */
import { useEffect, useMemo, useReducer, useRef, useState } from 'react';
import { ApiError, type StreamStatus } from '../api/client';
import { useApi } from '../api/context';
import type { ScenarioSummary, TriggerParamsArtifact } from '../api/types';
import { IndexChart, SeriesTable, type ChartMarker } from '../components/IndexChart';
import { PageHeader } from '../components/Layout';
import { Empty, ErrorState, FixtureChip, Loading, NotComputed, ProvenanceDetails, RealDataChip, thetaHiWording } from '../components/ui';
import { CONFIRMED_HOLD_MS, focusClaim, initialLiveState, liveReducer, type FeedItem } from '../live/liveState';
import { DERECHO_SCENARIO_ID, DemoGuide } from '../live/DemoGuide';
import { ClaimRow, PayoutStage } from '../live/PayoutStage';
import { RunClock } from '../live/RunClocks';
import { formatClockUtc, formatCountyLocal, formatUtc } from '../lib/format';
import { useAsync } from '../lib/useAsync';
import { isMockApi } from '../mock/mockApi';

const SPEEDS: { value: number; label: string }[] = [
  { value: 1800, label: '30 simulated minutes per second' },
  { value: 3600, label: '1 simulated hour per second' },
  { value: 7200, label: '2 simulated hours per second' },
];

export function LivePage() {
  const api = useApi();
  const [state, dispatch] = useReducer(liveReducer, undefined, initialLiveState);
  const [status, setStatus] = useState<StreamStatus>('connecting');
  const scenarios = useAsync(() => api.scenarios(), 'scenarios');
  const trigger = useAsync(() => api.analytics('trigger'), 'analytics:trigger');
  const [chosen, setChosen] = useState('');
  const [speed, setSpeed] = useState(3600);
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<Error | null>(null);
  const lastStatus = useRef<StreamStatus>('connecting');
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const handle = api.openReplayStream({
      onStatus: (s) => {
        if (s === 'reconnecting' && lastStatus.current !== 'reconnecting') dispatch({ type: 'connection_lost', at: Date.now() });
        lastStatus.current = s;
        setStatus(s);
      },
      onEvent: (event) => {
        dispatch({ type: 'event', event, at: Date.now() });
        if (event.type === 'claim_decided') {
          // claim_decided carries the claim row only; the list adds member name and county.
          api.claims().then(
            (r) => dispatch({ type: 'enrich', items: r.claims.map((c) => ({ id: c.id, member_name: c.member_name, fips: c.fips })) }),
            () => undefined,
          );
        }
      },
    });
    return () => handle.close();
  }, [api]);

  // Re-render while a payout is sending or was just confirmed, so the focus can move on after the hold.
  const active = state.claims.some((c) => (c.payout && c.settledSeenAt === null) || (c.settledSeenAt !== null && now - c.settledSeenAt < CONFIRMED_HOLD_MS + 1000));
  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => setNow(Date.now()), 500);
    return () => clearInterval(id);
  }, [active]);

  const scenarioList = scenarios.status === 'ok' ? scenarios.data.scenarios : [];
  const selectedId = chosen || state.scenarioId || scenarioList[0]?.id || '';
  const running: ScenarioSummary | undefined = scenarioList.find((s) => s.id === (state.scenarioId ?? selectedId));

  const th: TriggerParamsArtifact | null = trigger.status === 'ok' ? trigger.data : null;
  const thetaHi = th?.theta_hi ?? null;
  const thetaLo = th?.theta_lo ?? null;
  const bridge = th?.gap_bridge_minutes ?? null;
  const dropout = th?.dropout_max_fraction ?? null;

  const points = useMemo(
    () => state.series.map((t) => ({ t: t.sim_time, share: t.share, customers_out: t.customers_out })),
    [state.series],
  );
  const markers = useMemo<ChartMarker[]>(() => {
    const m: ChartMarker[] = state.witness
      .filter((w) => w.kind !== 'heartbeat')
      .map((w) => ({ t: w.simTime, kind: 'witness', tone: 'neutral', label: `Witness: ${w.kind}${w.valid ? '' : ' (rejected)'}` }));
    for (const c of state.claims) {
      if (!c.claim.sim_time) continue;
      m.push({
        t: c.claim.sim_time,
        kind: 'claim',
        tone: c.claim.decision === 'paid' ? 'good' : c.claim.decision === 'review' ? 'warn' : 'neutral',
        label: `${c.claim.tier === 't4' ? '4 h' : c.claim.tier} ${c.claim.rule} ${c.claim.decision}`,
      });
    }
    return m;
  }, [state.witness, state.claims]);

  const act = (label: string, fn: () => Promise<void>) => async () => {
    setActionError(null);
    setBusy(label);
    try {
      await fn();
    } catch (e: unknown) {
      setActionError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      setBusy(null);
    }
  };
  const start = act('Starting', async () => {
    const r = await api.startReplay({ scenario_id: selectedId, speed });
    dispatch({ type: 'started', replayId: r.replay_id, scenarioId: selectedId, simStart: r.sim_start, simEnd: r.sim_end, at: Date.now() });
  });
  const stop = act('Stopping', async () => void (await api.stopReplay()));
  const reset = act('Resetting', async () => void (await api.resetReplay()));

  const domain: [string, string] | null = state.simStart && state.simEnd ? [state.simStart, state.simEnd] : running ? [running.start, running.end] : null;
  const isRunning = state.replayId !== null && !state.ended;
  const fips = running?.fips ?? state.series[0]?.fips ?? null;
  const focus = focusClaim(state, now);
  const local = formatCountyLocal(state.simTime, fips);
  const derecho = scenarioList.find((s) => s.id === DERECHO_SCENARIO_ID) ?? null;
  const peakShare = useMemo(() => {
    let m: number | null = null;
    for (const t of state.series) if (t.share !== null && (m === null || t.share > m)) m = t.share;
    return m;
  }, [state.series]);

  return (
    <>
      <PageHeader title="Live replay">
        <StreamPill status={status} />
      </PageHeader>

      <DemoGuide
        thetaLo={thetaLo}
        thetaHi={thetaHi}
        derechoTitle={derecho ? derecho.title : null}
        peakShare={peakShare}
        derechoRunning={state.scenarioId === DERECHO_SCENARIO_ID}
        replayEnded={state.ended}
      />

      <div className="card control-card" style={{ marginBottom: 16 }}>
        {scenarios.status === 'loading' && <Loading what="scenarios" />}
        {scenarios.status === 'error' && <ErrorState what="scenarios" error={scenarios.error} onRetry={scenarios.reload} />}
        {scenarios.status === 'ok' && scenarioList.length === 0 && <Empty>No replay scenarios are available on the backend yet.</Empty>}
        {scenarios.status === 'ok' && scenarioList.length > 0 && (
          <div className="control-bar">
            <div className="field">
              <label htmlFor="scenario">Scenario</label>
              <select id="scenario" value={selectedId} onChange={(e) => setChosen(e.target.value)} disabled={isRunning}>
                {scenarioList.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.is_fixture ? '[fixture] ' : ''}
                    {s.title}
                  </option>
                ))}
              </select>
            </div>
            <div className="field">
              <label htmlFor="speed">Speed</label>
              <select id="speed" value={speed} onChange={(e) => setSpeed(Number(e.target.value))} disabled={isRunning}>
                {SPEEDS.map((s) => (
                  <option key={s.value} value={s.value}>
                    {s.label}
                  </option>
                ))}
              </select>
            </div>
            <button type="button" className="big" onClick={start} disabled={!selectedId || busy !== null}>
              {isRunning ? 'Restart replay' : 'Start replay'}
            </button>
            <button type="button" className="secondary" onClick={stop} disabled={!isRunning || busy !== null}>
              Stop
            </button>
            <button type="button" className="quiet" onClick={reset} disabled={busy !== null} title="Clears the replay state. Claims and payouts already made stay listed.">
              Reset
            </button>
            {isMockApi(api) && (
              <button type="button" className="quiet" onClick={() => api.dropStream()}>
                Simulate stream drop
              </button>
            )}
            {busy && (
              <span role="status" className="row small">
                <span className="spinner" aria-hidden="true" /> {busy}…
              </span>
            )}
          </div>
        )}
        {actionError && (
<ErrorState what="the replay control" error={actionError} />
        )}
        {running && <ScenarioLine scenario={running} />}
      </div>

      {status === 'reconnecting' && (
        <p role="status" className="notice notice-warn toast">
          <strong>Connection to the replay stream dropped.</strong> Reconnecting now; when it is back, the screen rebuilds
          from the server's snapshot.
        </p>
      )}

      <div className="live-layout">
        <div className="stack">
          <section className="card" aria-labelledby="chart-h">
            <div className="card-title">
              <h2 id="chart-h">County outage index</h2>
              <span className="spacer" />
              <div style={{ textAlign: 'right' }}>
                <div className="sim-clock" data-testid="sim-clock">
                  {state.simTime ? formatUtc(state.simTime) : 'Not started'}
                </div>
                <div className="sim-clock-sub">
                  {local ? `${local} county local time · ` : ''}simulated clock
                </div>
              </div>
            </div>
            <ThresholdNote trigger={trigger} />
            <IndexChart
              points={points}
              domain={domain}
              thetaHi={thetaHi}
              thetaLo={thetaLo}
              thetaHiNote={thetaHiWording(th?.theta_hi_status).short}
              markers={markers}
              fips={fips}
              label={`County outage index, ${points.length} readings so far`}
            />
            <SeriesTable points={points} fips={fips} />
          </section>

          <section className="card" aria-labelledby="clocks-h">
            <h2 id="clocks-h">The 4-hour clock</h2>
            <div className="clocks">
              <RunClock title="Strong threshold (theta_hi)" theta={thetaHi} gapBridgeMinutes={bridge} dropoutMaxFraction={dropout} series={points} />
              <RunClock title="Lenient threshold (theta_lo)" theta={thetaLo} gapBridgeMinutes={bridge} dropoutMaxFraction={dropout} series={points} />
            </div>
            <p className="small muted" style={{ marginTop: 12 }}>
              Runs count continuous time at or above a threshold. Missing readings of up to {bridge === null ? <NotComputed /> : `${bridge} minutes`}{' '}
              between readings above it are bridged, and so is a sudden fall to{' '}
              {dropout === null ? <NotComputed /> : `${Math.round(dropout * 100)}% or less`} of the last reading when the index is back within that
              time (a partial-feed dropout). The clock is a display; the claim decision comes from the rule engine.
            </p>
          </section>
        </div>

        <div className="stack">
          <section aria-labelledby="payout-h">
            <h2 id="payout-h" className="visually-hidden">
              Payout
            </h2>
            <PayoutStage focus={focus} />
          </section>
          <section className="card" aria-labelledby="claims-h">
            <h2 id="claims-h">Claims in this replay</h2>
            {state.claims.length === 0 ? (
              <p className="muted">None yet.</p>
            ) : (
              <ul className="claim-list">
                {state.claims.map((c) => (
                  <ClaimRow key={c.claim.id} c={c} />
                ))}
              </ul>
            )}
          </section>
          <section className="card" aria-labelledby="feed-h">
            <div className="card-title">
              <h2 id="feed-h">What happened</h2>
              <span className="spacer" />
              <span className="small muted">{state.heartbeats ? `${state.heartbeats} witness heartbeats` : ''}</span>
            </div>
            <Feed items={state.feed} />
          </section>
          <p className="small muted">
            Replay mode maps live witness events onto the replay clock: the server stamps each incoming witness entry with
            the current simulated time and uses that for the rules. The entry keeps its real device time and signature.
          </p>
        </div>
      </div>
    </>
  );
}

function StreamPill({ status }: { status: StreamStatus }) {
  const map: Record<StreamStatus, { cls: string; text: string }> = {
    connecting: { cls: 'status-info', text: 'Connecting to stream' },
    open: { cls: 'status-good', text: 'Stream connected' },
    reconnecting: { cls: 'status-warn', text: 'Reconnecting…' },
    closed: { cls: 'status-bad', text: 'Stream closed' },
  };
  const m = map[status];
  return (
    <span className={`stream-pill ${m.cls}`} role="status" data-testid="stream-status" data-status={status}>
      <span className="dot" aria-hidden="true" />
      {m.text}
    </span>
  );
}

function ScenarioLine({ scenario }: { scenario: ScenarioSummary }) {
  return (
    <div style={{ marginTop: 12 }}>
      <p className="row" style={{ marginBottom: 4 }}>
        {scenario.is_fixture ? <FixtureChip /> : <RealDataChip what="EAGLE-I outage records" />}
        <strong>{scenario.title}</strong>
      </p>
      <p className="small muted" style={{ marginBottom: 0 }}>
        {scenario.description} Window {formatUtc(scenario.start)} to {formatUtc(scenario.end)}. The outage is replayed on a
        simulated clock; members and their losses are simulated.
      </p>
      {!scenario.is_fixture && (
        <>
          <p className="small" style={{ margin: '4px 0 0' }}>
            Source:{' '}
            <a href={scenario.source.url} target="_blank" rel="noreferrer">
              {scenario.source.dataset}
            </a>
            , file <code>{scenario.source.file}</code>
          </p>
          <ProvenanceDetails provenance={scenario.provenance} label="Where this replay's data comes from" />
        </>
      )}
    </div>
  );
}

function ThresholdNote({ trigger }: { trigger: ReturnType<typeof useAsync<TriggerParamsArtifact>> }) {
  if (trigger.status === 'loading') return <p className="small muted threshold-note">Loading thresholds…</p>;
  if (trigger.status === 'error') {
    const missing = trigger.error instanceof ApiError && trigger.error.code === 'artifact_missing';
    return missing ? (
      <p className="small">
        Thresholds: <NotComputed reason="artifacts/trigger_params.json is missing" />. The chart shows the index only.
      </p>
    ) : (
      <ErrorState what="the thresholds" error={trigger.error} onRetry={trigger.reload} />
    );
  }
  const t = trigger.data;
  return (
    <div className="small threshold-note" style={{ marginBottom: 8 }}>
      <p style={{ marginBottom: 4 }}>
        Thresholds from the trigger-design search on 2014–2022 data:{' '}
        {t.theta_hi === null || t.theta_lo === null ? <NotComputed /> : 'the strong and lenient lines below.'}{' '}
        {t.theta_hi !== null && thetaHiWording(t.theta_hi_status).long}
      </p>
      <ProvenanceDetails provenance={t.provenance} label="Threshold provenance" />
    </div>
  );
}

function Feed({ items }: { items: FeedItem[] }) {
  const ref = useRef<HTMLUListElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [items.length]);
  if (items.length === 0) {
    return <p className="muted">Start a replay to see events here.</p>;
  }
  return (
    <ul className="feed" ref={ref} aria-live="polite" aria-relevant="additions">
      {items.map((f) => (
        <li key={f.key}>
          <time>{f.simTime ? formatClockUtc(f.simTime) : ''}</time>
          <span className={`status-${f.tone}`} style={{ fontWeight: 400 }}>
            <span aria-hidden="true" style={{ marginRight: 6 }}>
              {f.icon}
            </span>
            <span style={{ color: 'var(--text)' }}>{f.text}</span>
          </span>
        </li>
      ))}
    </ul>
  );
}

