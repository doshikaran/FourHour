/**
 * Runs the witness on this device: loads or registers the device log, watches the charger through
 * the Battery Status API where it exists, writes heartbeats every 60 s, and uploads in order with
 * retries. Without the Battery Status API the page offers self-reported entries only.
 */
import { useCallback, useEffect, useRef, useState } from 'react';
import type { FourHourApi } from '../api/client';
import type { PolicyId, WitnessKind } from '../api/types';
import { createKeyStore, type KeyStore } from './keystore';
import { HEARTBEAT_MS, WitnessLog, loadRecord, registerWitness, storageKey, type Reading, type UploadOutcome } from './log';
import { createSafeStore, type KeyValueStore } from './storage';

/** The Battery Status API is not in TypeScript's DOM types. */
interface BatteryManager extends EventTarget {
  readonly charging: boolean;
  readonly level: number;
}
type NavigatorWithBattery = Navigator & { getBattery?: () => Promise<BatteryManager> };

export type WitnessPhase =
  | { kind: 'starting' }
  | { kind: 'unregistered' }
  | { kind: 'registering' }
  | { kind: 'ready' }
  | { kind: 'key_lost' }
  | { kind: 'failed'; error: Error };

export type SenseMode = { kind: 'checking' } | { kind: 'battery'; charging: boolean; level: number } | { kind: 'manual'; why: string };

export interface WitnessView {
  phase: WitnessPhase;
  sense: SenseMode;
  log: WitnessLog | null;
  /** Bumped on every log change, so components re-render. */
  version: number;
  online: boolean;
  storageWarning: string | null;
  keyWarning: string | null;
  lastOutcome: UploadOutcome | null;
  uploading: boolean;
  register: () => void;
  attest: (kind: 'attest_lost' | 'attest_restored') => void;
  uploadNow: () => void;
  forget: () => void;
}

const SENSED_NOTE = 'device_time, charging, battery_level and online are real readings (Battery Status API)';
const ATTEST_NOTE = 'self-reported by the member with a button; charging is the attested state; no Battery Status API, battery_level null';
const MANUAL_HEARTBEAT_NOTE = 'heartbeat; charging is the last attested state (no Battery Status API), battery_level null';

export function batteryApiAvailable(): boolean {
  return typeof navigator !== 'undefined' && typeof (navigator as NavigatorWithBattery).getBattery === 'function';
}

