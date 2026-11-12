# Devpost submission text

Paste each section into the matching Devpost field. Numbers come from `artifacts/` (provenance in each file).

- **Demo URL:** https://fourhour-web.onrender.com
- **Repo:** https://github.com/doshikaran/FourHour (MIT)
- **Video:** `<YouTube URL>`

---

## Inspiration

USDA's Food Safety and Inspection Service says food in a fridge without power is safe for about 4 hours. A food truck, home baker or corner grocer with no generator loses its stock in a short outage. Ordinary business cover is built for long outages and pays slowly, after a claim form. We wanted cover that pays automatically, in the same wallet the seller already uses.

## What it does

FourHour is automatic power-outage cover for small food sellers. A member saves their PayPal wallet once and pays a small monthly premium from it ($1.36 a month for $50 of cover in Harris County, TX). A phone or laptop left on a charger at the premises becomes a **witness**: it keeps a signed, hash-chained log of when its charger loses power.

When the county outage index (DOE EAGLE-I, the share of customers without power) stays above a threshold for 4 hours **and** the member's own witness confirms the outage, the rule engine decides the claim and sends a PayPal Payout of 60% of the cover. No claim form. A witness on its own can never create a payout: unplugging a charger while the grid is fine is flagged for review and pays nothing.

FourHour is the pricing, trigger and claims engine a licensed carrier would run; a sandbox prototype, not an insurance offer.

## How we built it

- **Backend:** Go, standard library `net/http`, SQLite (`modernc.org/sqlite`), plain SQL migrations. A replay engine plays real historical outages through the rule engine and streams events to the browser (Server-Sent Events). Witness entries are verified with `crypto/ecdsa` (P-256) and SHA-256 hash chains.
- **Money:** integer cents end to end. Every money row is written with its idempotency key before the PayPal call, and the database allows one payout per (policy, outage event, tier).
- **Models:** Python (pandas, numpy, scipy, statsmodels) reading 2014–2024 EAGLE-I records for Texas, Florida and Illinois and the Prayas ESMI premises-level supply data. Fitted on 2014–2022, evaluated on 2023–2024, with the split enforced in one place and tested. The pipeline writes JSON artifacts with a provenance block; the Go service only reads them.
- **Frontend:** React 18, TypeScript, Vite, AG Grid Community for the operator grids. The witness page signs entries in the browser with WebCrypto.
- **Hosting:** Render (Go web service + static site).

## PayPal APIs used, and why each is central

| API | Call | Why it matters |
|---|---|---|
| Vault (Payment Method Tokens v3) | `POST /v3/vault/setup-tokens`, `POST /v3/vault/payment-tokens` | The member approves once; every later premium is charged without them present. Without the vault there is no subscription-like cover. |
| Orders v2 | `POST /v2/checkout/orders` with `payment_source.paypal.vault_id` | Charges each monthly premium against the saved wallet, with `PayPal-Request-Id` idempotency so a retry never double-charges. |
| Payouts v1 | `POST /v1/payments/payouts`, `GET /v1/payments/payouts/{id}` | The claim payment itself: automatic, to the member's PayPal account, keyed by `sender_batch_id` so one event can never pay twice. |
| Webhooks | `POST /v1/notifications/verify-webhook-signature` | Payout and capture confirmations are accepted only after PayPal verifies the signature; polling is the fallback. |
| OAuth 2.0 | `POST /v1/oauth2/token` | Client-credentials token, cached. |

## The AI, and what the evidence shows

The "AI" is a set of statistical and actuarial models, not a language model. No language model sets a price, an amount or a claim decision.

