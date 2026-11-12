#!/usr/bin/env node
// Drives a running FourHour backend the way a judge would, without a browser.
//
//   node scripts/judge-e2e.mjs <api-base-url> [derecho|spoof] [--speed 3600] [--timeout 600]
//
//   <api-base-url>  e.g. https://fourhour-api.onrender.com/api  or  http://localhost:8080/api
//   derecho         (default) start derecho-harris-2024, report power_lost once the index
//                   crosses theta_lo, wait for an R3 claim and a confirmed payout.
//                   THIS SENDS A REAL SANDBOX PAYOUT.
//   spoof           start spoof-quiet, report power_lost at once, wait for an R5 claim and
//                   check that no payout is created for it.
//
// The witness is a fresh ECDSA P-256 key made with WebCrypto, and entries are hashed and
// signed exactly as CONTRACTS.md section 6 and the browser witness page do.
// Needs Node 20+ (global fetch and WebCrypto). Exits 0 on pass, 1 on fail.

const { subtle } = globalThis.crypto;

const args = process.argv.slice(2);
const base = (args.find((a) => /^https?:\/\//.test(a)) || '').replace(/\/+$/, '');
const mode = args.includes('spoof') ? 'spoof' : 'derecho';
const opt = (name, dflt) => {
  const i = args.indexOf(name);
  return i >= 0 ? Number(args[i + 1]) : dflt;
};
const speed = opt('--speed', 3600);
const timeoutS = opt('--timeout', 600);
if (!base) {
  console.error('usage: node scripts/judge-e2e.mjs <api-base-url ending in /api> [derecho|spoof]');
  process.exit(2);
}

const t0 = Date.now();
const log = (...m) => console.log(`[${((Date.now() - t0) / 1000).toFixed(1).padStart(6)}s]`, ...m);
const fail = (msg) => {
  log('FAIL:', msg);
  process.exit(1);
};

async function api(method, path, body) {
  const res = await fetch(base + path, {
    method,
    headers: body ? { 'content-type': 'application/json' } : {},
    body: body ? JSON.stringify(body) : undefined,
    signal: AbortSignal.timeout(90_000), // the free host can take a minute to wake
  });
  const text = await res.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    json = { raw: text.slice(0, 300) };
  }
  if (!res.ok) throw new Error(`${method} ${path} -> ${res.status} ${JSON.stringify(json)}`);
  return json;
}

const b64url = (buf) => Buffer.from(buf).toString('base64url');
const hex = (buf) => Buffer.from(buf).toString('hex');

// ---- witness -------------------------------------------------------------
class Witness {
  async init(policyId) {
    this.keys = await subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, true, ['sign', 'verify']);
    const jwk = await subtle.exportKey('jwk', this.keys.publicKey);
    const r = await api('POST', '/witness/devices', {
      policy_id: policyId,
      public_key_jwk: { kty: jwk.kty, crv: jwk.crv, x: jwk.x, y: jwk.y },
    });
    this.deviceId = r.device_id;
    this.prevHash = r.genesis_hash;
    this.seq = 0;
    this.charging = true;
    return this.deviceId;
  }

  async send(kind) {
    if (kind === 'power_lost') this.charging = false;
    if (kind === 'power_restored') this.charging = true;
    const seq = this.seq + 1;
    const deviceTime = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z');
    // Field order and number formatting follow CONTRACTS.md section 6 (JSON.stringify).
    const canonical = JSON.stringify({
      seq,
      device_time: deviceTime,
      kind,
      charging: this.charging,
      battery_level: null,
      online: true,
      prev_hash: this.prevHash,
    });
    const hash = hex(await subtle.digest('SHA-256', new TextEncoder().encode(canonical)));
    const sig = b64url(
      await subtle.sign({ name: 'ECDSA', hash: 'SHA-256' }, this.keys.privateKey, new TextEncoder().encode(hash)),
    );
    const entry = JSON.parse(canonical);
    Object.assign(entry, { hash, sig });
    const r = await api('POST', '/witness/entries', { device_id: this.deviceId, entries: [entry] });
    if (r.accepted !== 1) throw new Error(`entry ${seq} not accepted: ${JSON.stringify(r.rejected)}`);
    this.seq = seq;
    this.prevHash = hash;
    return r;
  }
}

// ---- event stream --------------------------------------------------------
async function* sse(url, signal) {
  const res = await fetch(url, { headers: { accept: 'text/event-stream' }, signal });
  if (!res.ok) throw new Error(`stream -> ${res.status}`);
  const dec = new TextDecoder();
  let buf = '';
  for await (const chunk of res.body) {
    buf += dec.decode(chunk, { stream: true });
    let i;
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const block = buf.slice(0, i);
      buf = buf.slice(i + 2);
      let event = 'message';
      const data = [];
      for (const line of block.split('\n')) {
        if (line.startsWith('event:')) event = line.slice(6).trim();
        else if (line.startsWith('data:')) data.push(line.slice(5).trimStart());
      }
      if (!data.length) continue;
      try {
        yield { event, data: JSON.parse(data.join('\n')) };
      } catch {
        /* ignore malformed */
      }
    }
  }
}

