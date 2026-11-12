#!/usr/bin/env node
// Records a 1920x1080 tour of the hosted app that follows the video script:
// home -> enrol quote -> demo policy -> live derecho replay with the witness -> claim ->
// spoof-quiet replay (R5) -> evidence. The witness tab is recorded as a second clip.
//
//   NODE_PATH=<dir with playwright> node scripts/record-tour.mjs <frontend-url> <api-url>/api [--out docs/footage]
//
// SENDS ONE REAL SANDBOX PAYOUT ($30.00). Every wait has a hard timeout; pages load with
// waitUntil "domcontentloaded" so the open event stream can never hang the run. Writes
// tour-timeline.json next to the videos with the second at which each scene starts.

import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';

const require = createRequire(path.join(process.env.NODE_PATH || process.cwd(), 'noop.js'));
const { chromium } = require('playwright');

const args = process.argv.slice(2);
const urls = args.filter((a) => /^https?:\/\//.test(a));
const web = (urls[0] || '').replace(/\/+$/, '');
const api = (urls[1] || '').replace(/\/+$/, '');
const out = args.includes('--out') ? args[args.indexOf('--out') + 1] : 'docs/footage';
if (!web || !api) {
  console.error('usage: node scripts/record-tour.mjs <frontend-url> <api-url>/api [--out dir]');
  process.exit(2);
}
const killer = setTimeout(() => {
  console.error('hard limit reached (720 s), exiting');
  process.exit(1);
}, 720_000);

const VIEW = { width: 1920, height: 1080 };
const T0 = Date.now();
const log = (...m) => console.log(`[${((Date.now() - T0) / 1000).toFixed(1).padStart(6)}s]`, ...m);
fs.mkdirSync(out, { recursive: true });

const getJSON = async (p, init) => {
  const r = await fetch(api + p, { signal: AbortSignal.timeout(90_000), ...init });
  if (!r.ok) throw new Error(`${p} -> ${r.status}`);
  return r.json();
};

async function* sse(url, signal) {
  const res = await fetch(url, { headers: { accept: 'text/event-stream' }, signal });
  const dec = new TextDecoder();
  let buf = '';
  for await (const chunk of res.body) {
    buf += dec.decode(chunk, { stream: true });
    let i;
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const block = buf.slice(0, i);
      buf = buf.slice(i + 2);
      let event = 'message';
      let data = '';
      for (const line of block.split('\n')) {
        if (line.startsWith('event:')) event = line.slice(6).trim();
        else if (line.startsWith('data:')) data += line.slice(5).trimStart();
      }
      if (data) {
        try {
          yield { event, data: JSON.parse(data) };
        } catch {}
      }
    }
  }
}

// Follows the stream until `done(event, data)` returns true or the timeout passes.
async function follow(onEvent, timeoutMs) {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  try {
    for await (const { event, data } of sse(api + '/replay/stream', ctrl.signal)) {
      if (await onEvent(event, data)) return true;
    }
  } catch (e) {
    if (e.name !== 'AbortError') throw e;
  } finally {
    clearTimeout(timer);
    ctrl.abort();
  }
  return false;
}

