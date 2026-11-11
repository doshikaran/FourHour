# FourHour: contracts

Version: 1.3

This file is the single source of truth for every interface between the backend, the frontend, the ML pipeline and PayPal. Change it before changing code. Only the lead edits it (see `CLAUDE.md`).

Changelog:
- 1.0: initial.
- 1.1: backend moves from Python to Go; ML stays in Python and meets the backend only at `artifacts/`. No field, endpoint, event, key or route changed. Section 2: `DATABASE_URL` default is now a plain SQLite file path for the `modernc.org/sqlite` driver. Section 6: signature verification described for Go `crypto/ecdsa` (raw r and s, JWK x and y) instead of Python DER; the hash input string must be built explicitly to match `JSON.stringify`, not with `encoding/json`. Section 8: the backend validates artifacts at startup and reports a missing or malformed file through `/api/health` instead of crashing.
- 1.2: answers to Gate 1 (K4) questions. Section 1: ID prefixes `wen_` and `led_`. Section 2: added `PORT` and `VITE_USE_MOCK`. Section 3: nullability stated; `refunds.created_at` added; `ledger_entries` unique on (`kind`, `ref_id`); rejected witness entries are stored (unparseable ones are not; they are rejected as `malformed`) and the (`device_id`, `seq`) uniqueness applies to valid rows only. Section 4: policy responses never include vault tokens; `/api/health` may return `503 database_unavailable`; scenarios carry `is_fixture`; `GET /api/claims/{id}` shape fixed. Section 5: `snapshot` body fixed and now includes claims decided so far. Section 8: `artifacts_loaded` names; `pricing_table.counties` keyed by FIPS; `stop_loss` keys in cents; basis-risk notes live in `provenance.notes`. The remaining artifact inner shapes are still open (see the note under the table).
- 1.3: Gate 2 decisions D1–D9. Section 2: added `DEMO_VAULT_TOKEN`, `SANDBOX_MEMBER_EMAIL`, `SANDBOX_PAYOUT_EMAIL`. Section 3: an outage event is merged runs under the 72-hour hours clause; missing data never ends an event. Section 4: example scenario ID is now `derecho-harris-2024`. Section 5: `tick` `share` and `customers_out` may be null for a missing interval. Section 7: P5 duplicate `sender_batch_id` is not a failure; P4 needs no `stored_credential`; payout confirmation takes about 20–25 seconds in sandbox. Section 3: test cover of 100 cents for seeded sandbox test policies; payout amounts rounded down to a whole cent. Section 8: `trigger_params.json` adds `event_merge_hours` (72) and `gap_bridge_minutes` becomes 120; a missing 15-minute row in a scenario `series` is a point with `customers_out` and `share` null.

## 1. Conventions

- **JSON** field names are `snake_case`.
- **Time** is ISO 8601 UTC with a `Z` suffix, e.g. `2024-07-08T09:15:00Z`.
- **Money** is integer cents in a field ending `_cents`. Currency is USD. When sent to PayPal it is formatted as a decimal string with two places (`"150.00"`).
- **IDs** are a type prefix plus 12 lowercase base32 characters: `mem_`, `pol_`, `chg_`, `evt_`, `dev_`, `clm_`, `pay_`, `rfd_`, `wen_` (witness entry), `led_` (ledger entry).
- **County** is a 5-digit FIPS string, e.g. `"48201"`.
- **Errors** from the backend use one shape, with the matching HTTP status:

```json
{ "error": { "code": "policy_not_found", "message": "No policy pol_abc...", "paypal_debug_id": null } }
```

- **Base path** for all backend routes is `/api`.

## 2. Environment variables

