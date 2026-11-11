// Checks a FourHour witness fixture (the JSON from s5-phone-witness.html) without a browser.
// Rebuilds each hash input field by field, re-hashes it, checks the chain from genesis_hash,
// and verifies each raw r||s signature with Node's OpenSSL-backed crypto (not WebCrypto).
// Usage: node frontend/spikes/verify-fixture.cjs path/to/results.json   (Node 20+, no dependencies)
// Prints one line per entry and ALL OK or FAILED.
// Note: String(number) here matches JSON.stringify for numbers; the Go backend needs its own builder.
const crypto = require('crypto'); const fs = require('fs');
const f = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const key = crypto.createPublicKey({ key: f.public_key_jwk, format: 'jwk' });
let prev = f.genesis_hash, ok = true;
f.entries.forEach((e, i) => {
  // Explicit builder, field by field, as the backend must do.
  const lvl = e.battery_level === null ? 'null' : String(e.battery_level);
  const input = `{"seq":${e.seq},"device_time":${JSON.stringify(e.device_time)},"kind":${JSON.stringify(e.kind)},"charging":${e.charging},"battery_level":${lvl},"online":${e.online},"prev_hash":${JSON.stringify(e.prev_hash)}}`;
  const h = crypto.createHash('sha256').update(input, 'utf8').digest('hex');
  const sig = Buffer.from(e.sig, 'base64url');
  const v = crypto.verify('sha256', Buffer.from(e.hash, 'utf8'), { key, dsaEncoding: 'ieee-p1363' }, sig);
  const r = { seq: e.seq, input_matches: input === f.hash_inputs[i], hash_matches: h === e.hash, chain: e.prev_hash === prev, sig_len: sig.length, sig_valid: v };
  ok = ok && r.input_matches && r.hash_matches && r.chain && r.sig_len === 64 && v; prev = e.hash; console.log(JSON.stringify(r));
});
const t = f.entries[1]; const bad = crypto.verify('sha256', Buffer.from(t.hash.replace(/^./, c => c === 'a' ? 'b' : 'a'), 'utf8'), { key, dsaEncoding: 'ieee-p1363' }, Buffer.from(t.sig, 'base64url'));
console.log('tampered hash verifies:', bad); console.log(ok && !bad ? 'ALL OK' : 'FAILED');
