// @vitest-environment node
/**
 * F5 witness code against real browser output and the backend's checks:
 * - reproduces every hash (and verifies every signature) in the two S5 fixtures, one of them from a
 *   real MacBook Chrome 157 run, using the same code path the witness page uses;
 * - produces a log that the mock's real verification accepts, and whose exported fixture passes
 *   frontend/spikes/verify-fixture.cjs (Node's OpenSSL, not WebCrypto);
 * - keeps logging with the network down and uploads in order when it returns;
 * - storage failure degrades to memory with a warning.
 */
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, describe, expect, it } from 'vitest';
import type { PolicyId, PublicKeyJwk, WitnessEntry } from '../api/types';
import { buildHashInput, GENESIS_HASH } from '../lib/witnessHash';
import { createMockApi, type MockApi } from '../mock/mockApi';
import { fixturePolicy } from '../mock/fixtures';
import { bodyOf, checkEntry, generateWitnessKeys, hashEntryBody, importPublicJwk } from '../witness/entry';
import { createMemoryKeyStore } from '../witness/keystore';
import { WitnessLog, exportFixture, loadRecord, registerWitness } from '../witness/log';
import { createMemoryStore, createSafeStore } from '../witness/storage';

interface Fixture {
  fixture_format: string;
  public_key_jwk: PublicKeyJwk;
  genesis_hash: string;
  entries: WitnessEntry[];
  hash_inputs: string[];
}

const spikes = fileURLToPath(new URL('../../spikes/', import.meta.url));
const load = (name: string) => JSON.parse(readFileSync(join(spikes, 'fixtures', name), 'utf8')) as Fixture;

describe.each(['s5-mac-chrome.json', 'witness-desktop-chrome-headless.json'])('F5 code reproduces %s', (name) => {
  const fx = load(name);

  it('rebuilds every hash input and hash with the witness page code', async () => {
    expect(fx.entries).toHaveLength(7);
    for (const [i, e] of fx.entries.entries()) {
      expect(buildHashInput(bodyOf(e))).toBe(fx.hash_inputs[i]);
      expect(await hashEntryBody(bodyOf(e))).toBe(e.hash);
    }
  });

  it('accepts the whole chain with checkEntry (sequence, link, hash, signature)', async () => {
    const key = await importPublicJwk(fx.public_key_jwk);
    let prev = { seq: 0, hash: fx.genesis_hash };
    for (const e of fx.entries) {
      expect(await checkEntry(e, prev, key)).toEqual({ ok: true });
      prev = { seq: e.seq, hash: e.hash };
    }
  });

  it('rejects a tampered entry, a broken chain and a replayed seq', async () => {
    const key = await importPublicJwk(fx.public_key_jwk);
    const [e1, e2] = fx.entries as [WitnessEntry, WitnessEntry];
    const genesis = { seq: 0, hash: fx.genesis_hash };
    expect(await checkEntry({ ...e1, charging: !e1.charging }, genesis, key)).toEqual({ ok: false, problem: 'hash_mismatch' });
    expect(await checkEntry(e2, { seq: 1, hash: 'f'.repeat(64) }, key)).toEqual({ ok: false, problem: 'broken_chain' });
    expect(await checkEntry(e1, { seq: 1, hash: e1.hash }, key)).toEqual({ ok: false, problem: 'bad_sequence' });
    const otherSig = e2.sig;
    expect(await checkEntry({ ...e1, sig: otherSig }, genesis, key)).toEqual({ ok: false, problem: 'bad_signature' });
  });
});

const POLICY = fixturePolicy.id as PolicyId;
let clock = Date.parse('2026-11-12T10:00:00Z');
const now = () => new Date((clock += 1000));

async function setup(api: MockApi, opts: { online?: () => boolean } = {}) {
  const store = createMemoryStore();
  const keys = createMemoryKeyStore();
  const { record, keyPair } = await registerWitness(api, POLICY, keys, { store, now });
  const log = new WitnessLog(record, keyPair.privateKey, { store, now, online: opts.online ?? (() => true) });
  return { store, keys, log, keyPair };
}