| Name | Used by | Meaning |
|---|---|---|
| `PAYPAL_BASE_URL` | backend | Must be `https://api-m.sandbox.paypal.com` |
| `PAYPAL_CLIENT_ID` | backend | Sandbox app client ID |
| `PAYPAL_CLIENT_SECRET` | backend | Sandbox app secret |
| `PAYPAL_WEBHOOK_ID` | backend | ID of the registered webhook, for signature verification |
| `APP_BASE_URL` | backend | Public URL of the backend (vault return URL, webhook URL) |
| `FRONTEND_URL` | backend | Where to redirect after vault approval; also the CORS origin |
| `DATABASE_URL` | backend | SQLite file path for the `modernc.org/sqlite` driver. Default `./fourhour.db` |
| `ARTIFACTS_DIR` | backend | Default `../artifacts` |
| `PORT` | backend | Port to listen on. Default `8080`. Render sets it |
| `DEMO_VAULT_TOKEN` | backend (seed command, integration test) | Payment token saved in S1, used to seed a demo policy with no browser step |
| `SANDBOX_MEMBER_EMAIL` | backend (seed command, integration test) | Sandbox personal account used as the demo member |
| `SANDBOX_PAYOUT_EMAIL` | backend (integration test) | Sandbox personal account that receives test payouts |
| `VITE_API_BASE_URL` | frontend | Backend base URL including `/api` |
| `VITE_USE_MOCK` | frontend | `true` serves every endpoint and the stream from the frontend's mock layer. Default `false` |

## 3. Data model

Enums are stored as strings. A field that only gets a value later (PayPal IDs, end times, `cancelled_at`, `confirmed_at`, `confirmed_via`, `sim_time`, `reject_reason`, vault tokens) is null until then. Every other field is required.

**members**: `id`, `name`, `email`, `paypal_email` (the sandbox personal account that receives payouts), `created_at`.

**policies**: `id`, `member_id`, `fips`, `cover_cents` (5000, 15000 or 30000; or 100 for a seeded test policy, see below), `premium_cents`, `status` (`pending_vault` | `active` | `cancelled`), `vault_setup_token`, `vault_payment_token`, `model_version`, `created_at`, `cancelled_at`.

**Test cover.** The seed command (sandbox only) may create a policy with `cover_cents` 100 and `premium_cents` 100, so integration runs charge and pay at most $1.00. Such a policy has `model_version` `test-cover` and is labelled "test cover" in the UI. `POST /api/quotes` and `POST /api/policies` never accept 100.

**Payout amounts** are `cover_cents` × the schedule share × the rule's factor (R2: 50%), computed in integer arithmetic and rounded down to a whole cent.

**premium_charges**: `id`, `policy_id`, `period` (`YYYY-MM`), `amount_cents`, `request_id` (unique), `paypal_order_id`, `paypal_capture_id`, `status` (`created` | `completed` | `failed` | `refunded`), `created_at`. Unique on (`policy_id`, `period`).

**outage_events**: `id`, `fips`, `scenario_id` (null when not from a replay), `lo_started_at`, `lo_ended_at`, `hi_started_at`, `hi_ended_at`, `peak_share`. An event is a run at or above `theta_lo`, where missing intervals of up to `gap_bridge_minutes` between readings at or above the threshold do not end it, and runs starting within `event_merge_hours` of the previous run's end are merged into it (`SPEC.md` section 4, hours clause). The `hi_*` fields describe the longest run at or above `theta_hi` inside it, and are null if there is none. End times are null while the run is open.

**witness_devices**: `id`, `policy_id`, `public_key_jwk`, `registered_at`, `last_seq`, `last_hash`.

**witness_entries**: `id`, `device_id`, `seq`, `device_time`, `kind`, `charging`, `battery_level`, `online`, `prev_hash`, `hash`, `sig`, `received_at`, `sim_time` (null outside a replay), `valid`, `reject_reason`. Rejected entries are stored for audit with `valid = false`, their values as received and a `reject_reason`. Unique on (`device_id`, `seq`) among valid rows only, so a corrected resend of a rejected `seq` can be stored. An entry that cannot be parsed (a missing field or a wrong JSON type) is not stored; it is returned in `rejected` with reason `malformed`.

