# Independent numbers audit

**Date:** 2026-11-12, at repo HEAD `5d5f4e4`. The artifacts are from `0938669`; `ml/fourhour_ml/` is unchanged since then.

**Reviewer:** a fresh reviewer that had not worked on the project and was given only the repo. It wrote its own code for every recomputation, reading the project's code only to learn definitions. It made no repo edits and no network calls.

**Raw data:**
- EAGLE-I: rows for TX, FL and IL were re-extracted from all 11 raw files. Row counts match the manifests every year (2024: 4,803,738). Training `rows_used` match provenance.
- SHA-256 hashes were spot-checked on 5 raw files; all match provenance.
- ESMI `rows_used` match for all 10 files.

The lead's fixes are listed at the end.

## 1. Recomputed values

| Quantity | Artifact | Audit | Result | Depth |
|---|---|---|---|---|
| Basis risk, index only: missed / wrong | 0.703792 / 0.352548 (3526/5010, 844/2394) | same counts | match | raw ESMI |
| Basis risk, perfect witness | 0.059281 / 0.0 (297/5010, 0/4135) | same | match | raw ESMI |
| Basis risk, 10% logs missing | 0.109182 / 0.016717 | same (same seed and draw order) | match | raw ESMI |
| Basis risk, 30% logs missing | 0.23493 / 0.060367 | same (same seed and draw order) | match | raw ESMI |
| ESMI thresholds, districts, periods | lo 0.1, hi 0.5; 8 districts, 121 monitors | identical | match | raw ESMI |
| Pool loss ratio 2023 | 0.0 | 0.0 | match | raw events + seed |
| Pool loss ratio 2024 | 1.317051 (351 claims, 3,495,000 c) | 1.3170509, same claims and amount (analytic expectation 1.336) | match | raw events + seed |
| Solvency | ruin 0.1229, 1-in-200 capital 1,369,344 c, stop-loss 2,628,000 / 195,000 / 6,290 c | identical in every figure | match | raw + seed |
| Frequency, Poisson GLM | deviance 130.216402, log score 0.157983 | identical | match | raw (refit) |
| Frequency, NB GLM | deviance 130.233104, alpha 8.0e-5 | 130.216398, alpha 3.4e-7 (own MLE) | **differs: optimiser, not data** | raw |
| Frequency, historical mean | infinite | infinite | match | raw |
| Event counts | 34 train / 19 test; 144 fitted county-years; 54 excluded | identical | match | raw |
| Harris premiums $50 / $150 / $300 | 136 / 209 / 319 c | identical (136.4576 / 209.3728 / 318.7455 before rounding) | match | raw (all inputs) |
| theta_lo / theta_hi | 0.20 / 0.90, objective 0.078598 | identical; all 190 grid pairs within 5e-7 | match | raw |
| Severity | GPD shape −0.638346, KS 0.146 p 0.42, P24 0.81 / 0.82 / 0.68 | identical | match | raw |
| Harris / Uri finding | peak 0.2495, longest run 2.75 h | identical | match | raw |

The NB fit converges to alpha ≈ 0. There its deviance is 4e-6 *below* Poisson, while the artifact's is slightly above. "Did not beat its baselines" is therefore best read as "indistinguishable from Poisson".

## 2. Leakage

**No 2023 or 2024 row reaches fitting, threshold selection or tuning.** The reviewer traced each path by reading the code:
- **Split:** the years are set in `config.py:14-15`.
- **Training-year filters:** `build.py:52-68` (`fitted_county_years`, `readings_for`, `train_readings`).
- **Thresholds:** `trigger.grid_search_components` is `@train_only` (`trigger.py:79`).
- **Models:** `fit_frequency`, `fit_severity` and `fit_credibility` are `@train_only` (`models.py:61, 126, 154`). They are called with training data at `build.py:481-502`. Test data goes only to prediction and evaluation.
- **Pricing and solvency:** both use training events only (`build.py:584-613`).
- **Backtest:** applies test events to frozen premiums (`build.py:738-766`).
- **ESMI:** thresholds are chosen on the first half (`basis.py:183-227`) and scored on the second (`basis.py:246-250`).

Weaknesses that are not row leaks:
1. **The event rules saw a test-year storm.** The dropout rule and the 2-hour gap bridge were changed after the old rule split Hurricane Beryl (July 2024) in two, and the UTC check used the 2024 derecho. *Now disclosed in the README.*
2. **The guard checks only pandas arguments.** `score_grid`, `chosen_thresholds`, `solvency_run` and `basis.choose_thresholds` are undecorated. They are train-only by construction, not by enforcement.
3. **Product cuts came after backtest results were visible.** The 24-hour tier and the not-offered rule were cut then. This made the 2024 loss ratio worse (1.290 → 1.317), not better.
4. **ESMI district eligibility uses monitor availability in both halves.** It never uses outcomes.

