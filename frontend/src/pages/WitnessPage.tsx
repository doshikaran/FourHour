/**
 * F5: the witness page, opened on the device that stays on a charger at the premises.
 * Key generation (non-extractable ECDSA P-256), signed hash-chained entries, local storage before
 * upload, in-order upload with retries, self-reported fallback, and its own status.
 */
import { useState } from 'react';
import { useApi } from '../api/context';
import type { PolicyId } from '../api/types';
import { PageHeader } from '../components/Layout';
import { AsyncView, ErrorState, Loading, SelfReportedChip, SimulatedChip, TestCoverChip, isSelfReported, isTestCover } from '../components/ui';
import { formatUtc } from '../lib/format';
import { Link } from '../lib/router';
import { useAsync } from '../lib/useAsync';
import { exportFixture, type StoredEntry } from '../witness/log';
import { useWitness, type WitnessView } from '../witness/useWitness';

export function WitnessPage({ policyId }: { policyId: string }) {
  const api = useApi();
  const policy = useAsync(() => api.policy(policyId as PolicyId), `witness-policy:${policyId}`);
  const hasSubtle = typeof crypto !== 'undefined' && typeof crypto.subtle !== 'undefined';
  return (
    <>
      <PageHeader
        title="Witness"
        purpose="Keep this page open on a laptop or phone that stays on a charger at your premises. It keeps a signed log of when the charger loses power."
      />
      <AsyncView state={policy} what={`policy ${policyId}`}>
        {(r) => (
          <p className="row">
            Policy <code>{r.policy.id}</code> · {r.member.name} <SimulatedChip what="member" />
            {isTestCover(r.policy) && <TestCoverChip />}
            <Link to={`/policies/${encodeURIComponent(r.policy.id)}`}>Back to the policy</Link>
          </p>
        )}
      </AsyncView>
      {!hasSubtle ? (
        <p role="alert" className="notice notice-error">
          <strong>This page needs WebCrypto, which browsers only offer over HTTPS or on localhost.</strong> Open it from an
          https:// address.
        </p>
      ) : (
        policy.status === 'ok' && <WitnessBody policyId={policy.data.policy.id} />
      )}
    </>
  );
}

function WitnessBody({ policyId }: { policyId: PolicyId }) {
  const api = useApi();
  const w = useWitness(api, policyId);

  return (
    <div className="stack">
      {w.storageWarning && (
        <p role="alert" className="notice notice-warn">
          <strong>Not saving to this browser.</strong> {w.storageWarning}
        </p>
      )}
      {w.keyWarning && w.phase.kind === 'ready' && (
        <p role="status" className="notice notice-warn">
          {w.keyWarning}
        </p>
      )}
      {w.phase.kind === 'starting' && <Loading what="this device's witness log" />}
      {w.phase.kind === 'failed' && <ErrorState what="the witness" error={w.phase.error} />}
      {(w.phase.kind === 'unregistered' || w.phase.kind === 'registering' || w.phase.kind === 'key_lost') && <Setup w={w} />}
      {w.phase.kind === 'ready' && w.log && <Running w={w} />}
    </div>
  );
}

function Setup({ w }: { w: WitnessView }) {
  return (
    <section className="card">
      <h2>Set up this device</h2>
      {w.phase.kind === 'key_lost' && (
        <p role="alert" className="notice notice-warn">
          This browser has a saved log for this policy, but its signing key is gone, so it cannot add to that log. Set up
          again to start a new device log.
        </p>
      )}
      <p>
        This creates a signing key on this device and registers its public half with FourHour. The private key never
        leaves this browser and cannot be exported.
      </p>
      <SenseSummary w={w} />
      <button type="button" className="big" onClick={w.register} disabled={w.phase.kind === 'registering'}>
        {w.phase.kind === 'registering' ? 'Setting up…' : 'Set up this device as the witness'}
      </button>
    </section>
  );
}

function SenseSummary({ w }: { w: WitnessView }) {
  if (w.sense.kind === 'checking') return <p className="muted">Checking whether this browser can sense the charger…</p>;
  if (w.sense.kind === 'battery') {
    return <p>✓ This browser reports charger changes (Battery Status API). Unplugging is logged automatically.</p>;
  }
  return (
    <p>
      <SelfReportedChip /> {w.sense.why} You will report outages with the “Power is out” and “Power is back” buttons
      instead. Those entries are labelled self-reported.
    </p>
  );
}