// ---- main ----------------------------------------------------------------
async function main() {
  log(`base ${base}, mode ${mode}, speed ${speed}`);
  const health = await api('GET', '/health');
  log('health:', JSON.stringify(health));
  if (health.paypal_env !== 'sandbox') fail('backend is not in sandbox mode');

  const trig = await api('GET', '/analytics/trigger');
  const thetaLo = trig.theta_lo;
  if (typeof thetaLo !== 'number') fail('theta_lo not available from /analytics/trigger');
  log(`theta_lo ${thetaLo} (from trigger_params.json)`);

  const demo = await api('GET', '/demo');
  if (!demo.policy_id) fail('no demo policy (DEMO_VAULT_TOKEN not set on the backend?)');
  const policyId = demo.policy_id;
  const pol = await api('GET', `/policies/${policyId}`);
  log(`demo policy ${policyId}: status ${pol.policy.status}, cover ${demo.cover_cents} cents, fips ${demo.fips}`);

  const w = new Witness();
  const deviceId = await w.init(policyId);
  log(`registered witness device ${deviceId}`);
  await w.send('heartbeat');

  const scenario = mode === 'spoof' ? 'spoof-quiet' : 'derecho-harris-2024';
  const ctrl = new AbortController();
  const deadline = setTimeout(() => ctrl.abort(), timeoutS * 1000);
  const stream = sse(base + '/replay/stream', ctrl.signal);

  // Wait for the snapshot so no event is missed, then start the replay.
  const first = await stream.next();
  if (first.value?.event !== 'snapshot') log('warning: first stream event was', first.value?.event);
  const started = await api('POST', '/replay/start', { scenario_id: scenario, speed });
  log(`started ${scenario}: replay ${started.replay_id}, ${started.sim_start} -> ${started.sim_end}`);

  let lost = false;
  let claim = null;
  let payout = null;
  let ended = false;
  let endedAt = 0;

  try {
    for await (const { event, data } of stream) {
      if (event === 'tick') {
        const crossed = typeof data.share === 'number' && data.share >= thetaLo;
        if (!lost && (mode === 'spoof' || crossed)) {
          await w.send('power_lost');
          lost = true;
          log(`sim ${data.sim_time}: share ${data.share} -> sent signed power_lost (seq ${w.seq})`);
        }
      } else if (event === 'event_opened' || event === 'event_closed') {
        log(`sim ${data.sim_time}: ${event} ${data.outage_event_id} (${data.threshold})${data.duration_hours != null ? ` ${data.duration_hours} h` : ''}`);
      } else if (event === 'witness_entry' && data.device_id === deviceId) {
        log(`sim ${data.sim_time}: witness_entry ${data.kind} valid=${data.valid}`);
      } else if (event === 'claim_decided' && data.claim?.policy_id === policyId) {
        const c = data.claim;
        log(`sim ${data.sim_time}: claim_decided ${c.id} rule ${c.rule} ${c.decision} ${c.amount_cents} cents`);
        if (!claim) claim = c;
      } else if ((event === 'payout_sent' || event === 'payout_confirmed') && claim && data.payout?.claim_id === claim.id) {
        payout = data.payout;
        log(`${event}: ${payout.id} status ${payout.status} batch ${payout.paypal_batch_id} item ${payout.paypal_item_id} via ${payout.confirmed_via}`);
        if (event === 'payout_confirmed') break;
      } else if (event === 'replay_ended') {
        ended = true;
        endedAt = Date.now();
        log('replay_ended');
        if (mode === 'derecho' && !claim) break;
      }
      // Spoof: after an R5 claim, watch 30 more seconds for any payout, then stop.
      if (mode === 'spoof' && claim && (ended || Date.now() - Date.parse(claim.decided_at) > 30_000)) {
        if (!endedAt) endedAt = Date.now();
        if (Date.now() - endedAt > 30_000 || ended) break;
      }
    }
  } catch (e) {
    if (e.name !== 'AbortError') throw e;
    log('stream timed out');
  } finally {
    clearTimeout(deadline);
    ctrl.abort();
  }

  // Close the witness interval so the device log is tidy.
  if (lost) await w.send('power_restored').catch(() => {});

  if (!claim) fail('no claim was decided for the demo policy');
  const full = await api('GET', `/claims/${claim.id}`);
  const p = full.claim.payout;
  console.log('\nRESULT');
  console.log(JSON.stringify({
    scenario,
    policy_id: policyId,
    device_id: deviceId,
    claim_id: claim.id,
    rule: full.claim.rule,
    decision: full.claim.decision,
    amount_cents: full.claim.amount_cents,
    payout: p && {
      id: p.id,
      status: p.status,
      paypal_batch_id: p.paypal_batch_id,
      paypal_item_id: p.paypal_item_id,
      confirmed_via: p.confirmed_via,
      confirmed_at: p.confirmed_at,
    },
  }, null, 2));

  if (mode === 'derecho') {
    if (full.claim.rule !== 'R3') fail(`expected rule R3, got ${full.claim.rule}`);
    if (!p || p.status !== 'success') fail(`payout not confirmed (status ${p?.status})`);
    log(`PASS: R3 claim ${claim.id}, payout confirmed via ${p.confirmed_via}`);
  } else {
    if (full.claim.rule !== 'R5') fail(`expected rule R5, got ${full.claim.rule}`);
    if (p) fail('an R5 claim has a payout');
    log(`PASS: R5 claim ${claim.id}, no payout`);
  }
}

main().catch((e) => fail(e.message));