## 3. README and evidence page against the artifacts

The evidence page reads every number live from `/api/analytics/*`, which serves the artifact bytes unchanged. It has no hard-coded numbers.

README mismatches found:

| # | Where | Finding | Fixed |
|---|---|---|---|
| 1 | README "Event reconstruction" | "54 of 4,653 county-years": misleading denominator; the 54 are out of 198 training-year universe county-years | yes |
| 2 | README "Frequency" | historical mean predicts zero for Harris **and Hidalgo** | yes |
| 3 | README "Frequency" | NB vs Poisson gap is optimiser noise | yes, disclosed |
| 4 | README "Trigger design" | "pushes index-only payment towards never paying" contradicts the artifact notes, where never paying scores worse (0.0845) than the choice (0.0786) | yes, removed |
| 5 | README "R2/R4 incentive" | R2 written as "50%" (it is 50% of the schedule = 30% of cover) | yes |
| 6 | README "basis risk caveats" | "at least 8 active" monitors: four districts have a median below 8 | yes |

## 4. Claims the artifacts do not support

| # | Claim | Finding | Action |
|---|---|---|---|
| 1 | "evaluated on 2023–2024, which they never saw" | event rules saw Beryl 2024 | rewritten, disclosed |
| 2 | "the split is enforced in one place (`split.py`)" | years in `config.py`, filtering in `build.py`; `split_by_year` is unused | rewritten |
| 3 | "R2 almost never pays" | 8 theta_hi events in 144 training county-years; 4 in 44 test county-years | now "rarely pays (major hurricanes only)" |
| 4 | "most index-only payments would go to premises that had power" (README and `frontend/src/components/ui.tsx:348`) | at 0.90, theta_hi events have mean share about 0.96–1.0, so most R2 payments would be correct | README fixed; UI copy: see below |
| 5 | Basis-risk headline | (a) the premises' own monitor is inside its district index: excluding it, the perfect-witness missed rate rises from 5.9% to **17.1%** (855/5010, audit figure); (b) Sitapur supplies 3,563 of 5,010 premises outages (71%); outside Sitapur, perfect-witness missed is **19.0%** (275/1447; Pune 48%) | caveats added to README qualitatively; audit figures here only, since they are not in an artifact |
| 6 | 10%-missing row | 10.9% is the lowest of 50 seeds (range 10.9–12.2%, mean 11.5%); the 30% row ranges 21.6–23.6% | README says "one random draw" |
| 7 | Solvency | the whole simulated loss comes from one training event (Irma, Miami-Dade, 2017). Harris adds none, but produced all of the 2024 loss. Ruin 12.29% ≈ the chance of drawing 2017 | disclosed in README |
| 8 | "Timestamps confirmed as UTC" | inferred from one storm's onset | now "judged" |
| 9 | "backend replay reproduces the pipeline's runs exactly (tested)" | not in any artifact; the reviewer did not run the Go test | kept; the test is `TestParityDerechoHarris2024` |
| 10 | "Did not beat its baselines" | supportable only as "indistinguishable" | rewritten |

## 5. What the reviewer could not verify

- **Gap-bridge evidence:** 97.2% of 104 events. Artifact only.
- **UTC check details and the derecho dropout description:** only the interval count and peak were recomputed.
- **Backend parity test and all PayPal or sandbox claims:** not run.
- **Exact random outputs:** solvency, backtest and the missing-log rows can be reproduced only by copying the draw protocol and seed. The independent checks were analytic expectations and the spread across 50 seeds.
- **NB GLM to 6 decimals:** depends on the statsmodels optimiser.
- **Hashes of the remaining raw files:** covered only by exact row-count matches.
- **Whether human choices preceded seeing outcomes:** the 0.70 coverage floor, the dropout fraction, the 8-monitor cut-off and the 24-hour cut. Only docs and git history were available.

## Lead's actions

- **README:** rewritten for findings 3.1–3.6 and claims 4.1–4.3, 4.5–4.8 and 4.10.
- **`TASKS.md`:** the stale t24 line is fixed.
- **`docs/DEVPOST.md` and `docs/VIDEO_SCRIPT.md`:** updated to match.
- **Not changed:** the artifacts and `ml/` code. The numbers reproduce, so nothing needed regenerating.
- **Open for the frontend owner:** the copy at `frontend/src/components/ui.tsx:348` (claim 4.4).
- **Open for the ML owner:** the undecorated selection functions (2.2). Separately, the basis-risk study should be re-run with each premises' own monitor left out of its district index (4.5a). That is a model change, not something to make on the deadline night.