function Running({ w }: { w: WitnessView }) {
  const log = w.log;
  if (!log) return null;
  const rec = log.record;
  const pending = log.pending().length;
  const uploaded = rec.entries.filter((e) => e.uploaded).length;
  const last = rec.entries[rec.entries.length - 1];
  const lastBeat = [...rec.entries].reverse().find((e) => e.entry.kind === 'heartbeat');

  return (
    <>
      <section className="card" aria-labelledby="state-h">
        <h2 id="state-h" className="visually-hidden">
          Power state
        </h2>
        <PowerState w={w} />
        {w.sense.kind === 'manual' && (
          <div style={{ marginTop: 16 }}>
            <p className="small">
              <SelfReportedChip /> {w.sense.why}
            </p>
            <div className="manual-buttons">
              <button type="button" className="big" onClick={() => w.attest('attest_lost')}>
                ✕ Power is out
              </button>
              <button type="button" className="big secondary" onClick={() => w.attest('attest_restored')}>
                ✓ Power is back
              </button>
            </div>
          </div>
        )}
      </section>

      <section className="card" aria-labelledby="up-h">
        <div className="card-title">
          <h2 id="up-h">Log and upload</h2>
          <span className="spacer" />
          <span className={`status ${w.online ? 'status-good' : 'status-warn'}`}>
            <span className="status-icon" aria-hidden="true">
              {w.online ? '✓' : '!'}
            </span>
            {w.online ? 'Online' : 'Offline: still logging'}
          </span>
        </div>
        <dl className="kv" aria-live="polite">
          <dt>Entries logged</dt>
          <dd data-testid="entries-total">{rec.entries.length}</dd>
          <dt>Uploaded</dt>
          <dd data-testid="entries-uploaded">{uploaded}</dd>
          <dt>Waiting to upload</dt>
          <dd data-testid="entries-pending">{pending}</dd>
          <dt>Last upload</dt>
          <dd>{rec.last_upload_at ? formatUtc(rec.last_upload_at) : 'not yet'}</dd>
          <dt>Last heartbeat</dt>
          <dd>{lastBeat ? `${formatUtc(lastBeat.entry.device_time)} (every 60 s while this page is open)` : 'not yet'}</dd>
          <dt>Device</dt>
          <dd>
            <code>{rec.device_id}</code>
          </dd>
        </dl>
        {rec.halted && (
          <div role="alert" className="notice notice-error" style={{ marginTop: 12 }}>
            <p>{rec.halted}</p>
            <ForgetButton onForget={w.forget} label="Set up this device again" />
          </div>
        )}
        {!rec.halted && rec.last_upload_error && (
          <p role="status" className="notice notice-warn" style={{ marginTop: 12 }}>
            Last upload failed: {rec.last_upload_error}. Entries are kept on this device and retried every 15 seconds and when
            the connection returns.
          </p>
        )}
        <p className="small muted" style={{ marginTop: 12 }}>
          Every entry is saved on this device before it is sent, and entries are sent in order. Heartbeats only show the page
          is open; if this laptop sleeps or the tab is hidden they stop, and that is not counted as a power cut. Only “lost
          power” and “power back” entries mark an outage. During a replay the server stamps each entry with the replay clock;
          the entry keeps this device's own time and signature.
        </p>
        <div className="row">
          <button type="button" className="secondary" onClick={w.uploadNow} disabled={w.uploading || pending === 0}>
            {w.uploading ? 'Uploading…' : 'Upload now'}
          </button>
          <ExportButton w={w} />
          {!rec.halted && <ForgetButton onForget={w.forget} label="Stop using this device" />}
        </div>
      </section>

      <section className="card" aria-labelledby="log-h">
        <h2 id="log-h">This device's log</h2>
        {last ? <EntryTable entries={rec.entries} /> : <p className="muted">No entries yet.</p>}
      </section>
    </>
  );
}