describe('WitnessLog', () => {
  let api: MockApi;
  afterEach(() => api?.dispose());

  it('writes chained, signed entries that the mock verifies, and exports a fixture verify-fixture.cjs accepts', async () => {
    api = createMockApi({ latencyMs: 0 });
    const { log } = await setup(api);
    await log.append('heartbeat', { charging: true, battery_level: 0.29 }, 'real');
    await log.append('power_lost', { charging: false, battery_level: 0.83 }, 'real');
    await log.append('power_restored', { charging: true, battery_level: 1 }, 'real');
    await log.append('attest_lost', { charging: false, battery_level: null }, 'self-reported');
    await log.append('attest_restored', { charging: true, battery_level: null }, 'self-reported');
    await log.append('heartbeat', { charging: true, battery_level: 0.00001 }, 'test value');
    await log.append('heartbeat', { charging: true, battery_level: 0.1 + 0.2 }, 'test value');

    const out = await log.upload(api);
    expect(out).toEqual({ status: 'uploaded', accepted: 7 });
    expect(log.pending()).toHaveLength(0);
    expect(log.record.attested_charging).toBe(true);

    const fx = exportFixture(log.record, { userAgent: 'vitest (Node WebCrypto)', origin: 'test', generatedAt: '2026-11-12T00:00:00Z' });
    expect(fx.fixture_format).toBe('fourhour-witness-fixture/1');
    expect(fx.genesis_hash).toBe(GENESIS_HASH);
    expect(fx.entries.map((e) => e.seq)).toEqual([1, 2, 3, 4, 5, 6, 7]);
    expect(fx.entries.every((e) => /^[A-Za-z0-9_-]{86}$/.test(e.sig))).toBe(true);
    const dir = mkdtempSync(join(tmpdir(), 'fourhour-fx-'));
    const file = join(dir, 'fx.json');
    writeFileSync(file, JSON.stringify(fx));
    const report = execFileSync(process.execPath, [join(spikes, 'verify-fixture.cjs'), file], { encoding: 'utf8' });
    expect(report.trim().split('\n').at(-1)).toBe('ALL OK');
  });

  it('serialises concurrent appends: unique seq, unbroken chain', async () => {
    api = createMockApi({ latencyMs: 0 });
    const { log } = await setup(api);
    await Promise.all([1, 2, 3, 4, 5].map(() => log.append('heartbeat', { charging: true, battery_level: 0.5 }, 'n')));
    const es = log.record.entries.map((s) => s.entry);
    expect(es.map((e) => e.seq)).toEqual([1, 2, 3, 4, 5]);
    es.forEach((e, i) => expect(e.prev_hash).toBe(i === 0 ? GENESIS_HASH : es[i - 1]?.hash));
  });

  it('stores each entry before uploading, keeps logging offline, and uploads in order on reconnect', async () => {
    api = createMockApi({ latencyMs: 0 });
    let online = false;
    const { log, store } = await setup(api, { online: () => online });
    await log.append('heartbeat', { charging: true, battery_level: 0.5 }, 'n');
    await log.append('power_lost', { charging: false, battery_level: 0.5 }, 'n');
    expect(await log.upload(api)).toEqual({ status: 'offline' });
    // Persisted before any upload: a reload would find both.
    expect(loadRecord(store, POLICY)?.entries.map((s) => [s.entry.seq, s.uploaded])).toEqual([
      [1, false],
      [2, false],
    ]);
    // Browser says online but the request fails: still queued, error recorded.
    online = true;
    api.setOffline(true);
    await log.append('heartbeat', { charging: false, battery_level: 0.5 }, 'n');
    const failed = await log.upload(api);
    expect(failed.status).toBe('failed');
    expect(log.pending()).toHaveLength(3);
    expect(log.record.last_upload_error).toMatch(/Could not reach the backend/);
    // Back online: everything goes, in order.
    api.setOffline(false);
    await log.append('power_restored', { charging: true, battery_level: 0.5 }, 'n');
    expect(await log.upload(api)).toEqual({ status: 'uploaded', accepted: 4 });
    const server = await api.deviceEntries(log.record.device_id);
    expect(server.entries.map((e) => [e.seq, e.kind, e.valid])).toEqual([
      [1, 'heartbeat', true],
      [2, 'power_lost', true],
      [3, 'heartbeat', true],
      [4, 'power_restored', true],
    ]);
    expect(log.record.last_upload_error).toBeNull();
  });

  it('a resend after a lost response is accepted silently', async () => {
    api = createMockApi({ latencyMs: 0 });
    const { log } = await setup(api);
    const s = await log.append('heartbeat', { charging: true, battery_level: 0.5 }, 'n');
    await api.postEntries({ device_id: log.record.device_id, entries: [s.entry] }); // the "lost" response
    expect(await log.upload(api)).toEqual({ status: 'uploaded', accepted: 1 });
  });

  it('halts with a plain explanation when the server rejects an entry', async () => {
    api = createMockApi({ latencyMs: 0 });
    const { store } = await setup(api);
    const rec = loadRecord(store, POLICY);
    if (!rec) throw new Error('no record');
    const stranger = await generateWitnessKeys();
    const log = new WitnessLog(rec, stranger.privateKey, { store, now, online: () => true });
    await log.append('heartbeat', { charging: true, battery_level: 0.5 }, 'n');
    await log.append('heartbeat', { charging: true, battery_level: 0.5 }, 'n');
    const out = await log.upload(api);
    expect(out.status).toBe('halted');
    expect(log.record.entries[0]?.rejected).toBe('bad_signature');
    expect(log.record.halted).toMatch(/rejected entry 1 \(bad_signature\)/);
    expect(await log.upload(api)).toMatchObject({ status: 'halted' });
  });

  it('halts instead of retrying forever when the server does not know the device', async () => {
    api = createMockApi({ latencyMs: 0 });
    const { log } = await setup(api);
    await log.append('heartbeat', { charging: true, battery_level: 0.5 }, 'n');
    const other = createMockApi({ latencyMs: 0 }); // a fresh server: the device is unknown there
    const out = await log.upload(other);
    other.dispose();
    expect(out.status).toBe('halted');
    expect(log.record.halted).toMatch(/does not know this device/);
    expect(log.pending()).toHaveLength(1);
  });

  it('creates the private key non-extractable', async () => {
    const pair = await generateWitnessKeys();
    expect(pair.privateKey.extractable).toBe(false);
    await expect(crypto.subtle.exportKey('jwk', pair.privateKey)).rejects.toBeTruthy();
  });
});

describe('createSafeStore', () => {
  it('falls back to memory with a warning when storage is unavailable', () => {
    const s = createSafeStore(() => {
      throw new Error('SecurityError: access denied');
    });
    expect(s.persistent).toBe(false);
    expect(s.warning).toMatch(/access denied/);
    s.set('k', 'v');
    expect(s.get('k')).toBe('v');
  });

  it('falls back to memory when a write fails later (quota), keeping what was stored', () => {
    const backing = new Map<string, string>();
    let full = false;
    const fake = {
      get length() {
        return backing.size;
      },
      key: (i: number) => [...backing.keys()][i] ?? null,
      getItem: (k: string) => backing.get(k) ?? null,
      setItem: (k: string, v: string) => {
        if (full) throw new Error('QuotaExceededError');
        backing.set(k, v);
      },
      removeItem: (k: string) => void backing.delete(k),
      clear: () => backing.clear(),
    } satisfies Storage;
    const s = createSafeStore(() => fake);
    s.set('a', '1');
    expect(s.persistent).toBe(true);
    full = true;
    s.set('b', '2');
    expect(s.persistent).toBe(false);
    expect(s.warning).toMatch(/Quota/);
    expect([s.get('a'), s.get('b')]).toEqual(['1', '2']);
  });
});