**claims**: `id`, `policy_id`, `outage_event_id`, `tier` (`t4` | `t24`), `rule` (`R1`..`R5`), `decision` (`paid` | `declined` | `review`), `amount_cents`, `reason` (one plain sentence), `decided_at`, `sim_time`. Unique on (`policy_id`, `outage_event_id`, `tier`).

**payouts**: `id`, `claim_id` (unique), `sender_batch_id` (unique), `paypal_batch_id`, `paypal_item_id`, `amount_cents`, `status` (`created` | `pending` | `success` | `failed` | `unclaimed`), `created_at`, `confirmed_at`, `confirmed_via` (`webhook` | `poll`).

**refunds**: `id`, `premium_charge_id`, `amount_cents`, `request_id` (unique), `paypal_refund_id`, `status` (`created` | `completed` | `failed`), `created_at`.

**webhook_events**: `id` (PayPal's event ID, unique), `event_type`, `resource_id`, `verified`, `received_at`, `payload_json`.

**ledger_entries**: `id`, `at`, `kind` (`premium` | `payout` | `refund`), `amount_cents` (positive into the pool, negative out), `policy_id`, `ref_id` (the premium charge, payout or refund ID). Unique on (`kind`, `ref_id`), so a money movement is never appended twice. Append-only: rows are never updated or deleted. Pool balance is the sum.

## 4. REST API

All bodies are JSON. `200` unless stated.

### Reference data and quotes

`GET /api/health` → `{ "status": "ok", "paypal_env": "sandbox", "artifacts_loaded": ["pricing_table", ...] }`
`artifacts_loaded` lists the section 8 top-level artifacts that loaded and passed validation, by file name without `.json`. Scenario files are listed by `/api/replay/scenarios`, not here. If the database cannot be reached it returns `503` with code `database_unavailable` in the error shape.

`GET /api/counties` → `{ "counties": [ { "fips": "48201", "name": "Harris County", "state": "TX", "data_coverage": 0.97 } ] }`

`POST /api/quotes`
Request: `{ "fips": "48201", "cover_cents": 15000 }`
Response:

```json
{
  "fips": "48201",
  "cover_cents": 15000,
  "premium_cents": 412,
  "model_version": "2026-11-11a",
  "breakdown": {
    "expected_events_per_year": 0.61,
    "prob_24h_given_event": 0.18,
    "prob_premises_affected": 0.22,
    "expected_annual_payout_cents": 1410,
    "credibility_weight": 0.74,
    "risk_margin_rate": 0.35,
    "expense_load_cents": 150
  },
  "provenance": { "...": "see section 8" }
}
```

The numbers above only illustrate the shape. Real values come from `artifacts/pricing_table.json`.

### Members and policies

`POST /api/members` → `201`
Request: `{ "name": "...", "email": "...", "paypal_email": "..." }`
Response: the member row.

Policy rows in any API response never include `vault_setup_token` or `vault_payment_token`. They stay in the database.

`POST /api/policies` → `201`
Request: `{ "member_id": "mem_...", "fips": "48201", "cover_cents": 15000 }`
Response: `{ "policy": { ...policy row... }, "approve_url": "https://www.sandbox.paypal.com/..." }`
Creates the policy as `pending_vault`, creates a vault setup token, and returns PayPal's approval link. The frontend sends the browser to `approve_url`.

`GET /api/paypal/vault/return?policy_id=pol_...` (the `return_url` given to PayPal; PayPal appends its own query parameters)
Exchanges the approved setup token for a payment token, stores it, sets the policy `active`, charges the first premium, then redirects (`302`) to `{FRONTEND_URL}/policies/{policy_id}?enrolled=1`. On failure it redirects with `?enrolled=0&error=<code>`.

`GET /api/paypal/vault/cancel?policy_id=pol_...` → `302` to `{FRONTEND_URL}/enrol?cancelled=1`.

`GET /api/policies/{id}` → `{ "policy": {...}, "member": {...}, "premium_charges": [...], "devices": [...], "claims": [...] }`

`GET /api/policies` → `{ "policies": [ ... ] }` (operator view)

`POST /api/policies/{id}/charge-premium`
Request: `{ "period": "2026-12" }` → the premium_charge row. Calling it twice for the same period returns the existing row and charges nothing.

`POST /api/policies/{id}/cancel` → `{ "policy": {...}, "refund": {...} | null }`
Refunds the unused share of the current period's premium and sets the policy `cancelled`.

### Witness

`POST /api/witness/devices` → `201`
Request: `{ "policy_id": "pol_...", "public_key_jwk": { "kty": "EC", "crv": "P-256", "x": "...", "y": "..." } }`
Response: `{ "device_id": "dev_...", "genesis_hash": "0000...0000" }`

`POST /api/witness/entries`
Request: `{ "device_id": "dev_...", "entries": [ { ...entry, section 6... } ] }`
Response: `{ "accepted": 3, "rejected": [ { "seq": 14, "reason": "bad_signature" } ], "last_seq": 13, "last_hash": "..." }`
Entries must arrive in `seq` order starting at `last_seq + 1`. A resend of an already accepted `seq` with the same hash is accepted silently.

`GET /api/witness/devices/{id}/entries?limit=200` → `{ "entries": [ ... ] }`

### Replay

`GET /api/replay/scenarios` → `{ "scenarios": [ { "id": "derecho-harris-2024", "title": "...", "fips": "48201", "start": "...", "end": "...", "description": "...", "is_fixture": false } ] }`
`is_fixture` is true for fixture scenarios named `fixture-*`; the UI labels them.

`POST /api/replay/start`
Request: `{ "scenario_id": "derecho-harris-2024", "speed": 3600 }` (`speed` is simulated seconds per real second)
Response: `{ "replay_id": "...", "sim_start": "...", "sim_end": "..." }`
One replay runs at a time. Starting a new one stops the old one.

`POST /api/replay/stop` → `{ "stopped": true }`

`POST /api/replay/reset` → `{ "reset": true }`
Deletes outage events, witness entries, claims and payouts rows created by replays, so a demo can be re-run. It never touches PayPal. Payouts already sent stay sent; re-running a scenario creates a new replay and new outage events, so new keys.

`GET /api/replay/stream` → Server-Sent Events, section 5.

### Claims, ledger, analytics

`GET /api/claims` → `{ "claims": [ { ...claim row..., "payout": {...} | null, "member_name": "...", "fips": "..." } ] }`

`GET /api/claims/{id}` → `{ "claim": {...same shape as an item of GET /api/claims, including payout...}, "outage_event": {...row...}, "index_series": [ { "t": "...", "customers_out": 0, "share": 0.0 } ], "witness_entries": [ {...row...} ] }`. `index_series` covers the event window. `witness_entries` are the entries the rule engine used.

`GET /api/ledger` → `{ "balance_cents": 123400, "entries": [ ... ] }`

`GET /api/analytics/backtest`, `/api/analytics/pricing`, `/api/analytics/solvency`, `/api/analytics/basis-risk`, `/api/analytics/trigger`, `/api/analytics/data-quality` → the matching artifact from section 8, unchanged. `404` with code `artifact_missing` if the file isn't there.

### PayPal webhook

`POST /api/paypal/webhook` → `200` always once the event is stored (PayPal retries on anything else). Handling is in section 7.

## 5. Replay event stream

`GET /api/replay/stream` sends SSE messages. Each has an `event:` name and a JSON `data:` body that always includes `sim_time`.

| Event | Extra fields | Sent when |
|---|---|---|
| `tick` | `share`, `customers_out`, `fips` | Every simulated 15 minutes. `share` and `customers_out` are null for a missing interval |
| `event_opened` | `outage_event_id`, `threshold` (`lo` or `hi`) | The share crosses a threshold upward |
| `event_closed` | `outage_event_id`, `threshold`, `duration_hours` | The share falls below it |
| `witness_entry` | `device_id`, `policy_id`, `kind`, `valid` | A witness entry is accepted or rejected |
| `claim_decided` | `claim` (full row) | The rule engine decides a claim |
| `payout_sent` | `payout` (full row) | The Payouts call returned |
| `payout_confirmed` | `payout` (full row) | A webhook or poll confirmed the payout |
| `replay_ended` | `replay_id` | The scenario finished or was stopped |

The frontend must tolerate reconnects: on connect, the server first sends a `snapshot` event with the current replay state: `{ "replay_id", "scenario_id", "sim_time", "open_events": [ outage_event rows ], "series": [ tick bodies so far ], "claims": [ claims decided in this replay, same shape as GET /api/claims items ] }`. When no replay is running, `replay_id`, `scenario_id` and `sim_time` are null and the lists are empty.

## 6. Witness log format

One entry:

```json
{
  "seq": 12,
  "device_time": "2026-11-12T05:31:07Z",
  "kind": "power_lost",
  "charging": false,
  "battery_level": 0.83,
  "online": true,
  "prev_hash": "9f2c...",
  "hash": "41ab...",
  "sig": "base64url..."
}
```

- `kind` is one of `heartbeat`, `power_lost`, `power_restored`, `attest_lost`, `attest_restored`. The two `attest_*` kinds are the manual fallback (`SPEC.md` section 8) and count as `power_lost` / `power_restored` in the rules.
- `battery_level` is 0 to 1, or null if unavailable. `online` is the browser's own view of connectivity when the entry was written.
- `seq` starts at 1 and increases by 1. The first entry's `prev_hash` is 64 zeros.
- **Hash:** SHA-256, lowercase hex, of the UTF-8 bytes of this exact string, fields in this order, no spaces:
  `{"seq":12,"device_time":"...","kind":"...","charging":false,"battery_level":0.83,"online":true,"prev_hash":"..."}`
  Numbers are written as JavaScript's `JSON.stringify` writes them. The backend must rebuild the string from the received values byte for byte as `JSON.stringify` would write it; test this with fixtures generated in a real browser.
  The backend builds this string explicitly, field by field. It must not use Go's `encoding/json` to produce it, because the number and escaping rules differ (for example, `encoding/json` escapes `<`, `>` and `&`, and Go's shortest float format writes `1e-05` where JavaScript writes `0.00001`). Tests cover `battery_level` values `1`, `0.5`, `0.83` and `null`.
