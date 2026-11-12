#!/usr/bin/env node
// Screenshots every route and records the judge path on /live, at 1920x1080.
//
//   NODE_PATH=<dir with playwright installed> node scripts/capture-screens.mjs \
//       <frontend-url> <api-base-url ending in /api> [--video derecho|spoof|none] [--out docs]
//
// Needs `playwright` (npm i playwright; `npx playwright install ffmpeg` for video) and Google
// Chrome. Playwright is not a project dependency; install it outside the repo.
// Every wait has a hard timeout so the open event stream on /live can never hang the run:
// pages are loaded with waitUntil "domcontentloaded" (never "networkidle"), and the whole
// script is killed after --limit seconds (default 600).
//
// --video derecho records the real judge path and SENDS ONE REAL SANDBOX PAYOUT.

import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';

const require = createRequire(path.join(process.env.NODE_PATH || process.cwd(), 'noop.js'));
const { chromium } = require('playwright');

const args = process.argv.slice(2);
const urls = args.filter((a) => /^https?:\/\//.test(a));
const web = (urls[0] || '').replace(/\/+$/, '');
const api = (urls[1] || '').replace(/\/+$/, '');
const opt = (n, d) => (args.includes(n) ? args[args.indexOf(n) + 1] : d);
const videoMode = opt('--video', 'none');
const out = opt('--out', 'docs');
const limit = Number(opt('--limit', 600));
if (!web || !api) {
  console.error('usage: node scripts/capture-screens.mjs <frontend-url> <api-url>/api [--video derecho|spoof|none]');
  process.exit(2);
}
const killer = setTimeout(() => {
  console.error(`hard limit ${limit}s reached, exiting`);
  process.exit(1);
}, limit * 1000);

const t0 = Date.now();
const log = (...m) => console.log(`[${((Date.now() - t0) / 1000).toFixed(1).padStart(6)}s]`, ...m);
const VIEW = { width: 1920, height: 1080 };
const screensDir = path.join(out, 'screens');
const footageDir = path.join(out, 'footage');
fs.mkdirSync(screensDir, { recursive: true });

const getJSON = async (p) => {
  const r = await fetch(api + p, { signal: AbortSignal.timeout(90_000) });
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

async function settle(page, ms = 2500) {
  // Wait for fonts and a quiet moment; never for network idle.
  await page.evaluate(() => document.fonts && document.fonts.ready).catch(() => {});
  await page.waitForTimeout(ms);
}

async function recordJudgePath(browser, policyId, mode) {
  fs.mkdirSync(footageDir, { recursive: true });
  const scenario = mode === 'spoof' ? 'spoof-quiet' : 'derecho-harris-2024';
  const thetaLo = (await getJSON('/analytics/trigger')).theta_lo;

  // The witness runs in its own (unrecorded) context, like a second device.
  const wctx = await browser.newContext({ viewport: { width: 900, height: 1000 } });
  const wpage = await wctx.newPage();
  wpage.setDefaultTimeout(30_000);
  await wpage.goto(`${web}/witness/${policyId}`, { waitUntil: 'domcontentloaded', timeout: 90_000 });
  await wpage.getByRole('button', { name: /set up this device as the witness/i }).click();
  await wpage.getByRole('button', { name: /power is out/i }).waitFor({ timeout: 30_000 });
  log('witness registered');

  const ctx = await browser.newContext({ viewport: VIEW, recordVideo: { dir: footageDir, size: VIEW } });
  const page = await ctx.newPage();
  page.setDefaultTimeout(30_000);
  await page.goto(`${web}/live`, { waitUntil: 'domcontentloaded', timeout: 90_000 });
  await page.locator('#scenario').waitFor();
  await page.selectOption('#scenario', scenario);
  await settle(page, 2000);

  const ctrl = new AbortController();
  const stream = sse(api + '/replay/stream', ctrl.signal);
  await stream.next(); // snapshot
  await page.getByRole('button', { name: /^(start|restart) replay$/i }).click();
  log(`started ${scenario} from the Live page`);

  let pressed = false;
  let claim = null;
  let payout = null;
  const until = Date.now() + 420_000;
  try {
    for await (const { event, data } of stream) {
      if (Date.now() > until) break;
      if (event === 'tick' && !pressed && (mode === 'spoof' || (typeof data.share === 'number' && data.share >= thetaLo))) {
        await wpage.getByRole('button', { name: /power is out/i }).click();
        pressed = true;
        log(`sim ${data.sim_time} share ${data.share}: pressed "Power is out"`);
      } else if (event === 'claim_decided' && data.claim?.policy_id === policyId && !claim) {
        claim = data.claim;
        log(`claim ${claim.id} ${claim.rule} ${claim.decision}`);
        if (mode === 'spoof') break;
      } else if (event === 'payout_confirmed' && claim && data.payout?.claim_id === claim.id) {
        payout = data.payout;
        log(`payout confirmed ${payout.paypal_batch_id}/${payout.paypal_item_id} via ${payout.confirmed_via}`);
        break;
      } else if (event === 'replay_ended' && !claim) break;
    }
  } finally {
    ctrl.abort();
  }
  await page.waitForTimeout(6000); // let the confirmed state sit on screen
  if (claim) {
    await page.goto(`${web}/claims/${claim.id}`, { waitUntil: 'domcontentloaded', timeout: 90_000 });
    await settle(page, 5000);
  }
  const video = page.video();
  await ctx.close();
  await wpage.getByRole('button', { name: /power is back/i }).click().catch(() => {});
  await wctx.close();
  const dest = path.join(footageDir, `judge-path-${scenario}.webm`);
  await video.saveAs(dest);
  await video.delete().catch(() => {});
  log(`video saved: ${dest}`);
  return { claim, payout };
}

async function main() {
  log(`web ${web}, api ${api}, video ${videoMode}`);
  log('health', JSON.stringify(await getJSON('/health')));
  const demo = await getJSON('/demo');
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    let result = null;
    if (videoMode !== 'none' && demo.policy_id) result = await recordJudgePath(browser, demo.policy_id, videoMode);

    const claims = (await getJSON('/claims')).claims || [];
    const paid = claims.find((c) => c.payout && c.payout.status === 'success') || claims[0];
    const routes = [
      ['home', '/'],
      ['enrol', '/enrol'],
      ['live', '/live'],
      ['evidence', '/evidence'],
      ['operator', '/operator'],
      demo.policy_id && ['policy', `/policies/${demo.policy_id}`],
      demo.policy_id && ['witness', `/witness/${demo.policy_id}`],
      paid && ['claim', `/claims/${(result?.claim && result.claim.rule !== 'R5' ? result.claim : paid).id}`],
    ].filter(Boolean);

    const ctx = await browser.newContext({ viewport: VIEW });
    for (const [name, route] of routes) {
      const page = await ctx.newPage();
      page.setDefaultTimeout(20_000);
      try {
        await page.goto(web + route, { waitUntil: 'domcontentloaded', timeout: 90_000 });
        await settle(page, name === 'evidence' || name === 'operator' ? 5000 : 3000);
        await page.screenshot({ path: path.join(screensDir, `${name}.png`), timeout: 20_000 });
        await page.screenshot({ path: path.join(screensDir, `${name}-full.png`), fullPage: true, timeout: 30_000 });
        log(`screenshot ${name} (${route})`);
      } catch (e) {
        log(`screenshot ${name} FAILED: ${e.message.split('\n')[0]}`);
      } finally {
        await page.close().catch(() => {});
      }
    }
    await ctx.close();
    if (result) console.log('RESULT', JSON.stringify(result.claim && { claim_id: result.claim.id, rule: result.claim.rule, payout: result.payout }));
  } finally {
    await browser.close();
    clearTimeout(killer);
  }
}

main().catch((e) => {
  console.error('FAIL', e.message);
  process.exit(1);
});
