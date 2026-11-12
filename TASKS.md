# FourHour: tasks

Tag `[STRETCH]`: done only if the core loop and evidence are finished first.

Owners: `[BE]` backend-engineer (Go), `[ML]` ml-engineer (Python), `[FE]` frontend-engineer, `[LEAD]` main session, `[HUMAN]` the two human engineers.

**Phases 2, 3 and 4 can now all run in parallel.** Backend, frontend and ML are independent until integration: the backend and ML meet only at `artifacts/` (`CONTRACTS.md` section 8), and the frontend builds against its mock. Phase 5 also runs in parallel with phase 4. Other phases run in order.

Gate 2 decisions (2026-11-11): D1 gap bridging up to 2 h, D2 72-hour hours clause, D3 county customers from `MCC.csv`, D4 timestamps treated as UTC, D5 main replay is `derecho-harris-2024`, D6 one payout per (policy, event, tier) and 4-hour tier first, D7 P5 duplicate handling, D8 witness on any charger-connected browser with the Battery Status API (demo: MacBook), D9 stretch items. Written into `SPEC.md`, `CLAUDE.md` and `CONTRACTS.md` 1.3.

Mark a task done only when its "done when" check has actually been run and passed. Add the date and one line of evidence. If a check can't be run (no credentials, no phone), leave the task open and say what's missing.

## Phase 0: spikes

Each spike is one small program with its own README line saying how to run it and what a pass looks like. PayPal spikes are Go programs in `spikes/paypal/`, written by `[BE]`. Data spikes are Python scripts in `spikes/data/`, written by `[ML]`. The phone spike is an HTML page in `frontend/spikes/`, written by `[FE]`. `[HUMAN]` runs the ones that need credentials or hardware and records the result here.

- [x] **S1 `[BE]` write, `[HUMAN]` run: vault and merchant-initiated charge.** Go program in `spikes/paypal/`. Create a setup token, print the approval link, wait for approval, exchange for a payment token, create an order with `vault_id`.
  Done when: the order comes back `COMPLETED` with a `COMPLETED` capture and no buyer present for the charge. Record the order ID.
  If it fails: record the error and `debug_id`. Fallback is a buyer-approved order per premium; tell the lead before building anything on P4.
  Evidence (2026-11-11, run by humans on the real sandbox): order `9M67077598812253L` and capture `44X16900DL184870L` both `COMPLETED`, no buyer present, no `stored_credential` needed. Repeating with the same `PayPal-Request-Id` returned the ORIGINAL order, HTTP 200.
- [x] **S2 `[BE]` write, `[HUMAN]` run: payout.** Go program in `spikes/paypal/`. Send a $1.00 payout to a sandbox personal account, then poll it.
  Done when: batch status is `SUCCESS` and the recipient's sandbox balance went up. Then run it again with the same `sender_batch_id` and record what PayPal returns.
  If it fails: the product's money-out leg doesn't work. Stop and tell the humans.
  Evidence (2026-11-11, run by humans): batch `8S4CGLV8LRUB6` went PENDING → PROCESSING → SUCCESS in about 20–25 s; item `FBZGEZELPPVGL` SUCCESS. Repeat with the same `sender_batch_id`: HTTP 400 `USER_BUSINESS_ERROR`, `details[0].field` `SENDER_BATCH_ID`, issue "Batch with given sender_batch_id already exists", with a link to the original batch.
- [x] **S3 `[ML]`: EAGLE-I.** Python script in `spikes/data/`. Download one yearly file and the county-customers file. Print the columns, the row count for the three demo counties, and reconstruct events of 4 hours or more for Harris County in that year.
  Done when: the script runs from a clean checkout and prints an event list. Record the actual column names in `ml/README.md`.
  Evidence (2026-11-11): `ml/.venv/bin/python spikes/data/s3_eaglei.py` printed `S3 PASS` and the Harris 2024 event list (placeholder threshold 0.01, labelled). Column names recorded in `ml/README.md`.
