// @vitest-environment node
/**
 * The hash-input builder against entries produced by real browser WebCrypto
 * (frontend/spikes/fixtures, made by the S5 page), plus the battery_level cases from section 6.
 */
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import type { PublicKeyJwk, WitnessEntry } from '../api/types';
import { GENESIS_HASH, base64UrlDecode, buildHashInput, isoSeconds, sha256Hex } from '../lib/witnessHash';

interface Fixture {
  fixture_format: string;
  public_key_jwk: PublicKeyJwk;
  genesis_hash: string;
  entries: WitnessEntry[];
  hash_inputs: string[];
}

const fixture = JSON.parse(
  readFileSync(new URL('../../spikes/fixtures/witness-desktop-chrome-headless.json', import.meta.url), 'utf8'),
) as Fixture;

describe('buildHashInput', () => {
  it('matches the CONTRACTS.md section 6 field order and has no spaces', () => {
    const s = buildHashInput({
      seq: 12,
      device_time: '2026-11-12T05:31:07Z',
      kind: 'power_lost',
      charging: false,
      battery_level: 0.83,
      online: true,
      prev_hash: 'ab',
    });
    expect(s).toBe(
      '{"seq":12,"device_time":"2026-11-12T05:31:07Z","kind":"power_lost","charging":false,"battery_level":0.83,"online":true,"prev_hash":"ab"}',
    );
  });

  it.each([
    [1, '1'],
    [0.5, '0.5'],
    [0.83, '0.83'],
    [null, 'null'],
    [0.00001, '0.00001'],
    [0.1 + 0.2, '0.30000000000000004'],
  ])('writes battery_level %s as %s', (level, text) => {
    const s = buildHashInput({ seq: 1, device_time: 'x', kind: 'heartbeat', charging: true, battery_level: level, online: false, prev_hash: GENESIS_HASH });
    expect(s).toContain(`"battery_level":${text},`);
  });

  it('formats device_time without milliseconds', () => {
    expect(isoSeconds(new Date('2026-11-12T05:31:07.123Z'))).toBe('2026-11-12T05:31:07Z');
  });
});

describe('browser fixture', () => {
  it('has the expected format', () => {
    expect(fixture.fixture_format).toBe('fourhour-witness-fixture/1');
    expect(fixture.genesis_hash).toBe(GENESIS_HASH);
    expect(fixture.entries.length).toBe(fixture.hash_inputs.length);
  });

  it('rebuilds every hash input, hash and chain link', async () => {
    let prev = fixture.genesis_hash;
    for (const [i, e] of fixture.entries.entries()) {
      expect(e.seq).toBe(i + 1);
      expect(e.prev_hash).toBe(prev);
      expect(buildHashInput(e)).toBe(fixture.hash_inputs[i]);
      expect(await sha256Hex(buildHashInput(e))).toBe(e.hash);
      prev = e.hash;
    }
  });

  it('verifies every signature as raw r||s over the UTF-8 hash hex, and rejects a tampered hash', async () => {
    const key = await crypto.subtle.importKey('jwk', fixture.public_key_jwk, { name: 'ECDSA', namedCurve: 'P-256' }, false, ['verify']);
    const alg = { name: 'ECDSA', hash: 'SHA-256' };
    for (const e of fixture.entries) {
      const sig = base64UrlDecode(e.sig);
      expect(sig.byteLength).toBe(64);
      expect(e.sig).not.toMatch(/=/);
      expect(await crypto.subtle.verify(alg, key, sig, new TextEncoder().encode(e.hash))).toBe(true);
    }
    const first = fixture.entries[0] as WitnessEntry;
    const tampered = (first.hash[0] === 'a' ? 'b' : 'a') + first.hash.slice(1);
    expect(await crypto.subtle.verify(alg, key, base64UrlDecode(first.sig), new TextEncoder().encode(tampered))).toBe(false);
  });
});