export function manualReason(): string {
  const ua = typeof navigator !== 'undefined' ? navigator.userAgent : '';
  if (/iPhone|iPad|iPod/.test(ua)) return 'Safari on iPhone and iPad has no Battery Status API, so this page cannot sense the charger.';
  if (/Firefox\//.test(ua)) return 'Firefox has no Battery Status API, so this page cannot sense the charger.';
  if (/Safari\//.test(ua) && !/Chrome\//.test(ua)) return 'Safari has no Battery Status API, so this page cannot sense the charger.';
  return 'This browser does not offer the Battery Status API, so this page cannot sense the charger.';
}

export function useWitness(api: FourHourApi, policyId: PolicyId): WitnessView {
  const [phase, setPhase] = useState<WitnessPhase>({ kind: 'starting' });
  const [sense, setSense] = useState<SenseMode>({ kind: 'checking' });
  const [log, setLog] = useState<WitnessLog | null>(null);
  const [version, setVersion] = useState(0);
  const [online, setOnline] = useState(() => (typeof navigator === 'undefined' ? true : navigator.onLine));
  const [lastOutcome, setLastOutcome] = useState<UploadOutcome | null>(null);
  const [uploading, setUploading] = useState(false);
  const [keyWarning, setKeyWarning] = useState<string | null>(null);
  const storeRef = useRef<KeyValueStore | null>(null);
  const keysRef = useRef<KeyStore | null>(null);
  const senseRef = useRef<SenseMode>({ kind: 'checking' });
  senseRef.current = sense;

  if (!storeRef.current) storeRef.current = createSafeStore();
  const store = storeRef.current;

  const doUpload = useCallback(
    (l: WitnessLog) => {
      setUploading(true);
      l.upload(api)
        .then(setLastOutcome, (e: unknown) => setLastOutcome({ status: 'failed', error: String(e) }))
        .finally(() => setUploading(false));
    },
    [api],
  );

  const appendAndUpload = useCallback(
    (l: WitnessLog, kind: WitnessKind, reading: Reading, note: string) => {
      l.append(kind, reading, note).then(
        () => doUpload(l),
        (e: unknown) => setPhase({ kind: 'failed', error: e instanceof Error ? e : new Error(String(e)) }),
      );
    },
    [doUpload],
  );

  const currentReading = useCallback((l: WitnessLog): { reading: Reading; note: string } => {
    const s = senseRef.current;
    if (s.kind === 'battery') return { reading: { charging: s.charging, battery_level: s.level }, note: SENSED_NOTE };
    return { reading: { charging: l.record.attested_charging, battery_level: null }, note: MANUAL_HEARTBEAT_NOTE };
  }, []);

  // 1. Load the stored log and key.
  useEffect(() => {
    let live = true;
    (async () => {
      const keys = await createKeyStore();
      if (!live) return;
      keysRef.current = keys;
      if (!keys.persistent) {
        setKeyWarning('This browser cannot keep the signing key across reloads. If this page reloads, the device must be set up again.');
      }
      const rec = loadRecord(store, policyId);
      if (!rec) return setPhase({ kind: 'unregistered' });
      const pair = await keys.load(policyId);
      if (!live) return;
      if (!pair) return setPhase({ kind: 'key_lost' });
      setLog(new WitnessLog(rec, pair.privateKey, { store }));
      setPhase({ kind: 'ready' });
    })().catch((e: unknown) => live && setPhase({ kind: 'failed', error: e instanceof Error ? e : new Error(String(e)) }));
    return () => {
      live = false;
    };
  }, [policyId, store]);

  // 2. Battery Status API, or manual mode.
  useEffect(() => {
    const nav = navigator as NavigatorWithBattery;
    if (!nav.getBattery) {
      setSense({ kind: 'manual', why: manualReason() });
      return;
    }
    let live = true;
    let battery: BatteryManager | null = null;
    const update = () => battery && live && setSense({ kind: 'battery', charging: battery.charging, level: battery.level });
    nav.getBattery().then(
      (b) => {
        if (!live) return;
        battery = b;
        update();
        b.addEventListener('chargingchange', update);
        b.addEventListener('levelchange', update);
      },
      () => live && setSense({ kind: 'manual', why: 'The browser refused access to the Battery Status API.' }),
    );
    return () => {
      live = false;
      battery?.removeEventListener('chargingchange', update);
      battery?.removeEventListener('levelchange', update);
    };
  }, []);

  // 3. Subscribe to log changes.
  useEffect(() => {
    if (!log) return;
    return log.subscribe(() => setVersion((v) => v + 1));
  }, [log]);

  // 4. Charger changes become power_lost / power_restored entries.
  const lastCharging = useRef<boolean | null>(null);
  useEffect(() => {
    if (!log || sense.kind !== 'battery') return;
    const prev = lastCharging.current;
    lastCharging.current = sense.charging;
    if (prev === null || prev === sense.charging) return;
    appendAndUpload(
      log,
      sense.charging ? 'power_restored' : 'power_lost',
      { charging: sense.charging, battery_level: sense.level },
      SENSED_NOTE,
    );
  }, [log, sense, appendAndUpload]);

  // 5. A heartbeat when logging starts and every 60 s while the page is open.
  const senseReady = sense.kind !== 'checking';
  useEffect(() => {
    if (!log || !senseReady) return;
    const beat = () => {
      const { reading, note } = currentReading(log);
      appendAndUpload(log, 'heartbeat', reading, note);
    };
    beat();
    const id = setInterval(beat, HEARTBEAT_MS);
    return () => clearInterval(id);
  }, [log, senseReady, currentReading, appendAndUpload]);

  // 6. Upload on reconnect, and retry every 15 s while anything is waiting.
  useEffect(() => {
    if (!log) return;
    const on = () => {
      setOnline(true);
      doUpload(log);
    };
    const off = () => setOnline(false);
    window.addEventListener('online', on);
    window.addEventListener('offline', off);
    const id = setInterval(() => {
      if (log.pending().length > 0 && !log.record.halted) doUpload(log);
    }, 15_000);
    doUpload(log);
    return () => {
      window.removeEventListener('online', on);
      window.removeEventListener('offline', off);
      clearInterval(id);
    };
  }, [log, doUpload]);

  const register = useCallback(() => {
    const keys = keysRef.current;
    if (!keys) return;
    setPhase({ kind: 'registering' });
    registerWitness(api, policyId, keys, { store })
      .then(({ record, keyPair }) => {
        lastCharging.current = null;
        setLog(new WitnessLog(record, keyPair.privateKey, { store }));
        setPhase({ kind: 'ready' });
      })
      .catch((e: unknown) => setPhase({ kind: 'failed', error: e instanceof Error ? e : new Error(String(e)) }));
  }, [api, policyId, store]);

  const attest = useCallback(
    (kind: 'attest_lost' | 'attest_restored') => {
      if (!log) return;
      appendAndUpload(log, kind, { charging: kind === 'attest_restored', battery_level: null }, ATTEST_NOTE);
    },
    [log, appendAndUpload],
  );

  const forget = useCallback(() => {
    store.remove(storageKey(policyId));
    void keysRef.current?.remove(policyId);
    lastCharging.current = null;
    setLog(null);
    setLastOutcome(null);
    setPhase({ kind: 'unregistered' });
  }, [store, policyId]);

  const uploadNow = useCallback(() => log && doUpload(log), [log, doUpload]);

  return {
    phase,
    sense,
    log,
    version,
    online,
    storageWarning: store.warning,
    keyWarning,
    lastOutcome,
    uploading,
    register,
    attest,
    uploadNow,
    forget,
  };
}