function PowerState({ w }: { w: WitnessView }) {
  const rec = w.log?.record;
  if (w.sense.kind === 'checking') return <p className="muted">Checking the charger…</p>;
  if (w.sense.kind === 'battery') {
    const on = w.sense.charging;
    return (
      <div className="witness-status" data-testid="power-state" data-charging={String(on)}>
        <span className={`big-icon ${on ? 'status-good' : 'status-bad'}`} aria-hidden="true">
          {on ? '⚡' : '✕'}
        </span>
        <div>
          <div className="witness-state">{on ? 'On charger: power is on' : 'Charger disconnected: logged as power lost'}</div>
          <div className="muted">
            Battery {Math.round(w.sense.level * 100)}% · sensed by the Battery Status API · changes are logged within seconds
          </div>
        </div>
      </div>
    );
  }
  const on = rec?.attested_charging ?? true;
  return (
    <div className="witness-status" data-testid="power-state" data-charging={String(on)}>
      <span className={`big-icon ${on ? 'status-good' : 'status-bad'}`} aria-hidden="true">
        {on ? '✓' : '✕'}
      </span>
      <div>
        <div className="witness-state">{on ? 'Power reported on' : 'Power reported out'}</div>
        <div className="muted">
          <SelfReportedChip /> from your last button press
        </div>
      </div>
    </div>
  );
}

const KIND_TEXT: Record<StoredEntry['entry']['kind'], string> = {
  heartbeat: 'Heartbeat',
  power_lost: 'Charger lost power',
  power_restored: 'Charger power back',
  attest_lost: 'Power is out',
  attest_restored: 'Power is back',
};

function EntryTable({ entries }: { entries: StoredEntry[] }) {
  const recent = entries.slice(-50).reverse();
  return (
    <div className="table-wrap" style={{ maxHeight: 420, overflowY: 'auto' }}>
      <table className="plain">
        <thead>
          <tr>
            <th className="num">Seq</th>
            <th>Entry</th>
            <th>Device time</th>
            <th>Charging</th>
            <th>Battery</th>
            <th>Upload</th>
          </tr>
        </thead>
        <tbody>
          {recent.map((s) => (
            <tr key={s.entry.seq}>
              <td className="num">{s.entry.seq}</td>
              <td>
                {KIND_TEXT[s.entry.kind]} {isSelfReported(s.entry.kind) && <SelfReportedChip />}
              </td>
              <td>{formatUtc(s.entry.device_time)}</td>
              <td>{s.entry.charging ? 'yes' : 'no'}</td>
              <td>{s.entry.battery_level === null ? '—' : `${Math.round(s.entry.battery_level * 100)}%`}</td>
              <td>{s.rejected ? `✕ rejected (${s.rejected})` : s.uploaded ? '✓ uploaded' : '… waiting'}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {entries.length > 50 && <p className="small muted">Showing the latest 50 of {entries.length}.</p>}
    </div>
  );
}

function ExportButton({ w }: { w: WitnessView }) {
  const rec = w.log?.record;
  const exportIt = () => {
    if (!rec) return;
    const fx = exportFixture(rec, {
      userAgent: navigator.userAgent,
      origin: window.location.origin,
      generatedAt: new Date().toISOString().replace(/\.\d{3}Z$/, 'Z'),
    });
    const blob = new Blob([JSON.stringify(fx, null, 2)], { type: 'application/json' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = `witness-fixture-${rec.device_id}.json`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  };
  return (
    <button type="button" className="quiet" onClick={exportIt} disabled={!rec || rec.entries.length === 0} title="Download the entries and public key as a test fixture for the backend">
      Download log as a test fixture
    </button>
  );
}

function ForgetButton({ onForget, label }: { onForget: () => void; label: string }) {
  const [armed, setArmed] = useState(false);
  if (!armed) {
    return (
      <button type="button" className="quiet" onClick={() => setArmed(true)}>
        {label}
      </button>
    );
  }
  return (
    <span className="row">
      <span className="small">This deletes the key and log on this device. Entries already uploaded stay on the server.</span>
      <button type="button" onClick={onForget}>
        Yes, delete
      </button>
      <button type="button" className="quiet" onClick={() => setArmed(false)}>
        Cancel
      </button>
    </span>
  );
}
