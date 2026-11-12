# FourHour backend (Go)

Go 1.27 or later. Everything below runs from `backend/`. The service reads only the variables in `CONTRACTS.md` section 2, from the process environment or from `-env-file ../.env` (the process environment wins). It refuses to start unless `PAYPAL_BASE_URL` is exactly `https://api-m.sandbox.paypal.com`.

## Run the backend for the browser demo (sandbox)

1. Seed the demo member and its test-cover policy. This needs `DEMO_VAULT_TOKEN` and `SANDBOX_MEMBER_EMAIL` and refuses to run without them. It makes no PayPal call unless you pass `-charge`.

   ```
   go run ./cmd/fourhour seed -env-file ../.env
   go run ./cmd/fourhour seed -env-file ../.env -charge   # also charges this month's $1.00 premium
   ```

   Flags: `-fips 48201` (default Harris County), `-charge`, `-env-file`. The seed is **idempotent**. A second run finds the same active `test-cover` policy for that member email and county and writes nothing. A second `-charge` in the same month returns the existing charge and calls PayPal again only if the earlier call's outcome was unknown, in which case it reuses the same `PayPal-Request-Id`. The policy has `cover_cents` 100, `premium_cents` 100 and `model_version` `test-cover`. Payouts go to `SANDBOX_PAYOUT_EMAIL`, or to `SANDBOX_MEMBER_EMAIL` if that is not set.

2. Start the server:

   ```
   FRONTEND_URL=http://localhost:5173 go run ./cmd/fourhour -env-file ../.env
   ```

   It listens on `:$PORT` (default 8080; `-addr` overrides). `FRONTEND_URL` is the only CORS origin and the redirect target after vault approval, so set it to wherever the frontend runs. `ARTIFACTS_DIR` defaults to `../artifacts` and is re-read on every request, so new artifacts from `ml/` appear without a restart. `DATABASE_URL` defaults to `./fourhour.db`. Seed and server must use the same `DATABASE_URL`.

3. Start a replay (or press start on `/live`):

   ```
   curl -N http://localhost:8080/api/replay/stream          # in one terminal
   curl -X POST http://localhost:8080/api/replay/start -H 'Content-Type: application/json' \
        -d '{"scenario_id":"fixture-outage-harris","speed":3600}'
   ```

   With no witness device registered, the fixture drives rule R2. The stream then sends `claim_decided` (30 cents), `payout_sent` (status `pending`) and, about 20 seconds later, `payout_confirmed` (status `success`, `confirmed_via` `poll`). Each replay creates new outage events and new keys, so **each replay that pays sends a real 30-cent sandbox payout**. `POST /api/replay/reset` stops the replay and clears its in-memory state. It deletes no rows.

Without `PAYPAL_WEBHOOK_ID`, webhooks are stored unverified and change nothing, and payouts are confirmed by polling. Without `APP_BASE_URL`, `POST /api/policies` answers `503 config_missing`, because PayPal needs a return URL. The seeded policy does not need it.

## What is fixture and what is real

- `fixture-outage-harris` (embedded in `internal/artifacts/fixtures/scenarios/`) is a hand-written series, not EAGLE-I data. It is dated in the year 2000 and listed with `is_fixture: true`. Its 95% plateau crosses `theta_hi` under both the real and the fixture trigger parameters.
- `fixture-trigger_params.json` is used only when `artifacts/trigger_params.json` is missing, invalid or has a null value. The server logs a warning when it falls back. It is never listed in `artifacts_loaded` or served by `/api/analytics/trigger`.
- The payout schedule comes from `pricing_table.json`. If that file is missing, it falls back to `SPEC.md` section 4 (60% at 4 hours, 100% at 24 hours), with a logged warning.

## Tests

```
go vet ./...
go test -race ./...
```

PayPal calls in the unit tests go to `internal/paypal/paypaltest`, a fake server with **hand-written** responses based on the PayPal API reference. The repeated-order and repeated-`sender_batch_id` behaviours follow what spikes S1 and S2 observed in the sandbox. None of the responses were recorded from the sandbox.

**Opt-in sandbox integration test** (off by default; it moves real sandbox money: a $1.00 charge and a $0.30 payout per run):

```
FOURHOUR_SANDBOX_IT=1 go test -race -count=1 -timeout 6m -run TestSandboxLoop -v ./internal/integration/
```

It runs seed, premium charge (P4), fixture replay, rule R2, payout (P5) and confirmation by polling (P6) against the real sandbox, using a fresh temporary database and an empty artifacts directory. It prints every `paypal-debug-id` and never prints the client secret, the access token or `DEMO_VAULT_TOKEN`.

## Layout

| Package | Role |
|---|---|
| `cmd/fourhour` | server and `seed` subcommand |
| `internal/config` | the only reader of the environment |
| `internal/paypal` | the only PayPal client (P1–P8, D7 duplicate-batch reading) |
| `internal/db` | SQLite open (one connection) and migrations |
| `internal/artifacts` | artifact validation and typed loaders, embedded fixtures |
| `internal/rules` | run detection (gap bridge, dropouts, hours clause) and rules R1–R5 |
| `internal/witness` | entry parsing, JSON.stringify-exact hash input, ECDSA r‖s verification |
| `internal/service` | members, policies, premiums, claims, payouts, polling, webhooks, witness, ledger |
| `internal/replay` | simulated clock, event persistence, rule evaluation, stream snapshot |
| `internal/stream` | SSE fan-out |
| `internal/api` | HTTP routes (`CONTRACTS.md` section 4) |