- **Signature:** ECDSA P-256 with SHA-256 over the UTF-8 bytes of the `hash` hex string. WebCrypto returns the signature as 64 raw bytes (r then s). It is sent base64url-encoded without padding. The backend splits the 64 bytes into r and s (32 bytes each, big-endian) and verifies with Go's `crypto/ecdsa`. The public key arrives as a JWK; the backend decodes its base64url `x` and `y` values into a P-256 public key.
- Heartbeats are written every 60 seconds while the page is open.

The backend rejects an entry, with a reason, when: the signature fails (`bad_signature`), `prev_hash` doesn't match the stored `last_hash` (`broken_chain`), `seq` is not `last_seq + 1` (`bad_sequence`), `device_time` is earlier than the previous entry's (`time_went_backwards`). Outside a replay it also rejects entries whose `device_time` is more than 24 hours behind arrival with no earlier offline gap to explain it (`stale`).

A witness outage interval runs from a valid `power_lost` (or `attest_lost`) entry to the next valid `power_restored` (or `attest_restored`). During a replay the interval is measured in `sim_time`.

## 7. PayPal calls

All calls go to `PAYPAL_BASE_URL`. Confirm each request body against the linked reference before coding it. Every response's `debug_id` (header `paypal-debug-id`) is logged.

| # | Purpose | Call | Idempotency |
|---|---|---|---|
| P1 | Access token | `POST /v1/oauth2/token` (client credentials). Cache until shortly before expiry | none |
| P2 | Start saving the wallet | `POST /v3/vault/setup-tokens` with `payment_source.paypal` containing `usage_type: "MERCHANT"` and an `experience_context` with `return_url` and `cancel_url` | `PayPal-Request-Id: vst-{policy_id}` |
| P3 | Finish saving the wallet | `POST /v3/vault/payment-tokens` with the approved setup token | `PayPal-Request-Id: vpt-{policy_id}` |
| P4 | Charge a premium | `POST /v2/checkout/orders`, `intent: "CAPTURE"`, one purchase unit, `payment_source.paypal.vault_id` = the payment token | `PayPal-Request-Id: prem-{policy_id}-{period}` |
| P5 | Pay a claim | `POST /v1/payments/payouts`, one item, `recipient_type: "EMAIL"`, `recipient_wallet: "PAYPAL"`, `receiver` = member's `paypal_email` | `sender_batch_id: clm-{policy_id}-{outage_event_id}-{tier}`; `sender_item_id` = the claim ID |
| P6 | Check a payout | `GET /v1/payments/payouts/{payout_batch_id}` | read-only |
| P7 | Refund a premium | `POST /v2/payments/captures/{capture_id}/refund` with an amount | `PayPal-Request-Id: rfd-{charge_id}` |
| P8 | Verify a webhook | `POST /v1/notifications/verify-webhook-signature` with the received headers, the raw body and `PAYPAL_WEBHOOK_ID` | read-only |

