# FourHour: product spec

## 1. The problem

USDA guidance says a closed refrigerator keeps food safe for about 4 hours without power; after that, perishable food should be discarded. A food truck owner, home baker or corner grocer with no generator loses their stock in a short outage. Ordinary business cover is built for long outages and pays slowly.

Source to cite in the README and UI: USDA FSIS, "Keep Your Food Safe During Emergencies" (fsis.usda.gov). Quote it accurately and link it. Do not paraphrase it into a stronger claim.

## 2. The user

**Member:** a US micro food seller with perishable stock and no generator. Demo personas are in three counties:

| County | FIPS | Why |
|---|---|---|
| Harris County, TX | 48201 | The Houston derecho, May 2024, is the main replay |
| Miami-Dade County, FL | 12086 | High data coverage, hurricane exposure |
| Cook County, IL | 17031 | High data coverage, different climate |

**Operator:** the carrier or program administrator running the pool. They see the ledger, claims, and the analytics panels.

## 3. What the product does

1. **Quote.** The member picks a county and a cover amount ($50, $150 or $300). The pricing model returns a monthly premium and shows how it was built.
2. **Enrol.** The member saves their PayPal wallet (vault). The first premium is charged.
3. **Register a witness.** The member opens the witness page on a phone that stays on a charger at their premises. The phone creates a key pair and registers its public key.
4. **Monitor.** The system tracks the county outage index. The phone logs charger state.
5. **Decide.** When an outage qualifies under the trigger rules in section 4, a claim is created automatically.
6. **Pay.** A PayPal Payout is sent to the member. A webhook confirms it.
7. **Explain.** The claim page shows which rule fired, the index curve, the witness log, and the payout status.

## 4. Trigger rules

Two signals:

- **Index:** the share of a county's electricity customers without power, from DOE EAGLE-I 15-minute data. An index event is open while the share stays at or above a threshold.
- **Witness:** the member's phone reports that its charger lost power, in a signed, hash-chained log.

Two index thresholds, `theta_hi` (strong) and `theta_lo` (lenient), with `theta_lo < theta_hi`. Both are chosen by the trigger-design step in `ml/` and read from `artifacts/trigger_params.json`. They are never hard-coded.

"Duration" below means continuous hours. For the index it is hours at or above the threshold. For the witness it is hours between a `power_lost` entry and the next `power_restored` entry.

| Rule | Index | Witness | Result |
|---|---|---|---|
| R1 | at or above `theta_hi` for 4h or more | confirms 4h or more, overlapping the index event | Pay full schedule |
| R2 | at or above `theta_hi` for 4h or more | no device registered, or no log received | Pay 50% of schedule |
| R3 | at or above `theta_lo` for 4h or more, but below `theta_hi` | confirms 4h or more, overlapping | Pay full schedule |
| R4 | at or above `theta_hi` for 4h or more | log shows power stayed on | No payout. Claim recorded as declined with reason |
| R5 | below `theta_lo` | reports an outage | No payout. Flagged for review |

The principle: **the witness can confirm or contradict an index event; it can never create one.** A phone unplugged by hand while the grid is fine pays nothing (R5). That is the fraud control.

Why R3 exists: a county index can stay low while one neighbourhood is dark. R3 is what lets the witness reduce missed payouts, not only wrong ones. The basis-risk study in section 6 measures how much.

**Payout schedule**, as a share of the chosen cover amount:

| Outage duration | Share | Basis |
|---|---|---|
| 4h or more | 60% | Refrigerated stock (USDA 4-hour guidance) |
| 24h or more | 100% | Frozen stock as well (USDA freezer guidance; confirm the exact wording on the FSIS page before citing) |

The 60/40 split is a product assumption. Say so in the UI.

**Hours clause.** Qualifying runs in the same county that start within 72 hours of the previous run's end are one outage event. 72 hours is a product parameter, like the hours clause in a catastrophe cover, not a fitted value. It is written to `artifacts/trigger_params.json` as `event_merge_hours`.

One payout per policy, per outage event, per tier. If an outage first qualifies at 4h and later passes 24h, the second payment is the difference, sent as a separate payout with its own key (see `CONTRACTS.md` section 7).