- [x] **S4 `[ML]`: ESMI.** Blocked 2026-11-11: Dataverse requires a guestbook form (ID 251) for every download, API included. 2026-11-11: humans placed the location file and 12 voltage files (2.2 GB) in `ml/data/raw/esmi/`; the first run crashed (every date failed to parse). Re-run by `[ML]` in Gate 2. Python script in `spikes/data/`. Download the dataset from Harvard Dataverse, print its structure, count premises per district.
  Done when: at least one district has enough premises to build an index (state the number and the cut-off you chose).
  If it fails: the basis-risk study can't be done on this data. Tell the lead; do not substitute simulated data.
  Evidence (2026-11-12): ESMI parsed; 8 districts with at least 8 active monitors (cut-off `min_active_monitors` 8), e.g. Pune median 16; `basis_risk.json` built from them.
- [x] **S5 `[FE]` write, `[HUMAN]` run: phone charger events.** A single HTML page that shows charging state and logs every change with a timestamp.
  Done when: on the actual demo phone, unplugging and replugging the charger each produce a log line within a few seconds. Record phone model and browser.
  If it fails: the witness uses the manual `attest_*` fallback. Tell the lead.
  Evidence (2026-11-11, run by humans): the demo witness device is now a MacBook in Chrome 157 (decision D8; the only phone is an iPhone, and Safari has no Battery Status API). Unplug and replug were each reported about 3 s later; WebCrypto, hash chain and storage passed. Fixture: `frontend/spikes/fixtures/s5-mac-chrome.json`, checked by `verify-fixture.cjs`: `ALL OK`, 7 entries.
- [ ] **S6 `[HUMAN]`: licences.** 2026-11-12: humans decided to treat EAGLE-I as CC BY 4.0 and ESMI as CC0 1.0 per repository metadata; JSON artifacts and scenario files are committed with EAGLE-I attribution in the README; raw data never committed. Humans still to confirm on the licence pages. Read the licence terms on the EAGLE-I and ESMI download pages.
  Done when: recorded here whether small derived slices (scenario files, aggregates) may be committed to a public repo. If not, the repo ships download-and-build scripts only.
- [x] **S7 `[HUMAN]`: sandbox setup.** One sandbox business account with vault and Payouts enabled on the app, at least two sandbox personal accounts, a positive business balance, `.env` filled in.
  Evidence (2026-11-11, reported by humans): US business account with a balance, two US personal accounts, vault and Payouts enabled, `.env` filled in, plus `DEMO_VAULT_TOKEN`, `SANDBOX_MEMBER_EMAIL`, `SANDBOX_PAYOUT_EMAIL`.

## Phase 1: skeleton

- [x] **K1 `[LEAD]`:** `LICENSE` (MIT), `.gitignore`, `.env.example`, empty `README.md` with the section headings from `SPEC.md` section 10.
  Evidence (2026-11-11): `LICENSE` (MIT, doshikaran, 2026), `.env.example` (CONTRACTS 1.2 section 2 names, empty), `README.md` with the SPEC section 10 headings, `.gitignore`.
- [x] **K2 `[BE]`:** Go skeleton in `backend/`: Go module, config package, plain SQL migrations for every table in `CONTRACTS.md` section 3, `/api/health`, `go test` set up.
  Done when: `go test ./...` passes and the service refuses to start with a non-sandbox `PAYPAL_BASE_URL`.
  Evidence (2026-11-11): `go test -race ./...` in `backend/`: 5 packages ok; refusal tested by unit test and by hand (exit 1, no DB file). Caveat: Go 1.22.2 on macOS 26 needs `-ldflags=-linkmode=external` until Go is upgraded; without it 3 of 5 packages abort with `dyld: missing LC_UUID`.
- [x] **K2b `[ML]`:** Python project skeleton in `ml/` with the train/test split function, the artifact schema check, and pytest set up.
  Done when: `pytest` passes, including a test of the split function and a test that the schema check rejects a malformed artifact.
  Evidence (2026-11-11): `pytest` in `ml/`: 86 passed, including split-guard tests and 25+ malformed-artifact cases.