References: developer.paypal.com/docs/api/payment-tokens/v3/, /docs/api/orders/v2/, /docs/api/payments.payouts-batch/v1/, /docs/api/payments/v2/, /api/rest/webhooks/.

**P4 success** means the order status is `COMPLETED` and the capture inside it is `COMPLETED`. A capture that is `PENDING` is stored as `created`, not `completed`, and is not added to the ledger until a webhook or a later read confirms it.

**P4 vaulted charge.** Confirmed in S1: no `stored_credential` field is needed. Repeating the order with the same `PayPal-Request-Id` returns the original order with HTTP 200.

**P5 duplicate batch.** Confirmed in S2: repeating a `sender_batch_id` returns HTTP 400 with `name` `USER_BUSINESS_ERROR`, `details[0].field` `SENDER_BATCH_ID` and `issue` "Batch with given sender_batch_id already exists", with `details[0].link[0].href` pointing at the original batch. This is not a failure. The backend reads the original batch ID from that link, fetches it with P6, updates the payout row from it and continues from its status. Any other 400 is a failure.

**P5 timing.** In S2 the batch went `PENDING` → `PROCESSING` → `SUCCESS` in about 20–25 seconds. `PROCESSING` counts as still pending. The UI shows a visible "sending" state for that time.

**P5 funding.** Payouts are paid from the sandbox business account's balance. Check the balance in the sandbox dashboard before a demo.

