/**
 * The witness log (F5): appends signed, hash-chained entries, writes each one to storage BEFORE
 * any upload is attempted, and uploads them in seq order. Appends and uploads are each serialised
 * on a promise chain, so two charger events in the same tick can never share a seq.
 *
 * Witness outage intervals come only from power_lost / power_restored (or attest_*) entries
 * (SPEC section 8). Heartbeats only show the page is alive; a missing heartbeat means nothing.
 */
import { ApiError, type FourHourApi } from '../api/client';
import type {
  DeviceId,
  PolicyId,
  PublicKeyJwk,
  WitnessEntry,
  WitnessEntryBody,
  WitnessKind,
  WitnessRejectReason,
} from '../api/types';
import { GENESIS_HASH, buildHashInput, isoSeconds } from '../lib/witnessHash';
import { createEntry, exportPublicJwk, generateWitnessKeys } from './entry';
import type { KeyStore } from './keystore';
import type { KeyValueStore } from './storage';

export const RECORD_VERSION = 1;
export const HEARTBEAT_MS = 60_000;
export const UPLOAD_BATCH = 50;

export type RejectReason = WitnessRejectReason | 'malformed';

export interface StoredEntry {
  entry: WitnessEntry;
  uploaded: boolean;
  rejected: RejectReason | null;
  /** How the values were obtained, for the exported fixture. */
  note: string;
}

export interface WitnessRecord {
  version: typeof RECORD_VERSION;
  policy_id: PolicyId;
  device_id: DeviceId;
  public_key_jwk: PublicKeyJwk;
  genesis_hash: string;
  registered_at: string;
  entries: StoredEntry[];
  last_upload_at: string | null;
  last_upload_error: string | null;
  /** Set when the server rejected an entry: the chain cannot continue on this device record. */
  halted: string | null;
  /** Last self-reported state, used for `charging` without a Battery API (CONTRACTS 1.4 section 6). */
  attested_charging: boolean;
}

export interface Reading {
  charging: boolean;
  battery_level: number | null;
}

export interface WitnessDeps {
  store: KeyValueStore;
  subtle?: SubtleCrypto;
  now?: () => Date;
  online?: () => boolean;
}

export const storageKey = (policyId: string) => `fourhour.witness.${policyId}`;

function isRecord(v: unknown): v is WitnessRecord {
  if (typeof v !== 'object' || v === null) return false;
  const r = v as Partial<WitnessRecord>;
  return r.version === RECORD_VERSION && typeof r.device_id === 'string' && Array.isArray(r.entries) && typeof r.genesis_hash === 'string';
}

export function loadRecord(store: KeyValueStore, policyId: string): WitnessRecord | null {
  const raw = store.get(storageKey(policyId));
  if (!raw) return null;
  try {
    const v: unknown = JSON.parse(raw);
    return isRecord(v) ? v : null;
  } catch {
    return null;
  }
}

/** Creates a key pair on this device, registers the public key, and saves both. */
export async function registerWitness(
  api: Pick<FourHourApi, 'registerDevice'>,
  policyId: PolicyId,
  keys: KeyStore,
  deps: WitnessDeps,
): Promise<{ record: WitnessRecord; keyPair: CryptoKeyPair }> {
  const subtle = deps.subtle ?? crypto.subtle;
  const keyPair = await generateWitnessKeys(subtle);
  const jwk = await exportPublicJwk(keyPair.publicKey, subtle);
  const res = await api.registerDevice({ policy_id: policyId, public_key_jwk: jwk });
  const record: WitnessRecord = {
    version: RECORD_VERSION,
    policy_id: policyId,
    device_id: res.device_id,
    public_key_jwk: jwk,
    genesis_hash: res.genesis_hash,
    registered_at: isoSeconds((deps.now ?? (() => new Date()))()),
    entries: [],
    last_upload_at: null,
    last_upload_error: null,
    halted: null,
    attested_charging: true,
  };
  await keys.save(policyId, keyPair);
  deps.store.set(storageKey(policyId), JSON.stringify(record));
  return { record, keyPair };
}