Known limitation to state in the README: under R2 a member with no witness still gets 50%, and under R4 a member with a witness can get nothing. That could discourage registering a witness. A production design would price the two cases differently.

## 5. Pricing and solvency models

All fitted on 2014–2022, evaluated on 2023–2024.

**5.1 Event reconstruction.** From EAGLE-I 15-minute rows per county: compute share out = customers out / county customers. Build events as runs at or above a threshold. Missing data never ends an event: a run of missing 15-minute intervals of up to 2 hours is bridged when the readings on both sides are at or above the threshold. An event ends on an observed reading below the threshold, or on a gap longer than 2 hours. The 2-hour bridge is checked against the gap lengths inside large events in the training years only; if that evidence shows it is clearly wrong, a different value is proposed with the evidence, never tuned on 2023–2024. Whether zero-outage rows are recorded is checked per year (the 2024 file records them). EAGLE-I has scrape gaps and partial-feed dropouts during some large events.

County customers come from the dataset's county customer file (`MCC.csv`), not a yearly `total_customers` column. The share is capped at 1.0, and every county-year where customers out exceeds the county count is listed in `data_quality.json`. Timestamps are treated as UTC; this is checked against a known storm (the Houston derecho struck in the early evening local time on 16 May 2024) and the finding is reported. Runs are then merged into events by the hours clause in section 4. Detect gaps, report how many intervals are missing per county-year, and exclude county-years below a coverage floor from fitting. Report results with and without the exclusion.

**5.2 Frequency.** Count of qualifying events per county-month, after merging by the hours clause. Negative-binomial GLM with month-of-year and state effects. Compare against two baselines on the test years: a Poisson GLM and the county's historical mean rate. Report predicted versus realised counts, out-of-sample deviance, and a calibration table.

**5.3 Severity.** Event durations above 4 hours. Fit a generalised Pareto distribution to the excess. Use it for P(duration at or above 24h | event). Report a QQ plot's data and a goodness-of-fit statistic.

**5.4 Credibility.** Bühlmann–Straub: blend each county's own rate with its state's rate. Report the credibility weight per county.

**5.5 Premium.** Monthly premium = (expected annual payout / 12) × (1 + risk margin) + expense load. Expected annual payout = expected qualifying events × expected payout share × probability the member's premises are affected given an index event. Version 1 estimates that last probability as the mean share of customers out during event windows. The risk margin comes from the tail of the simulated annual loss (5.7). State every component in the quote response so the UI can show the breakdown.

**5.6 Trigger design.** Choose `theta_hi` and `theta_lo` on the training years by grid search, trading expected wrong payouts against expected missed payouts. Write the chosen values and the full grid to `artifacts/trigger_params.json`.

**5.7 Solvency.** Simulate a pool (default 1,000 members spread over the three counties) for one year, many times. One county event hits every member in that county at once, so resample whole historical years per county to keep storm clustering. Report probability of ruin, capital needed for a 1-in-200 year, and the price of an aggregate stop-loss layer.

**5.8 Backtest.** Run the pool through 2023 and 2024 with the frozen models and thresholds. Report premiums collected, payouts, loss ratio, and events per county. Hurricane Beryl (Harris County, July 2024) falls in the test years: the model never saw it.

## 6. Basis-risk study (India, ESMI)

The objection this answers: "a county outage count doesn't prove my fridge was off."

Prayas ESMI recorded minute-level supply at individual premises in India (Harvard Dataverse, doi:10.7910/DVN/CLLZZM). For each district with enough monitors:

1. Build a district index: share of monitors without supply, per minute.
2. For each premises, find its true outages of 4 hours or more.
3. Score two schemes against that truth: index-only (`theta_hi`) and the full rule table in section 4.
4. Report, per scheme: missed payouts (premises out 4h or more, not paid) and wrong payouts (paid, premises had power).

In this study the "witness" is the premises' own monitor, so it is perfect by construction. That makes the result an upper bound. Also run it with a share of witness logs missing at random (10% and 30%) and report all three. Say this plainly in the artifact's `notes` field and in the UI.

Thresholds for this study are chosen on the first half of the ESMI period and scored on the second half.

## 7. Demo scenarios

Replays of real EAGLE-I windows, precomputed by `ml/` into `artifacts/scenarios/`. The replay runs on a simulated clock at a chosen speed.

