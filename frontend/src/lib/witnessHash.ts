/**
 * Hash input and hash for a witness entry, exactly as CONTRACTS.md section 6 specifies:
 * SHA-256, lowercase hex, of the UTF-8 bytes of the entry's fields in contract order, no spaces,
 * numbers as JSON.stringify writes them. JSON.stringify keeps insertion order, so the object
 * literal below fixes the field order.
 */
import type { WitnessEntryBody } from '../api/types';

export const GENESIS_HASH = '0'.repeat(64);

export function buildHashInput(e: WitnessEntryBody): string {
  return JSON.stringify({
    seq: e.seq,
    device_time: e.device_time,
    kind: e.kind,
    charging: e.charging,
    battery_level: e.battery_level,
    online: e.online,
    prev_hash: e.prev_hash,
  });
}

export function toHex(buf: ArrayBuffer): string {
  return Array.from(new Uint8Array(buf), (b) => b.toString(16).padStart(2, '0')).join('');
}

export async function sha256Hex(text: string, subtle: SubtleCrypto = crypto.subtle): Promise<string> {
  return toHex(await subtle.digest('SHA-256', new TextEncoder().encode(text)));
}

export function base64UrlEncode(buf: ArrayBuffer): string {
  let s = '';
  for (const b of new Uint8Array(buf)) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export function base64UrlDecode(s: string): Uint8Array<ArrayBuffer> {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (s.length % 4)) % 4);
  const bin = atob(b64);
  const out = new Uint8Array(new ArrayBuffer(bin.length));
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

/** ISO 8601 UTC with Z and no milliseconds, as in the CONTRACTS.md examples. */
export function isoSeconds(d: Date = new Date()): string {
  return d.toISOString().replace(/\.\d{3}Z$/, 'Z');
}