**Order of writes for any money call:** insert the local row with its key and status `created` → call PayPal → update the row from the response. If the process dies between the call and the update, the same key on retry returns PayPal's original result. The unique constraints in section 3 stop a second local row.

**Webhooks to subscribe to.** Confirm the exact names on developer.paypal.com/api/rest/webhooks/event-names:

- `PAYMENT.CAPTURE.COMPLETED`, `PAYMENT.CAPTURE.DENIED`, `PAYMENT.CAPTURE.REFUNDED`
- `PAYMENT.PAYOUTSBATCH.SUCCESS`, `PAYMENT.PAYOUTSBATCH.DENIED`
- `PAYMENT.PAYOUTS-ITEM.SUCCEEDED`, `PAYMENT.PAYOUTS-ITEM.FAILED`, `PAYMENT.PAYOUTS-ITEM.UNCLAIMED`

**Webhook handling:** store the raw event; skip it if its ID is already stored; verify with P8; if verified, update the matching row and append to the ledger if that hasn't happened yet; emit `payout_confirmed` on the stream when a payout succeeds. Events from the webhook simulator can't be verified with P8, so they are stored with `verified = false` and change nothing.

**Polling fallback.** Webhooks need a public URL. For any payout still `pending` 10 seconds after it was sent, poll P6 every 5 seconds for up to 2 minutes. Whichever arrives first sets `confirmed_via`.