/** Start of the halt message when the server no longer knows this device (for example after a database reset). */
export const UNKNOWN_DEVICE_HALT = 'The server does not know this device';

/** True for the backend's answer to a device it does not know: 404, normally with code device_not_found. */
export function isUnknownDeviceError(e: unknown): boolean {
  return e instanceof ApiError && (e.code === 'device_not_found' || e.status === 404);
}

export function isUnknownDeviceHalt(halted: string | null | undefined): boolean {
  return typeof halted === 'string' && halted.startsWith(UNKNOWN_DEVICE_HALT);
}

export type UploadOutcome =
  | { status: 'nothing' }
  | { status: 'uploaded'; accepted: number }
  | { status: 'offline' }
  | { status: 'failed'; error: string }
  | { status: 'halted'; reason: string };

export class WitnessLog {
  private rec: WitnessRecord;
  private appendChain: Promise<unknown> = Promise.resolve();
  private uploadChain: Promise<UploadOutcome> = Promise.resolve({ status: 'nothing' });
  private listeners = new Set<() => void>();
  private readonly subtle: SubtleCrypto;
  private readonly now: () => Date;
  private readonly online: () => boolean;

  constructor(
    record: WitnessRecord,
    private readonly privateKey: CryptoKey,
    private readonly deps: WitnessDeps,
  ) {
    this.rec = record;
    this.subtle = deps.subtle ?? crypto.subtle;
    this.now = deps.now ?? (() => new Date());
    this.online = deps.online ?? (() => navigator.onLine);
  }

  get record(): WitnessRecord {
    return this.rec;
  }

  subscribe(fn: () => void): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  /** Marks the log as unusable because the server does not know its device. Returns the message. */
  haltUnknownDevice(): string {
    const halted = `${UNKNOWN_DEVICE_HALT} (${this.rec.device_id}). The server's database may have been reset, for example when the hosted service restarted. Register this device again to start a new log.`;
    this.commit({ ...this.rec, halted, last_upload_error: null });
    return halted;
  }

  private commit(next: WitnessRecord): void {
    this.rec = next;
    this.deps.store.set(storageKey(next.policy_id), JSON.stringify(next));
    for (const fn of this.listeners) fn();
  }

  pending(): StoredEntry[] {
    return this.rec.entries.filter((e) => !e.uploaded && e.rejected === null);
  }

  /** Signs and stores one entry. Resolves after it is saved, before any upload. */
  append(kind: WitnessKind, reading: Reading, note: string): Promise<StoredEntry> {
    const run = async (): Promise<StoredEntry> => {
      const last = this.rec.entries[this.rec.entries.length - 1];
      const body: WitnessEntryBody = {
        seq: last ? last.entry.seq + 1 : 1,
        device_time: isoSeconds(this.now()),
        kind,
        charging: reading.charging,
        battery_level: reading.battery_level,
        online: this.online(),
        prev_hash: last ? last.entry.hash : this.rec.genesis_hash,
      };
      const entry = await createEntry(body, this.privateKey, this.subtle);
      const stored: StoredEntry = { entry, uploaded: false, rejected: null, note };
      const attested = kind === 'attest_lost' ? false : kind === 'attest_restored' ? true : this.rec.attested_charging;
      this.commit({ ...this.rec, entries: [...this.rec.entries, stored], attested_charging: attested });
      return stored;
    };
    const p = this.appendChain.then(run, run);
    this.appendChain = p.catch(() => undefined);
    return p;
  }

  /** Uploads pending entries in seq order, in batches, until none are left or one fails. */
  upload(api: Pick<FourHourApi, 'postEntries'>): Promise<UploadOutcome> {
    const run = () => this.uploadOnce(api);
    // Wait for in-flight appends so an upload never skips an entry being signed.
    const p = this.uploadChain.then(() => this.appendChain).then(run, run);
    this.uploadChain = p.catch((e: unknown) => ({ status: 'failed', error: String(e) }) as UploadOutcome);
    return p;
  }