async function main() {
  log('health', JSON.stringify(await getJSON('/health')));
  const demo = await getJSON('/demo');
  if (!demo.policy_id) throw new Error('no demo policy');
  const policyId = demo.policy_id;
  const thetaLo = (await getJSON('/analytics/trigger')).theta_lo;
  await getJSON('/replay/reset', { method: 'POST' });

  const browser = await chromium.launch({ channel: 'chrome', headless: true });

  // Witness: its own context, like a second device, recorded as a separate clip.
  const wctx = await browser.newContext({ viewport: { width: 960, height: 1080 }, recordVideo: { dir: out, size: { width: 960, height: 1080 } } });
  const wpage = await wctx.newPage();
  wpage.setDefaultTimeout(30_000);
  await wpage.goto(`${web}/witness/${policyId}`, { waitUntil: 'domcontentloaded', timeout: 90_000 });
  await wpage.getByRole('button', { name: /set up this device as the witness/i }).click();
  await wpage.getByRole('button', { name: /power is out/i }).waitFor();
  log('witness registered');

  const ctx = await browser.newContext({ viewport: VIEW, recordVideo: { dir: out, size: VIEW } });
  const page = await ctx.newPage();
  page.setDefaultTimeout(30_000);
  const vt0 = Date.now();
  const timeline = [];
  const mark = (scene) => {
    const t = Math.round((Date.now() - vt0) / 100) / 10;
    timeline.push({ t, scene });
    log(`scene @${t}s: ${scene}`);
  };
  const go = async (route, waitMs = 3500) => {
    await page.goto(web + route, { waitUntil: 'domcontentloaded', timeout: 90_000 });
    await page.evaluate(() => document.fonts && document.fonts.ready).catch(() => {});
    await page.waitForTimeout(waitMs);
  };
  const scrollTo = async (y, waitMs = 2500) => {
    await page.evaluate((top) => window.scrollTo({ top, behavior: 'smooth' }), y);
    await page.waitForTimeout(waitMs);
  };
  const scrollToEl = async (selector, offset = 24, waitMs = 2500) => {
    const y = await page.evaluate(
      ([sel, off]) => {
        const el = document.querySelector(sel);
        return el ? Math.max(0, el.getBoundingClientRect().top + window.scrollY - off) : 0;
      },
      [selector, offset],
    );
    await scrollTo(y, waitMs);
  };

  // 1. Home
  mark('home');
  await go('/', 5000);
  await scrollTo(700, 4000);
  await scrollTo(0, 1500);

  // 2. Enrol: Harris County, $50, quote breakdown
  mark('enrol-quote');
  await go('/enrol', 3000);
  await page.selectOption('#county', '48201').catch(() => {});
  await page.waitForTimeout(1500);
  await page.locator('input[name="cover"]').first().check().catch(() => {});
  await page.waitForTimeout(3000);
  await scrollTo(450, 5000);

  // 3. Demo policy: active, first premium charged
  mark('policy-premium');
  await go(`/policies/${policyId}`, 5000);
  await scrollTo(500, 4000);

  // 4. Live: derecho with the witness
  mark('live-derecho-start');
  await go('/live', 3000);
  await page.selectOption('#scenario', 'derecho-harris-2024');
  await page.waitForTimeout(1500);
  let claim = null;
  let payout = null;
  let pressed = false;
  const streamDone = follow(async (event, data) => {
    if (event === 'tick' && !pressed && typeof data.share === 'number' && data.share >= thetaLo) {
      pressed = true;
      await wpage.getByRole('button', { name: /power is out/i }).click();
      mark('witness-power-out');
      log(`sim ${data.sim_time} share ${data.share}`);
    } else if (event === 'claim_decided' && data.claim?.policy_id === policyId && !claim) {
      claim = data.claim;
      mark(`claim-${claim.rule}`);
    } else if (event === 'payout_sent' && claim && data.payout?.claim_id === claim.id) {
      mark('payout-sending');
    } else if (event === 'payout_confirmed' && claim && data.payout?.claim_id === claim.id) {
      payout = data.payout;
      mark('payout-confirmed');
      return true;
    } else if (event === 'replay_ended' && !claim) return true;
    return false;
  }, 300_000);
  await page.waitForTimeout(400);
  await page.getByRole('button', { name: /^(start|restart) replay$/i }).click();
  await page.waitForTimeout(1200);
  await scrollToEl('#chart-h', 140, 1500);
  await streamDone;
  await page.waitForTimeout(7000);
  if (!claim || !payout) throw new Error('derecho: no confirmed payout');

  // 5. Claim explained
  mark('claim-page');
  await go(`/claims/${claim.id}`, 5000);
  await scrollTo(600, 4000);
  await scrollTo(1300, 4000);

  // 6. Spoof: quiet week, witness reports an outage, R5, no payout
  mark('live-spoof-start');
  await wpage.getByRole('button', { name: /power is back/i }).click();
  await go('/live', 2500);
  if (await page.getByRole('button', { name: /^stop$/i }).isEnabled().catch(() => false)) {
    await page.getByRole('button', { name: /^stop$/i }).click();
    await page.waitForTimeout(1500);
  }
  await page.selectOption('#scenario', 'spoof-quiet');
  await page.waitForTimeout(1500);
  let spoof = null;
  const spoofDone = follow(async (event, data) => {
    if (event === 'tick' && !pressed) {
      pressed = true;
      await wpage.getByRole('button', { name: /power is out/i }).click();
      mark('witness-power-out-quiet');
    } else if (event === 'claim_decided' && data.claim?.policy_id === policyId && data.claim.rule === 'R5') {
      spoof = data.claim;
      mark('claim-R5');
      return true;
    }
    return false;
  }, 120_000);
  pressed = false;
  await page.getByRole('button', { name: /^(start|restart) replay$/i }).click();
  await page.waitForTimeout(1200);
  await scrollToEl('#chart-h', 140, 1500);
  await spoofDone;
  await page.waitForTimeout(7000);

  // 7. Evidence: basis risk first, then the rest
  mark('evidence');
  await go('/evidence', 6000);
  await scrollTo(500, 6000);
  await scrollTo(1300, 5000);
  mark('end');

  const video = page.video();
  const wvideo = wpage.video();
  await ctx.close();
  await wpage.getByRole('button', { name: /power is back/i }).click().catch(() => {});
  await wctx.close();
  await getJSON('/replay/reset', { method: 'POST' }).catch(() => {});

  // Save before closing the browser: saveAs needs the browser connection.
  const main = path.join(out, 'fourhour-tour-1080p.webm');
  const wit = path.join(out, 'fourhour-witness-tab.webm');
  await video.saveAs(main);
  await video.delete().catch(() => {});
  await wvideo.saveAs(wit);
  await wvideo.delete().catch(() => {});
  await browser.close();
  const result = {
    recorded_at: new Date().toISOString(),
    site: web,
    policy_id: policyId,
    derecho_claim: { id: claim.id, rule: claim.rule, amount_cents: claim.amount_cents },
    payout: payout && { id: payout.id, paypal_batch_id: payout.paypal_batch_id, paypal_item_id: payout.paypal_item_id, confirmed_via: payout.confirmed_via },
    spoof_claim: spoof && { id: spoof.id, rule: spoof.rule, decision: spoof.decision },
    timeline,
  };
  fs.writeFileSync(path.join(out, 'tour-timeline.json'), JSON.stringify(result, null, 2) + '\n');
  log('saved', main, wit);
  console.log(JSON.stringify(result, null, 2));
  clearTimeout(killer);
}

main().catch((e) => {
  console.error('FAIL', e.message);
  process.exit(1);
});