- [x] **K3 `[FE]`:** frontend project with the routes in `CONTRACTS.md` section 9 as empty pages, a typed API client generated or hand-written from section 4, and a mock layer serving fixture responses for every endpoint and a scripted SSE stream.
  Done when: `npm run build` passes with strict TypeScript and every route renders against the mock.
  Evidence (2026-11-11): `tsc --noEmit` OK, `npm run build` OK, `vitest`: 47 passed, including every section 9 route rendered against the mock; routes also loaded in headless Chrome via `vite preview`.
- [x] **K4 `[LEAD]`:** read all three skeletons (K2, K2b, K3) against `CONTRACTS.md`. Fix any mismatch in the code, or change the contract by the process in `CLAUDE.md`.
  Evidence (2026-11-11): Migration columns match section 3 exactly (scripted diff). Agent questions answered in CONTRACTS 1.2; backend, frontend and ML re-aligned and re-tested (BE 5 ok, FE 47 passed, ML 86 passed).
- [CUT 2026-11-12] **K5 `[LEAD]` `[STRETCH]`:** Deferred at Gate 1. `docker-compose.yml` at the repo root that builds and runs the backend and frontend, with `artifacts/` mounted read-only into the backend. Each service's Dockerfile is written by its owner (`[BE]`, `[FE]`).
  Done when: `docker compose up` serves the app from a clean clone.

## Phase 2: backend core loop

- [x] **B1 `[BE]`:** PayPal client: P1–P8, with token caching, idempotency headers, `debug_id` logging, typed result structs. `go test` unit tests against recorded responses served by a local test server.
  Evidence (2026-11-12, Windows): `go test -race -count=3 ./...` all ok; sandbox run below exercised P1, P4, P5, P6.
- [x] **B2 `[BE]`:** members, policies, vault return, first premium, charge-premium, with the write order from section 7.
  Done when: test proves a repeated charge for the same period creates no second row and no second PayPal call.
  Evidence (2026-11-12): `TestChargeOncePerPeriodAndRowUnique` passes (-race); sandbox charge order `5UG31672BS8343058`, capture `1E8763885K549590F` COMPLETED.
- [x] **B3 `[BE]`:** replay engine and SSE stream reading `artifacts/scenarios/`. Until real scenario files exist, use a fixture clearly named `fixture-*` that the scenarios endpoint marks as a fixture. Tests run with `go test -race`.
  Evidence (2026-11-12): -race ok; `TestParityDerechoHarris2024` shows the backend gives the ML run on the real derecho file (one theta_lo run 2024-05-17T00:15Z to 19:30Z, 4 dropouts, no theta_hi run).
- [x] **B4 `[BE]`:** witness registration, entry verification (hash, chain, signature in raw r‖s form, ordering), outage intervals.
  Done when: tests pass against fixtures produced by real browser WebCrypto, including a tampered entry, a broken chain and a replayed `seq`. Verification splits the 64 raw signature bytes into r and s and checks them with `crypto/ecdsa`. The hash input is rebuilt by the explicit string builder from `CONTRACTS.md` section 6, not `encoding/json`, with test cases for `battery_level` `1`, `0.5`, `0.83` and `null`.
  Evidence (2026-11-12): real-browser fixture (MacBook Chrome), tampered, broken chain, replayed seq, stale tests pass with -race.
- [x] **B5 `[BE]`:** rule engine implementing the table in `SPEC.md` section 4, including the 24-hour top-up. Build the 4-hour tier end to end first; add the 24-hour top-up only after the loop passes (D6). Events follow the hours clause (D2).
  Done when: one test per rule, plus a test that one outage event can never produce two payouts for the same tier.
  Evidence (2026-11-12): one test per rule plus one-payout-per-tier pass; 24-hour top-up CUT, `TestNo24HourTopUp` proves only t4 is decided.