  private async uploadOnce(api: Pick<FourHourApi, 'postEntries'>): Promise<UploadOutcome> {
    let accepted = 0;
    for (;;) {
      if (this.rec.halted) return { status: 'halted', reason: this.rec.halted };
      const batch = this.pending().slice(0, UPLOAD_BATCH);
      if (batch.length === 0) return accepted ? { status: 'uploaded', accepted } : { status: 'nothing' };
      if (!this.online()) return { status: 'offline' };
      let res;
      try {
        res = await api.postEntries({ device_id: this.rec.device_id, entries: batch.map((b) => b.entry) });
      } catch (e: unknown) {
        if (isUnknownDeviceError(e)) {
          // The server no longer knows this device (for example after a database reset). Retrying
          // cannot help; the member must set the device up again.
          const halted = this.haltUnknownDevice();
          return { status: 'halted', reason: halted };
        }
        const error = (e instanceof Error ? e.message : String(e)).replace(/\.+$/, '');
        this.commit({ ...this.rec, last_upload_error: error });
        return { status: 'failed', error };
      }
      const rejected = new Map(res.rejected.map((r) => [r.seq, r.reason as RejectReason]));
      const sent = new Set(batch.map((b) => b.entry.seq));
      const entries = this.rec.entries.map((s) => {
        if (!sent.has(s.entry.seq)) return s;
        const reason = rejected.get(s.entry.seq);
        if (reason) return { ...s, rejected: reason };
        return s.entry.seq <= res.last_seq ? { ...s, uploaded: true } : s;
      });
      const first = res.rejected[0];
      const halted = first
        ? `The server rejected entry ${first.seq} (${first.reason}). Later entries chain from it, so this device log cannot continue. Set this device up again to start a new log.`
        : null;
      const progress = entries.filter((s) => s.uploaded).length - this.rec.entries.filter((s) => s.uploaded).length;
      accepted += Math.max(0, progress);
      this.commit({ ...this.rec, entries, last_upload_at: isoSeconds(this.now()), last_upload_error: null, halted });
      if (halted) return { status: 'halted', reason: halted };
      if (progress === 0) {
        // Nothing moved forward and nothing was rejected: the server's view differs from ours.
        const error = `The server's last entry is ${res.last_seq}; it accepted none of entries ${batch[0]?.entry.seq}–${batch[batch.length - 1]?.entry.seq}.`;
        this.commit({ ...this.rec, last_upload_error: error });
        return { status: 'failed', error };
      }
    }
  }
}

/** The format of frontend/spikes/fixtures (fourhour-witness-fixture/1), readable without JS tooling. */
export interface WitnessFixture {
  fixture_format: 'fourhour-witness-fixture/1';
  generated_by: string;
  generated_at: string;
  user_agent: string;
  page_origin: string;
  policy_id: string;
  device_id: string;
  public_key_jwk: PublicKeyJwk;
  genesis_hash: string;
  entries: WitnessEntry[];
  hash_inputs: string[];
  entry_notes: string[];
}

export function exportFixture(rec: WitnessRecord, meta: { userAgent: string; origin: string; generatedAt: string }): WitnessFixture {
  return {
    fixture_format: 'fourhour-witness-fixture/1',
    generated_by: 'frontend/src/pages/WitnessPage.tsx (F5 witness page)',
    generated_at: meta.generatedAt,
    user_agent: meta.userAgent,
    page_origin: meta.origin,
    policy_id: rec.policy_id,
    device_id: rec.device_id,
    public_key_jwk: rec.public_key_jwk,
    genesis_hash: rec.genesis_hash,
    entries: rec.entries.map((s) => s.entry),
    hash_inputs: rec.entries.map((s) => buildHashInput(s.entry)),
    entry_notes: rec.entries.map((s) => s.note),
  };
}

export { GENESIS_HASH };