| Scenario | Window | Shows |
|---|---|---|
| `derecho-harris-2024` | Harris County, May 2024 (Houston derecho) | R1: index and witness agree, payout fires at hour 4, top-up at hour 24 |
| `spoof-quiet` | A quiet week in any demo county | R5: phone unplugged, index quiet, no payout |
| `local-outage` | A window where the share sits between `theta_lo` and `theta_hi` for 4h or more | R3: witness upgrades a lenient index event |

Hurricane Beryl (Harris County, July 2024) stays in the backtest with its feed gap disclosed: the feed is missing its onset. It becomes a second scenario, `beryl-harris-2024`, only if its onset can be shown without inventing data. Before building scenario files, `ml/` reports data completeness for both storm windows.

`ml/` selects the `local-outage` window from the data after thresholds are chosen. If no such window exists in the demo counties, say so and drop the scenario; do not construct one.

**Clocks in demo mode.** The replay clock is simulated; the phone's clock is real. During a replay, the server stamps each incoming witness entry with the current simulated time and uses that for rule evaluation. The entry keeps its real device time and signature. The UI and README say that replay mode maps live witness events onto the replay clock.

## 8. Witness

A page in the frontend, opened on any device that stays on a charger at the premises and runs a browser with the Battery Status API, such as an Android phone or a laptop in Chrome. The demo uses a MacBook in Chrome. It:

- Generates an ECDSA P-256 key pair with WebCrypto and registers the public key.
- Watches charger state through the Battery Status API where the browser supports it.
- Appends signed, hash-chained entries (format in `CONTRACTS.md` section 6), stores them locally, and uploads them when online. It keeps logging while offline.
- Shows its own state: charging or not, entries queued, last upload.

Fallback when the Battery Status API is missing: a "Power is out" / "Power is back" button pair. Entries made this way have kind `attest`, and the UI labels them "self-reported". Self-reported entries are treated the same as sensed ones by the rules; the README says so. On iOS, where Safari has no Battery Status API, the page offers self-reported entries only.

Witness outage intervals come only from `power_lost` and `power_restored` entries (or their `attest_*` forms), never from missing heartbeats: a hidden tab or a sleeping device stops heartbeats without any loss of power.

## 9. Scope

**In:** quote, enrol with vault, premium charge, witness, replay, rule engine, automatic payout, webhook confirmation, cancel with pro-rata refund, claims view, ledger view, analytics panels (backtest, pricing breakdown, solvency, basis risk).

**Out:** Venmo payouts, Log in with PayPal, multiple perils, real-time EAGLE-I, native mobile app, Bluetooth thermometers, user accounts with passwords, any LLM feature.

**Cut order if something must go** (first cut first): cancel and refund; `local-outage` scenario; solvency panel; offline queueing on the witness page (keep live logging); the 24-hour top-up payout. Never cut: the vault → premium → replay → payout → webhook loop, the out-of-sample backtest, the basis-risk result.

## 10. README requirements

Judges may never run the app, and part of judging may be automated. The README must contain, in this order:

1. One-paragraph description: problem, user, what happens.
2. A table of every PayPal API call used, with endpoint, purpose, and where it is in the code.
3. The AI section: each model, the data it was fitted on, and its out-of-sample result, with numbers read from `artifacts/`.
4. "What is real and what is simulated."
5. Data sources with links, licences and known gaps.
6. Limitations, including basis risk, EAGLE-I gaps, the witness being spoofable on its own, and the R2/R4 incentive issue.
7. Framing statement from `CLAUDE.md` rule 9.
8. Run instructions that work from a clean clone, including sandbox test accounts.
9. Licence (MIT, with a `LICENSE` file at the repo root so GitHub shows it).

## 11. Video storyboard (under 3 minutes)

- **0:00–1:00.** The USDA quote over a fridge. A seller in Harris County gets a quote with its breakdown, saves PayPal, first premium shows completed.
- **1:00–2:00.** Replay the Houston derecho at speed. The index climbs. The witness laptop is unplugged on camera and its log appears. The 4-hour clock runs out, the claim fires, the payout lands in the sandbox account. Then the spoof scenario is declined with its reason.
- **2:00–3:00.** Backtest on years the model never saw. Basis-risk result on Indian premises data. Limitations card. Close.