- [x] **B6 `[BE]`:** payouts, webhook endpoint with verification and de-duplication, polling fallback, ledger. Tests run with `go test -race`.
  Evidence (2026-11-12): webhook verify/dedupe, polling and ledger tests pass with -race. Sandbox (FOURHOUR_SANDBOX_IT=1, run 3 of 5): claim `clm_ytbevero7t55` R2 30 cents, payout batch `G6NW9GX8C8K98` item `2CUWU899JMFXG` SUCCESS via poll; ledger balance 70 cents.
- [x] **B7 `[BE]`:** claims, ledger and analytics endpoints; cancel with pro-rata refund. `[STRETCH]` for cancel and refund (D9).
- [ ] **B8 `[HUMAN]`:** run the loop against the sandbox: enrol, charge, replay, payout, confirmation.
  Done when: the payout shows in the sandbox personal account and the claim page shows it confirmed.

## Phase 3: frontend core flows

- [x] **F1 `[FE]`:** design tokens, layout, and shared states (loading, empty, error, "not computed", "simulated" and "real data" labels).
  Evidence (2026-11-12): typecheck clean, vitest 102 passed, build OK; labels and "not computed" seen against the real backend.
- [x] **F2 `[FE]`:** `/` and `/enrol` through the PayPal hand-off and back.
  Evidence (2026-11-12): / and /enrol load against the real backend up to the PayPal hand-off; the hand-off itself is for the humans (real vault approval).
- [x] **F3 `[FE]`:** `/live`: index chart with both thresholds, the 4-hour clock, event feed, claims appearing as decided. Must survive a stream reconnect.
  Evidence (2026-11-12): paid derecho replay on /live (sandbox run 4 of 5): lo run opened at sim 2024-05-17T00:15Z, claim at 04:15Z, payout sent, confirmed by poll.
- [x] **F4 `[FE]`:** `/policies/:id` and `/claims/:id`.
  Evidence (2026-11-12): /policies/pol_bz7eouein5ad and /claims/clm_tr7vun2ixysq show the R3 claim and its confirmed payout.
- [x] **F5 `[FE]`:** `/witness/:policyId`: key generation, signing, hash chain, local queue, upload, manual fallback, own-status display.
  Done when: entries it produces pass the backend's verification, and the page keeps logging with the network off and uploads on reconnect.
  `[STRETCH]` (D9): the local queue and the network-off part of this check. Live logging and upload are not stretch.
  Evidence (2026-11-12): device dev_mpozsu6y54wg registered from the real page in headless Chrome; 7 entries accepted; a tampered entry was rejected `bad_signature`. Offline queue CUT. Self-reported buttons now shown in battery mode too.
- [x] **F6 `[FE]`:** `/operator` grids (AG Grid Community): policies, claims, ledger.
  Evidence (2026-11-12): /operator grids load the real policies, claims and ledger.
- [x] **F7 `[FE]`:** switch from the mock to the real backend.
  Done when: every route works against the running backend with the mock disabled.
  Evidence (2026-11-12): all 8 routes load in headless Chrome against the running backend with the mock off, with no console or HTTP errors.

## Phase 4: models and evidence

- [x] **M1 `[ML]`:** ingest EAGLE-I for TX, FL and IL, all years; event reconstruction; `data_quality.json`.
  Evidence (2026-11-12): `data_quality.json` regenerated from clean commit e9fbacd (all 8 artifacts validate, `pytest` 148 passed); 4,653 county-years, 54 excluded below the 0.70 floor.
- [x] **M2 `[ML]`:** trigger design → `trigger_params.json`.
  Evidence (2026-11-12): `trigger_params.json` theta_lo 0.20, theta_hi 0.90 (`grid_edge_no_interior_optimum`), 190-pair grid, provenance e9fbacd.
