import { useEffect, useState } from 'react';
import { ApiError } from '../api/client';
import type { StreamStatus } from '../api/client';
import { useApi } from '../api/context';
import type { ReplayEventName } from '../api/types';
import { PageHeader } from '../components/Layout';
import { isMockApi } from '../mock/mockApi';
import { Probe } from '../components/Probe';

// K3 empty page: proves the stream wiring (status, snapshot on connect, event counts).
// F3 adds the stream reducer, the index chart, the 4-hour clock, the feed and claims.
export function LivePage() {
  const api = useApi();
  const [status, setStatus] = useState<StreamStatus>('connecting');
  const [counts, setCounts] = useState<Partial<Record<ReplayEventName, number>>>({});
  const [simTime, setSimTime] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  useEffect(() => {
    const handle = api.openReplayStream({
      onStatus: setStatus,
      onEvent: (ev) => {
        setSimTime(ev.data.sim_time);
        setCounts((c) => ({ ...c, [ev.type]: (c[ev.type] ?? 0) + 1 }));
      },
    });
    return () => handle.close();
  }, [api]);

  const act = (fn: () => Promise<unknown>) => () => {
    setActionError(null);
    fn().catch((e: unknown) => setActionError(e instanceof ApiError ? `${e.message} (${e.code})` : String(e)));
  };

  return (
    <>
      <PageHeader
        title="Live replay"
        purpose="Replay control, index chart with thresholds and the 4-hour clock, event feed, claims as they are decided."
      />
      <Probe
        what="scenarios"
        loadKey="scenarios"
        load={() => api.scenarios()}
        summary={(r) => r.scenarios.map((s) => (s.is_fixture ? `${s.title} [fixture]` : s.title)).join(', ') || 'none'}
      />
      <div className="row">
        <button type="button" onClick={act(() => api.startReplay({ scenario_id: 'fixture-replay', speed: 3600 }))} disabled={api.mode !== 'mock'}>
          Start fixture replay
        </button>
        <button type="button" onClick={act(() => api.stopReplay())}>
          Stop
        </button>
        {isMockApi(api) && (
          <button type="button" onClick={() => api.dropStream()}>
            Simulate stream drop
          </button>
        )}
      </div>
      {actionError && (
        <p role="alert" className="notice notice-error">
          {actionError}
        </p>
      )}
      <dl className="facts">
        <dt>Stream</dt>
        <dd data-testid="stream-status">{status}</dd>
        <dt>Simulated time</dt>
        <dd>{simTime ?? 'no events yet'}</dd>
        <dt>Events received</dt>
        <dd data-testid="stream-counts">
          {Object.entries(counts)
            .map(([k, v]) => `${k} ${v}`)
            .join(', ') || 'none'}
        </dd>
      </dl>
    </>
  );
}
