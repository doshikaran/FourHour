# FourHour

FourHour is automatic power-outage cover for small food sellers. A member pays a small monthly premium from a saved PayPal wallet. When a grid outage lasts past the USDA 4-hour food-safety line and the member's own phone confirms the power was out at their premises, a PayPal Payout is sent automatically. No claim form.

It is an entry for the PayPal AI Hackathon. The goal is to win. Everything in this repo is judged on five equal criteria: technological implementation, design, potential impact, innovation, presentation.

## Read these first, in this order

1. `SPEC.md`: what the product does, the trigger rules, the models, the demo.
2. `CONTRACTS.md`: every interface. Schema, API, events, PayPal calls, artifact formats.
3. `TASKS.md`: ordered work with "done when" checks.

If code and `CONTRACTS.md` disagree, `CONTRACTS.md` is right and the code is a bug.

## Repo layout and ownership

| Path | Owner | Contents |
|---|---|---|
| `backend/` | backend-engineer | Go service: PayPal client, ledger, replay engine, witness verification, rule engine. Reads `artifacts/` |
| `spikes/paypal/` | backend-engineer | Small Go programs that each test one PayPal assumption (S1, S2) |
| `ml/` | ml-engineer | Python: data ingest, models, backtests. Writes `artifacts/` |
| `artifacts/` | ml-engineer | Generated JSON read by the backend and shown in the UI. Never edited by hand |
| `spikes/data/` | ml-engineer | Small Python scripts that each test one data assumption (S3, S4) |
| `frontend/` | frontend-engineer | React app: enrolment, live replay, claims, analytics panels, witness page. The phone charger spike (S5) lives in `frontend/spikes/` |
| `docker-compose.yml` | lead | Builds and runs the backend and frontend together |
| `CONTRACTS.md` | lead | Changed only by the process below |
| `README.md`, `LICENSE` | lead | Judge-facing. See `SPEC.md` section 10 |

An agent does not edit files outside the paths it owns. If it needs a change elsewhere, it says so in its report.

## How the work is run

The main Claude Code session is the **lead**. The lead:

- Delegates to the `backend-engineer`, `ml-engineer` and `frontend-engineer` agents, in parallel where tasks don't depend on each other.
- Does not write feature code in `backend/`, `ml/` or `frontend/`. It integrates, runs end-to-end checks, and keeps `TASKS.md` current.
- Owns `CONTRACTS.md`. To change a contract: edit `CONTRACTS.md` first, bump its version line, note the change in its changelog, then tell all three agents.

The `ml-engineer` and `backend-engineer` never import or call each other's code. The Python pipeline runs offline and writes JSON to `artifacts/`; the Go service only reads it. They meet only at `artifacts/`, in the formats in `CONTRACTS.md` section 8.

Tasks marked `[HUMAN]` in `TASKS.md` are for the two human engineers. Do not attempt them and do not mark them done. They include anything needing PayPal sandbox credentials for the first time, checking model results, testing on a physical phone, and the video.

## Stack

- **Backend:** Go (current stable). Standard library `net/http` with its built-in routing. `database/sql` with the pure-Go SQLite driver `modernc.org/sqlite` (no cgo, so builds and deploys stay simple); the path comes from `DATABASE_URL`. Plain SQL migration files. Standard library `crypto/ecdsa` and `crypto/sha256` for witness verification. Standard library `net/http` client for PayPal. `log/slog` for structured logs. `go test` for tests. No web framework and no ORM unless you state a reason.
- **ML:** Python 3.11+, pandas or polars, numpy, scipy, statsmodels, pytest. No deep learning. Fixed random seeds.
- **Frontend:** React 18, TypeScript (strict), Vite, AG Grid Community (MIT). No AG Grid Enterprise features.
- **Local run:** a `docker-compose.yml` at the repo root builds and runs the backend and frontend, so a judge needs only Docker.
- **Artifacts:** one documented command regenerates everything in `artifacts/` from raw data.
- **Deploy:** Render (one web service for the backend, one static site for the frontend).

Do not add a framework or service not listed here without saying why in your report.

## Hard rules

1. **No invented numbers.** Every metric shown in the UI, the README or the video comes from a file in `artifacts/` that code in `ml/` generated from real data. Each artifact carries a `provenance` block (`CONTRACTS.md` section 8). If a number can't be computed yet, the UI shows "not computed", not a placeholder value.
2. **Label what is simulated.** Members, their loss amounts, and replayed outages are simulated. The UI says so wherever they appear. Historical outage data and model results are real and are labelled as real.
3. **No leakage.** Models are fitted on 2014–2022 and evaluated on 2023–2024. Nothing from the test years touches fitting, threshold selection or tuning. The split is enforced in one place in `ml/` and tested. Owner: ml-engineer.
4. **Idempotent money.** Every PayPal call that moves money carries the idempotency key defined in `CONTRACTS.md` section 7. The database enforces one payout per (policy, outage event). This applies to the Go service.
5. **Money is integer cents.** USD only. Never floats for amounts. In the Go service, amounts are `int64` cents; never `float64`.
6. **Sandbox only.** The Go service refuses to start unless `PAYPAL_BASE_URL` is `https://api-m.sandbox.paypal.com`.
7. **Secrets stay out of git.** Credentials live in `.env`. `.env.example` lists the names with empty values.
8. **A language model never sets an amount, a price or a claim decision.** Those come from the models and rules in `SPEC.md`.
9. **Framing.** FourHour is "the pricing, trigger and claims engine a licensed carrier would run; a sandbox prototype, not an insurance offer". Never write "this is not insurance".
10. **Check PayPal request shapes against the official docs** (developer.paypal.com) before coding a call. When a call fails, log the `debug_id` from the response and stop to diagnose. Do not retry with guessed variations.
11. **Don't claim what you haven't run.** Every report ends with three lists: what was run and passed, what was run and failed, what was written but not run.
12. **Commit messages and pull request text never mention AI tools, assistants, Claude or Anthropic, and never carry Co-Authored-By or 'Generated with' lines.** This applies to every agent.

## Committing and pushing

Every agent commits through `./scripts/push.sh`, never with a raw `git commit`. It stages everything, refuses `.env` files, database files, files over 50 MB and leaked PayPal credentials, writes and checks the message, then pushes. Pass a message as one quoted argument to use your own; use `--dry-run` to see the message and file list without committing. Commit and push only when the lead or a human asks.

## Things not yet verified

These are assumptions from research. Each has a spike in `TASKS.md` phase 0. Do not build on one until its spike passes.

- A merchant-initiated order against a vaulted PayPal wallet completes without the buyer present in sandbox.
- A Payouts batch to a sandbox personal account reaches SUCCESS.
- EAGLE-I yearly files download and parse with the columns we expect.
- The ESMI India dataset downloads without a login and parses.
- The demo phone's browser reports charger connect and disconnect events.
- The licences of EAGLE-I and ESMI allow committing small derived slices to a public repo.

## Environment variables

Defined in `CONTRACTS.md` section 2. The backend reads them through one config package; nothing else calls `os.Getenv`.