**Not used:** Venmo payouts, the PayPal MCP server and Agent Toolkit, Subscriptions, Disputes.

## 8. Artifacts

Files in `artifacts/`, written by `ml/`, read by the backend, shown by the frontend. Every file has this block:

```json
"provenance": {
  "generated_at": "2026-11-11T18:00:00Z",
  "git_commit": "abc1234",
  "code": "ml/backtest.py",
  "data_sources": [ { "name": "EAGLE-I 2014-2024", "url": "...", "sha256": "...", "rows_used": 123456 } ],
  "train_years": [2014, 2022],
  "test_years": [2023, 2024],
  "seed": 20261111,
  "notes": "Free text: gaps, exclusions, assumptions."
}
```

| File | Contents |
|---|---|
| `trigger_params.json` | `theta_hi`, `theta_lo`, `gap_bridge_minutes` (120), `event_merge_hours` (72), and `grid`: every candidate pair with its expected wrong and missed payouts |
| `pricing_table.json` | `model_version`; `counties`: an object keyed by FIPS, each value holding `expected_events_per_year`, `prob_24h_given_event`, `prob_premises_affected`, `credibility_weight`; `rates`: `risk_margin_rate`, `expense_load_cents`; `payout_schedule`: `{ "t4": 0.6, "t24": 1.0 }` |
| `backtest.json` | `frequency`: per county and year, predicted and realised event counts, for the NB model and both baselines, plus deviance and calibration bins; `severity`: GPD parameters, fit statistic, QQ points; `credibility`: weight per county; `pool`: per test year, premiums, payouts, loss ratio, events |
| `solvency.json` | `members`, `simulations`, `ruin_probability`, `capital_1_in_200_cents`, `stop_loss`: `attachment_cents`, `limit_cents`, `price_cents`; `annual_loss_quantiles` |
| `basis_risk.json` | Per scheme (`index_only`, `rules_perfect_witness`, `rules_10pct_missing`, `rules_30pct_missing`): premises-events, `missed_rate`, `wrong_rate`, counts; `districts_used`; `fit_period`, `score_period`. Caveats (perfect witness, upper bound) go in `provenance.notes` |
| `data_quality.json` | Per county-year: expected intervals, intervals present, coverage share, whether excluded from fitting |
| `scenarios/{id}.json` | `id`, `title`, `description`, `fips`, `county_customers`, `start`, `end`, `series`: list of `{ "t": "...", "customers_out": 0, "share": 0.0 }` at 15-minute steps, `source` |

A field that could not be computed is `null` with the reason in `provenance.notes`. It is never filled with a plausible value.

Still open, to be fixed before tasks M1–M6 and E1: the item shape of `trigger_params.grid`; the inner shapes of `backtest`; `annual_loss_quantiles`; where the four basis-risk schemes sit and their count keys; the types of `fit_period` and `score_period`; the row key of `data_quality.json`; the type of a scenario's `source`. A missing 15-minute row in a scenario `series` is a point with `customers_out` and `share` set to null. Until then, producers and consumers treat these as open and do not invent keys.

The Go service validates each artifact against the shape above at startup. A missing or malformed file does not crash it: the file is left out of `artifacts_loaded` in `/api/health`, and the reason is logged.

## 9. Frontend routes

| Route | Purpose |
|---|---|
| `/` | Problem statement, USDA quote with link, start button |
| `/enrol` | County and cover picker, quote with breakdown, member details, hand-off to PayPal |
| `/policies/:id` | Policy status, premium history, witness device status and QR/link to the witness page, claims |
| `/witness/:policyId` | The phone page (`SPEC.md` section 8) |
| `/live` | Replay control, index chart with thresholds and the 4-hour clock, event feed, claims as they are decided |
| `/claims/:id` | One claim explained: rule, index curve, witness log, payout status |
| `/operator` | Policies, claims and ledger grids (AG Grid) |
| `/evidence` | Backtest, pricing, solvency, basis-risk and data-quality panels (AG Grid plus charts), each with its provenance |