- **Basis risk (the result that matters most).** On real Indian premises data (Prayas ESMI, 5,010 premises outages of 4 h or more, scored on a later period than the thresholds were chosen on): paying on the index alone **missed 70.4%** of real outages, and **35.3% of its payments** went to premises that had power. With the witness rules and a perfect witness: **5.9% missed, 0.0% wrong**. With 30% of witness logs missing: 23.5% missed, 6.0% wrong. Caveats: Indian data; only 8 districts with enough monitors, and one of them supplies most of the outages; each premises' own monitor is part of its district index; and the "witness" there is the premises' own monitor. So this is an upper bound. An independent audit (`AUDIT.md`) reproduced every number and lists these limits.
- **Trigger design.** Thresholds chosen on 2014–2022: lower line 20% of customers out (`theta_lo`), upper line 90% (`theta_hi`). 90% is the edge of the search grid, not an optimum, and the objective is nearly flat near it.
- **Frequency.** A negative-binomial GLM **did not beat its baselines** on 2023–2024 and is indistinguishable from the Poisson one: qualifying events are too rare (34 in training) for overdispersion, so it collapses to the Poisson baseline (deviance 130.233 vs 130.216).
- **Severity, credibility, pricing.** Generalised Pareto severity; Bühlmann–Straub credibility (every weight estimated at 0, so counties are priced at their state rate); premium = expected events × 60% × P(premises affected) × cover / 12 × (1 + 47.1% risk margin) + $1.00.
- **Solvency and backtest.** 1,000 simulated members, 20,000 simulated years: 12.3% probability of ruin with no starting capital. Backtest: 2023 loss ratio 0.00, 2024 loss ratio 1.32 (the Houston derecho and Hurricane Beryl). Two test years is far too few to judge.

## Challenges we ran into

- County data can't prove a particular fridge was off; we measured how much a premises witness helps instead of assuming it.
- EAGLE-I has scrape gaps and partial-feed dropouts during big storms; event reconstruction bridges gaps up to 2 hours and treats dropouts as missing.
- A witness can be spoofed by unplugging a charger, so the rules only let it confirm or contradict an index event, never create one.

## Accomplishments we're proud of

- Real PayPal sandbox money end to end: vault, merchant-initiated premium charges, and automatic payouts that are confirmed by PayPal before the UI shows them as paid (signature-verified webhook, with polling as the fallback).
- Every number in the app and README traces to an artifact with provenance; anything not computed is shown as "not computed".
- We report the results that didn't work.

## Limitations

- Cover in practice requires a witness: the 90% index-only line almost never pays.
- Outages affecting less than 20% of a county aren't covered.
- Counties with no qualifying history (Cook County, IL) aren't offered.
- Self-reported witness entries ("Power is out" button) are treated like sensed ones.
- Only the 4-hour tier is paid; a 24-hour top-up was designed and cut.
- Small premiums carry a high share of fixed payment fees; the $1.00 expense load is an assumption.
- Members, their premises and loss amounts are simulated. Outage data and model results are real.

## What's next

- A licensed carrier partner and a real rate filing.
- Utility outage feeds at a finer level than the county to shrink basis risk.
- A native witness app with background sensing, and pricing that rewards having a witness.
- More test years before trusting the loss ratio.

## Built with

Go, SQLite, Python, pandas, NumPy, SciPy, statsmodels, React, TypeScript, Vite, AG Grid Community, WebCrypto, Server-Sent Events, PayPal Vault, PayPal Orders v2, PayPal Payouts, PayPal Webhooks, DOE EAGLE-I, Prayas ESMI, Render.

## Testing instructions (no PayPal login needed)

1. Open the demo URL and go to **Live** (`/live`). The free host may take up to a minute to wake.
2. A demo member with an active $50 Harris County policy already exists (simulated member, real sandbox wallet).
3. In **Run the demo**, open the witness link in a new tab on the same computer and press **Set up this device as the witness**.
4. Back on Live, start **derecho-harris-2024** (real EAGLE-I data, Houston, May 2024).
5. When the index crosses the 20% line, press **Power is out** on the witness tab (or unplug a laptop charger in Chrome) and leave it.
6. About 4 simulated hours later (a few real seconds) rule R3 decides the claim and a $30.00 sandbox payout goes from "sending" to "confirmed". Open the claim to see the rule, the index, the witness log and the PayPal batch and item IDs.
7. To see the fraud control, start **spoof-quiet** and press **Power is out**: the claim is R5, flagged for review, and nothing is paid.
8. **Evidence** (`/evidence`) shows every model result with its provenance; **Operator** (`/operator`) shows policies, claims and the ledger.

Without a browser: `node scripts/judge-e2e.mjs https://fourhour-api.onrender.com/api spoof` runs the fraud case and prints the claim (Node 20+).