- [x] **M3 `[ML]`:** frequency, severity, credibility; out-of-sample evaluation → `backtest.json`.
  Done when: a test fails if any 2023–2024 row reaches a fitting function.
  Evidence (2026-11-12): `backtest.json` provenance e9fbacd; NB does not beat baselines (deviance 130.2331 vs Poisson 130.2164); leakage test passes.
- [x] **M4 `[ML]`:** pricing → `pricing_table.json`; solvency → `solvency.json`.
  Evidence (2026-11-12): `pricing_table.json` model_version 2026-11-12-e9fbacd; `solvency.json` ruin 0.1229, 1-in-200 capital 1,686,492 cents.
- [x] **M5 `[ML]`:** scenario files, including selecting the `local-outage` window from the data or reporting that none exists.
  Evidence (2026-11-12): `derecho-harris-2024` and `spoof-quiet` regenerated at e9fbacd and now committed; local-outage and Beryl CUT.
- [x] **M6 `[ML]`:** ESMI basis-risk study → `basis_risk.json`.
  Evidence (2026-11-12): `basis_risk.json` e9fbacd: index_only missed 0.704 / wrong 0.353; rules with perfect witness missed 0.059 / wrong 0.0.
- [CUT 2026-11-12] **M8 `[ML]` `[STRETCH]`:** EAGLE-I 2025 as an extra hold-out year (D9).
- [ ] **M7 `[HUMAN]`:** review every artifact. Check the split, read the numbers for plausibility, re-run one end to end.
  Done when: a human has signed off here, with any concerns listed. Nothing from `artifacts/` goes into the README or video before this.

## Phase 5: evidence in the UI

- [x] **E1 `[FE]`:** `/evidence` panels reading the analytics endpoints, each with its provenance visible. Missing artifacts show "not computed".
  Evidence (2026-11-12): six panels read the analytics endpoints with provenance; theta_hi shown as the grid edge; nulls show "not computed".
- [x] **E2 `[FE]`:** quote breakdown on `/enrol` driven by the real pricing response.
  Evidence (2026-11-12): Harris $150 quote shows $2.70/month, matching POST /api/quotes (270 cents, expected payout 1377 cents).

## Phase 6: ship

- [x] **H1 `[LEAD]`:** README per `SPEC.md` section 10, with every number read from `artifacts/`.
  Evidence (2026-11-12): README written from artifacts at provenance e9fbacd, all nine SPEC section 10 sections in order. Pending M7: humans must sign off the numbers before submission.
- [ ] **H2 `[LEAD]`:** deploy backend and frontend on Render; register the webhook to the deployed URL.
  Prepared (2026-11-12): `render.yaml` and `DEPLOY.md` written, not run. Account steps are for the humans.
- [ ] **H3 `[HUMAN]`:** clean-clone test: follow the README on a fresh machine.
- [ ] **H4 `[HUMAN]`:** run the three scenarios end to end on the deployed app, three times.
- [ ] **H5 `[HUMAN]`:** record and upload the video; submit on Devpost.

## If something must be cut

Follow the cut order in `SPEC.md` section 9. Record each cut here with the reason, and remove it from the README and UI so nothing claims a feature that isn't there.

Cuts made 2026-11-12 by the humans (deadline 01:30 IST 13 Nov; build frozen about 15:30 IST):

- [CUT 2026-11-12] The 24-hour top-up payout (B5 second tier). Not tested end to end. The rule engine pays the 4-hour tier only. The premium still includes the t24 term from `pricing_table.json`; the README states this as a limitation.
- [CUT 2026-11-12] Cancel with pro-rata refund (B7 stretch). Backend code may remain; no UI or README mentions it.
- [CUT 2026-11-12] Offline queueing on the witness page (F5 stretch). Live logging and upload stay.
- [CUT 2026-11-12] `docker-compose.yml` (K5). The README gives native run steps.
- [CUT 2026-11-12] The `local-outage` and `beryl-harris-2024` scenarios (M5). Beryl stays in the backtest with its feed gap disclosed.
- [CUT 2026-11-12] EAGLE-I 2025 hold-out (M8).
