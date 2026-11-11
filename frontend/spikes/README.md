# Frontend spikes

## S5: phone charger events (`s5-phone-witness.html`)

**What it is.** One self-contained HTML file, no build step, no network requests. Opened on the demo phone, it checks everything the witness page depends on and shows PASS, FAIL, PENDING or N/A per check: secure context, Battery Status API with a live charger log, WebCrypto ECDSA P-256 (non-extractable private key, 64-byte raw r‖s signature), a chain of 7 signed log entries built exactly as `CONTRACTS.md` section 6, local storage persistence across a reload, and online/offline events. It also tries the Screen Wake Lock API (marked "Extra").

**How to run it (Mac, phone on any network).** The Battery API and WebCrypto need HTTPS on a phone, so a plain `http://192.168.x.x` address will not work. The fewest-steps option that works on both iPhone Safari and Android Chrome is a Cloudflare quick tunnel: it gives a public `https://…trycloudflare.com` URL with a normal trusted certificate, so nothing has to be installed on the phone and no account is needed.

One-time install on the Mac (not done by the agents): `brew install cloudflared`

Then, in two Terminal windows from the repo root:

```sh
# Terminal 1: serve the spikes folder on localhost (uses the Node already installed)
npx --yes http-server@14.1.1 frontend/spikes -p 8000 -c-1

# Terminal 2: expose it over HTTPS
cloudflared tunnel --url http://localhost:8000
```

Terminal 2 prints a line like `https://some-random-words.trycloudflare.com`. On the phone open `https://some-random-words.trycloudflare.com/s5-phone-witness.html`. Press Ctrl-C in both terminals when done. The URL is public while the tunnel runs but the page holds no secrets; a new URL is issued each run, and local storage is per URL, so do the reload check within one run.

**What to do on the phone.**
1. Open the page with the phone on its charger. Keep the screen on.
2. Reload the page once (the browser's reload button). "5b" should turn PASS. Each load makes a new key and new entries and clears the logs, so reload before the tests below, not after.
3. Charger test: tap "I am unplugging now", unplug; wait for the line; tap "I am plugging in now", plug in. Each change should add a `chargingchange` line showing the seconds since your tap.
4. Network test: airplane mode on, wait a few seconds, off.
5. Tap "Copy results" and paste into a message (Slack, email, Notes). If copy fails, use "Share…" or "Download .json", or select all in the text box and copy by hand.

**PASS looks like:** the Summary badge reads PASS, and check 2b says "2 charger change(s) seen, both unplug and replug" with each change appearing within a few seconds of your tap. That is the S5 "done when" condition. Record phone model and browser in `TASKS.md`.
**FAIL looks like:** a red FAIL badge with the reason. "2a Battery Status API present: FAIL" means the witness must use the manual `attest_*` buttons; tell the lead. Safari on iPhone is expected to fail 2a (Safari has never shipped `navigator.getBattery`), as is Firefox; Chrome on Android is expected to pass 2a. Only the phone test settles 2b.

**The results JSON** (`fixture_format: "fourhour-witness-fixture/1"`) is plain JSON for the backend's verification tests. Fields: `public_key_jwk` {kty, crv, x, y}; `genesis_hash` (64 zeros); `entries` (section 6 entries, seq 1..7, chained and signed, postable unchanged to `POST /api/witness/entries`); `hash_inputs` (the exact hashed string per entry, same order); `entry_notes` (which values are real readings and which are chosen test values: entry 1 is all real; entries 2–7 use test `battery_level` values 0.5, 0.83, null, 1, 0.00001 and 0.30000000000000004); `test_signature`; `checks`; `battery_log`; `network_log`; `visibility_log`; `user_agent`; `generated_at`. The full field list is in the comment at the top of the HTML file.

**Check a results file without a browser:** `node frontend/spikes/verify-fixture.cjs results.json` (prints `ALL OK` or `FAILED`).

`fixtures/witness-desktop-chrome-headless.json` is a fixture produced by this page in headless desktop Chrome on a Mac (2026-11-11). It is a real WebCrypto output the backend can test against now; it is not the phone result and does not complete S5.
