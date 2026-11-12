/**
 * Building and checking one witness entry (CONTRACTS.md section 6). The hash input comes from
 * buildHashInput in lib/witnessHash.ts, the same builder the S5 spike fixtures were checked with.
 *
 * Signature: ECDSA P-256 with SHA-256 over the UTF-8 bytes of the hash hex string. WebCrypto
 * returns 64 raw bytes (r then s); they are sent base64url without padding.
 */
import type { PublicKeyJwk, WitnessEntry, WitnessEntryBody } from '../api/types';
import { base64UrlDecode, base64UrlEncode, buildHashInput, sha256Hex } from '../lib/witnessHash';

export const ECDSA_KEY = { name: 'ECDSA', namedCurve: 'P-256' } as const;
export const ECDSA_SIGN = { name: 'ECDSA', hash: 'SHA-256' } as const;

const utf8 = (s: string) => new TextEncoder().encode(s);

export function bodyOf(e: WitnessEntryBody): WitnessEntryBody {
  return {
    seq: e.seq,
    device_time: e.device_time,
    kind: e.kind,
    charging: e.charging,
    battery_level: e.battery_level,
    online: e.online,
    prev_hash: e.prev_hash,
  };
}

export function hashEntryBody(body: WitnessEntryBody, subtle: SubtleCrypto = crypto.subtle): Promise<string> {
  return sha256Hex(buildHashInput(body), subtle);
}

export async function createEntry(body: WitnessEntryBody, privateKey: CryptoKey, subtle: SubtleCrypto = crypto.subtle): Promise<WitnessEntry> {
  const b = bodyOf(body);
  const hash = await hashEntryBody(b, subtle);
  const sig = await subtle.sign(ECDSA_SIGN, privateKey, utf8(hash));
  if (sig.byteLength !== 64) throw new Error(`Expected a 64-byte raw signature, got ${sig.byteLength} bytes`);
  return { ...b, hash, sig: base64UrlEncode(sig) };
}

/** A new key pair. The private key is non-extractable: it can sign but never leave the device. */
export async function generateWitnessKeys(subtle: SubtleCrypto = crypto.subtle): Promise<CryptoKeyPair> {
  return subtle.generateKey(ECDSA_KEY, false, ['sign', 'verify']);
}

/** The public key as the contract's JWK (kty, crv, x, y only). */
export async function exportPublicJwk(publicKey: CryptoKey, subtle: SubtleCrypto = crypto.subtle): Promise<PublicKeyJwk> {
  const jwk = await subtle.exportKey('jwk', publicKey);
  if (jwk.kty !== 'EC' || jwk.crv !== 'P-256' || !jwk.x || !jwk.y) throw new Error('Unexpected public key format');
  return { kty: 'EC', crv: 'P-256', x: jwk.x, y: jwk.y };
}

export function importPublicJwk(jwk: PublicKeyJwk, subtle: SubtleCrypto = crypto.subtle): Promise<CryptoKey> {
  return subtle.importKey('jwk', { ...jwk, ext: true }, ECDSA_KEY, true, ['verify']);
}

export type EntryCheck = { ok: true } | { ok: false; problem: 'hash_mismatch' | 'bad_signature' | 'broken_chain' | 'bad_sequence' };

/** Checks one entry against the previous one: sequence, chain link, hash and signature. */
export async function checkEntry(
  entry: WitnessEntry,
  prev: { seq: number; hash: string },
  publicKey: CryptoKey,
  subtle: SubtleCrypto = crypto.subtle,
): Promise<EntryCheck> {
  if (entry.seq !== prev.seq + 1) return { ok: false, problem: 'bad_sequence' };
  if (entry.prev_hash !== prev.hash) return { ok: false, problem: 'broken_chain' };
  if ((await hashEntryBody(entry, subtle)) !== entry.hash) return { ok: false, problem: 'hash_mismatch' };
  let sig: Uint8Array<ArrayBuffer>;
  try {
    sig = base64UrlDecode(entry.sig);
  } catch {
    return { ok: false, problem: 'bad_signature' };
  }
  if (sig.byteLength !== 64) return { ok: false, problem: 'bad_signature' };
  const ok = await subtle.verify(ECDSA_SIGN, publicKey, sig, utf8(entry.hash));
  return ok ? { ok: true } : { ok: false, problem: 'bad_signature' };
}
