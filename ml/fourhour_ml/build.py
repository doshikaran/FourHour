"""Builds every artifact from raw data. Entry point: ``ml/build_artifacts.py``.

Each ``build_*`` function computes an artifact document from data and code only, then
``write_artifact`` validates it against CONTRACTS.md 8.1 before writing.
"""

from __future__ import annotations

import functools

import numpy as np
import pandas as pd

from . import panel, quality, trigger
from .config import (
    COVERAGE_FLOOR,
    DROPOUT_MAX_FRACTION,
    EVENT_MERGE_HOURS,
    FALSE_CONFIRMATION_RATE,
    GAP_BRIDGE_MINUTES,
    PRODUCT_PARAMS_SET_ON,
    SENSITIVITY_FALSE_CONFIRMATION,
    SENSITIVITY_WITNESS_SHARE,
    TEST_YEARS,
    TRAIN_YEARS,
    UNIVERSE_MIN_MEDIAN_COVERAGE,
    UNIVERSE_YEARS,
    WITNESS_SHARE,
)
from .schema import make_provenance

TRAIN = tuple(range(TRAIN_YEARS[0], TRAIN_YEARS[1] + 1))
TEST = tuple(range(TEST_YEARS[0], TEST_YEARS[1] + 1))


def iso(t) -> str:
    return pd.Timestamp(t).strftime("%Y-%m-%dT%H:%M:%SZ")


def r6(x) -> float | None:
    return None if x is None or (isinstance(x, float) and not np.isfinite(x)) else float(round(float(x), 6))


# ------------------------------------------------------------------ shared context


@functools.lru_cache(maxsize=1)
def coverage() -> pd.DataFrame:
    return panel.mark_exclusions(panel.coverage_table())


def fitted_county_years(include_low_coverage: bool = False) -> pd.DataFrame:
    cov = coverage()
    m = cov["in_model_universe"] & cov["year"].between(*TRAIN_YEARS)
    if not include_low_coverage:
        m &= ~cov["excluded_from_fitting"]
    return cov[m][["fips", "year", "state", "county", "coverage_share"]].reset_index(drop=True)


def readings_for(county_years: pd.DataFrame, years: tuple[int, ...]) -> pd.DataFrame:
    df = panel.readings(years)
    df = df.assign(year=df["t"].dt.year).merge(county_years[["fips", "year"]], on=["fips", "year"])
    return df[["fips", "t", "customers_out", "year"]]


@functools.lru_cache(maxsize=1)
def train_readings() -> pd.DataFrame:
    return readings_for(fitted_county_years(), TRAIN)


# ------------------------------------------------------------------ data_quality.json (M1)


def _frozen_runs(full: pd.Series, min_hours: float = 12.0) -> list[tuple[pd.Timestamp, pd.Timestamp, float]]:
    """Stretches where consecutive readings repeat the exact same non-zero value."""
    s = full.dropna()
    grp = (s != s.shift()).cumsum()
    out = []
    for _, g in s.groupby(grp):
        if g.iloc[0] > 0 and len(g) > 1:
            hrs = (g.index[-1] - g.index[0]) / pd.Timedelta(hours=1)
            if hrs >= min_hours:
                out.append((g.index[0], g.index[-1], float(g.iloc[0])))
    return out


def _dropout_readings(full: pd.Series) -> list[tuple[pd.Timestamp, float, float]]:
    s = full.dropna()
    prev = s.shift(1)
    m = (prev >= 20000) & (s <= 0.1 * prev)
    return [(t, float(prev[t]), float(s[t])) for t in s[m].index]


def _low_start(df24: pd.DataFrame) -> str:
    h = df24[df24["fips"] == "48201"].set_index("t")["customers_out"]
    daily = h["2024-06-01":"2024-07-08"].resample("D").median()
    low = daily < 20
    # start of the last run of low days before 8 July
    idx = low[::-1]
    start = None
    for day, v in idx.items():
        if v:
            start = day
        elif start is not None:
            break
    return str(start.date()) if start is not None else "none"


def _longest_run_hours(t_ns: np.ndarray, share: np.ndarray, theta: float, dropout_max_fraction: float | None) -> float:
    from .events import dropout_mask, find_runs

    if t_ns.size == 0:
        return 0.0
    keep = dropout_mask(t_ns, share, theta, GAP_BRIDGE_MINUTES, dropout_max_fraction)
    r = find_runs(t_ns[keep], share[keep], theta, GAP_BRIDGE_MINUTES)
    return float((r["end_ns"] - r["start_ns"]).max() / 3.6e12) if r["i0"].size else 0.0


HARRIS_URI_WINDOW = (pd.Timestamp("2021-02-14"), pd.Timestamp("2021-02-21"))  # Winter Storm Uri, a training year


def harris_training_finding() -> dict:
    """Why the historical-mean baseline predicts no Harris events: Harris's own training years
    (2014-2022 only) at theta_lo, with and without the dropout rule, and the Uri window."""
    fips = "48201"
    theta, _ = chosen_thresholds()
    cov = coverage()
    n = float(panel.county_customers()[fips])
    per_year = []
    for y in TRAIN:
        g = panel.readings((y,))
        g = g[g["fips"] == fips].sort_values("t")
        t_ns = g["t"].to_numpy("datetime64[ns]").astype(np.int64)
        sh = np.minimum(g["customers_out"].to_numpy(float) / n, 1.0)
        c = cov[(cov["fips"] == fips) & (cov["year"] == y)].iloc[0]
        per_year.append(
            {
                "year": y,
                "coverage": float(c.coverage_share),
                "excluded": bool(c.excluded_from_fitting),
                "max_share": float(sh.max()) if sh.size else 0.0,
                "run_dropout": _longest_run_hours(t_ns, sh, theta, DROPOUT_MAX_FRACTION),
                "run_plain": _longest_run_hours(t_ns, sh, theta, None),
            }
        )
    a, b = HARRIS_URI_WINDOW
    w = panel.readings((a.year,))
    w = w[(w["fips"] == fips) & (w["t"] >= a) & (w["t"] < b)].set_index("t")["customers_out"]
    expected = int((b - a) / pd.Timedelta(minutes=15))
    peak_t = w.idxmax()
    uri_above = int((w / n >= theta).sum())
    near = 0.8 * theta
    uri_near_h = float((w / n >= near).sum()) * 0.25
    used = [p for p in per_year if not p["excluded"]]
    excl = [p for p in per_year if p["excluded"]]
    any_used_4h = any(max(p["run_dropout"], p["run_plain"]) >= QUALIFY_HOURS for p in used)
    any_excl_4h = any(max(p["run_dropout"], p["run_plain"]) >= QUALIFY_HOURS for p in excl)
    best = max(used, key=lambda p: max(p["run_dropout"], p["run_plain"])) if used else None
    yrs = "; ".join(
        f"{p['year']}: coverage {p['coverage']:.3f}{' (excluded by the coverage floor)' if p['excluded'] else ''}, max share {p['max_share']:.4f}, longest run at or above {theta} {p['run_dropout']:.2f} h with the dropout rule and {p['run_plain']:.2f} h without"
        for p in per_year
    )
    if any_used_4h:
        verdict = "Harris has a qualifying run in a fitted training year; the zero-event result is not explained by the data and needs a code check."
    elif any_excl_4h:
        verdict = "The only Harris qualifying runs are in county-years excluded by the coverage floor, so the exclusion removes them."
    elif best is not None:
        verdict = (
            f"The feed has usable Harris data in {len(used)} fitted training years, and in none of them does the share stay at or above {theta} for {QUALIFY_HOURS} continuous hours; "
            f"the closest is {best['year']} ({max(best['run_dropout'], best['run_plain']):.2f} h). The dropout rule, gap bridging and coverage exclusion did not remove an event: "
            + (
                "the dropout rule lengthens the longest run rather than shortening it"
                if all(p["run_dropout"] >= p["run_plain"] for p in per_year)
                else "no year's longest run reaches 4 hours with or without the dropout rule"
            )
            + (f", and the excluded years never reach a share of {theta}." if all(p["max_share"] < theta for p in excl) else ".")
        )
    else:
        verdict = "Harris has no fitted training year."
    text = (
        f"Harris County (48201) has no theta_lo ({theta}) qualifying event in its 2014-2022 training years, which is why the historical-mean baseline predicts 0 for it. "
        f"{verdict} Winter Storm Uri window {a.date()} to {(b - pd.Timedelta(days=1)).date()}: {int(w.notna().sum())} of {expected} intervals present, peak {int(w.max()):,} customers out "
        f"({w.max() / n:.4f} of {int(n):,}) at {iso(peak_t)}, and only {uri_above} readings at or above {theta}, against {uri_near_h:.2f} hours of readings at or above {near:.2f}: the share hovers near {theta} but keeps dipping below it. "
        f"Per year: {yrs}. Checked on training years only; no threshold or rule was changed."
    )
    return {"id": "harris-training-years", "finding": text}


def build_data_quality() -> dict:
    cov = coverage()
    ys = panel.year_stats()
    cust = panel.county_customers()

    rows = []
    for r in cov.sort_values(["fips", "year"]).itertuples():
        rows.append(
            {
                "fips": r.fips,
                "county": str(r.county),
                "state": r.state,
                "year": int(r.year),
                "county_customers": int(r.county_customers),
                "expected_intervals": int(r.expected_intervals),
                "intervals_present": int(r.intervals_present),
                "coverage_share": r6(r.coverage_share),
                "zero_rows": int(r.zero_rows),
                "max_customers_out": None if pd.isna(r.max_customers_out) else r6(r.max_customers_out),
                "intervals_over_county_customers": int(r.intervals_over_county_customers),
                "in_model_universe": bool(r.in_model_universe),
                "excluded_from_fitting": bool(r.excluded_from_fitting),
                "exclusion_reason": r.exclusion_reason,
            }
        )
    year_summary = [
        {
            "year": s["year"],
            "rows": s["rows"],
            "zero_rows": s["zero_rows"],
            "null_rows": s["null_rows"],
            "off_grid_rows": s["off_grid_rows"],
            "first_t": iso(s["first_t"]),
            "last_t": iso(s["last_t"]),
        }
        for s in ys
    ]

    # D1 evidence, training years only, fitted county-years, before and after the dropout rule
    used = fitted_county_years()
    tr = train_readings()
    ev_after = quality.gap_bridge_evidence(tr, used, DROPOUT_MAX_FRACTION)
    ev_before = quality.gap_bridge_evidence(tr, used, None)
    gap_ev = {k: v for k, v in ev_after.items() if not k.startswith("_")}

    def gap_summary(ev: dict) -> str:
        g = pd.Series(ev["_gap_minutes"], dtype=float)
        sp = pd.Series(ev["_splits"], dtype=int)
        within = ev["share_of_gaps_within_bridge"]
        return (
            f"{ev['events_examined']} large events, {len(g)} missing gaps "
            f"(median {g.median():.0f} min, max {g.max():.0f} min, {within:.1%} within {GAP_BRIDGE_MINUTES} min, "
            f"{int(((g > 60) & (g < 240)).sum())} between 61 and 239 min), "
            f"{int((sp > 1).sum())} large events split into more than one run by the {GAP_BRIDGE_MINUTES}-minute rule"
        )

    drop = pd.Series(ev_after["_dropouts"], dtype=float)

    # D5 storm windows and D4 timestamp check
    df24 = panel.readings((2024,))
    storms, storm_series = [], {}
    for w in quality.STORM_WINDOWS:
        sw = quality.storm_window(df24, w)
        storm_series[w["id"]] = sw.pop("_series")
        storms.append(sw)
    der = storm_series["derecho-harris-2024"]
    ber = storm_series["beryl-harris-2024"]
    harris = int(cust["48201"])
    onset = quality.derecho_onset(der, harris)
    rise_t = pd.Timestamp(onset["first_sharp_rise_utc"].replace("Z", ""))
    local_hour = (rise_t - pd.Timedelta(hours=5)).hour
    consistent = 16 <= local_hour <= 20

    def describe_storm(sw: dict, full: pd.Series) -> str:
        fr = _frozen_runs(full)
        dr = _dropout_readings(full)
        parts = [
            f"{sw['intervals_present']} of {sw['expected_intervals']} intervals present; longest missing run {sw['longest_missing_minutes']} min",
            f"{len(dr)} dropout readings (a fall of 90% or more from 20,000 or more customers out): "
            + "; ".join(f"{iso(t)} {int(p):,} to {int(v):,}" for t, p, v in dr),
        ]
        for a, b, v in fr:
            parts.append(f"frozen feed: the same value {int(v):,} repeats from {iso(a)} to {iso(b)} ({(b - a) / pd.Timedelta(hours=1):.1f} h); no rule is applied to it")
        return ". ".join(parts) + "."

    storms[0]["notes"] = "Houston derecho, Harris County (test year: description only). " + describe_storm(storms[0], der)
    # Beryl onset: readings between landfall (about 09:00Z on 8 July) and the first reading of
    # 1% share or more.
    first_big = ber[ber / harris >= 0.01].index[0]
    pre = ber["2024-07-08T00:00":"2024-07-08T09:00"]
    storms[1]["notes"] = (
        "Hurricane Beryl, Harris County (test year: description only). "
        + describe_storm(storms[1], ber)
        + f" Onset missing from the feed: between 2024-07-08T00:00Z and 09:00Z (landfall about 09:00Z, 04:00 CDT) the readings are "
        f"{int(pre.min()):,} to {int(pre.max()):,} customers out ({int((pre == 0).sum())} zero rows), and the first reading at 1% share or more is "
        f"{iso(first_big)} ({int(ber[first_big]):,}). Harris readings have a daily median below 20 customers out on every day from {_low_start(df24)} up to the storm, so the feed was already near zero before landfall. The onset cannot be shown without inventing data, so Beryl is not a replay scenario; it stays in the backtest with this gap disclosed."
    )
    timestamp_check = {
        "assumed": "UTC",
        "storm": "Houston derecho 2024-05-16 (Harris County)",
        "first_sharp_rise_utc": onset["first_sharp_rise_utc"],
        "consistent_with_utc": bool(consistent),
        "evidence": (
            f"The first reading at 1% share or more that is ten times the reading before it is {onset['first_sharp_rise_utc']}, "
            f"which is {onset['local_cdt']} if the timestamps are UTC: early evening, when the derecho struck Houston. "
            "If the timestamps were local time, the rise would be at "
            f"{rise_t.strftime('%H:%M')} local, late at night, which does not match. Readings around it: "
            + ", ".join(f"{k} {v:,}" if v is not None else f"{k} missing" for k, v in onset["readings"].items())
            + ". Also, the 2014 file starts at 2014-11-01T04:00:00Z, which is midnight EDT."
        ),
    }

    # data_coverage per county: pooled coverage over 2015-2024 (2014's file starts in November)
    pooled = cov[cov["year"] >= 2015].groupby("fips")[["intervals_present", "expected_intervals"]].sum()
    county_coverage = {f: {"data_coverage": r6(r.intervals_present / r.expected_intervals)} for f, r in pooled.iterrows()}

    harris = harris_training_finding()
    d3 = cov[cov["intervals_over_county_customers"] > 0]
    u = cov[cov["in_model_universe"]].drop_duplicates("fips")
    excl = cov[cov["excluded_from_fitting"]]
    nulls = cov["max_customers_out"].isna().sum()
    zr = ", ".join(f"{s['year']}: {s['zero_rows']}" for s in ys)
    notes = " ".join(
        [
            "Real data: DOE/ORNL EAGLE-I county outage counts, Texas, Florida and Illinois, every county in MCC.csv for those states, 2014-2024.",
            f"Zero-outage rows per year: {zr}. Zero rows are not systematically published in any year (in 2022-2024 only a few hundred per county-year), so a missing 15-minute row may be a scrape gap or a true zero.",
            "coverage_share = intervals with a reading / intervals in the UTC calendar year; it is a lower bound on feed coverage and measures feed coverage only where some customers are out in nearly every interval.",
            f"Model universe (decided on training-year coverage only): counties whose median coverage over {UNIVERSE_YEARS[0]}-{UNIVERSE_YEARS[1]} is at least {UNIVERSE_MIN_MEDIAN_COVERAGE}: {len(u)} counties ({', '.join(u['county'] + ' ' + u['state'])}).",
            f"A universe county-year in 2014-2022 is excluded from fitting when coverage_share is below coverage_floor {COVERAGE_FLOOR}. 0.80 was tried first; before any model was fitted, the training coverage table showed feed-missing years at 0.69 or lower and ordinary years at 0.72 or higher, so the floor was set at 0.70. {len(excl)} county-years are excluded, including every 2014 county-year (the 2014 file starts 2014-11-01) and Harris 2015-2017 (coverage {', '.join(f'{v:.3f}' for v in cov[(cov.fips == '48201') & cov.year.between(2015, 2017)]['coverage_share'])}), so Hurricane Harvey (2017) is not in the Harris training data.",
            "Files for 2014-2021 end at 00:00 on 31 December; the rest of that day is missing.",
            f"Off-grid timestamps (scrape jitter) are moved to the start of their 15-minute slot: {sum(s['off_grid_rows'] for s in ys)} rows; {sum(s['off_grid_dropped_collisions'] for s in ys)} dropped because the slot already had a reading. Null customers_out rows are not readings.",
            f"D3: county customers from MCC.csv. {len(d3)} county-years have intervals where customers out exceeds the MCC count (share capped at 1.0); they are the rows with intervals_over_county_customers above 0. Among model-universe counties: "
            + ("; ".join(f"{r.county} {r.state} {r.year} (max {int(r.max_customers_out):,} vs {int(r.county_customers):,})" for r in d3[d3.in_model_universe].itertuples()) or "none")
            + ".",
            f"max_customers_out is null in {nulls} rows because the county has no reading in that year.",
            f"D1 gap evidence (training years, fitted county-years; large event = share at or above 1% traced with a 24-hour bridge, peak 5% or more, 4 h or more). Without the dropout rule: {gap_summary(ev_before)}. With the dropout rule (production, the values in gap_bridge_evidence): {gap_summary(ev_after)}.",
            f"Dropout-like episodes in the same county-years (a reading falling by 90% or more from a reading at 1% share or more, back to half the level within 2 h): {len(drop)}, median {drop.median():.0f} min, max {drop.max():.0f} min.",
            f"county_coverage.data_coverage is the pooled coverage_share over 2015-2024.",
            "Finding harris-training-years: " + harris["finding"],
        ]
    )
    sources = panel.data_source_entries(panel.ALL_YEARS, {s["year"]: s["rows"] for s in ys})
    return {
        "coverage_floor": COVERAGE_FLOOR,
        "rows": rows,
        "year_summary": year_summary,
        "gap_bridge_evidence": gap_ev,
        "storm_windows": storms,
        "timestamp_check": timestamp_check,
        "county_coverage": county_coverage,
        "findings": [harris],
        "provenance": make_provenance("ml/fourhour_ml/build.py:build_data_quality", sources, notes),
    }


# ------------------------------------------------------------------ trigger_params.json (M2)


@functools.lru_cache(maxsize=1)
def trigger_components():
    used = fitted_county_years()
    return trigger.grid_search_components(train_readings()[["fips", "t", "customers_out"]], len(used))


def theta_hi_status(theta_hi: float) -> str:
    return "grid_edge_no_interior_optimum" if theta_hi >= max(trigger.THETA_GRID) else "interior_optimum"


def sensitivity_table() -> pd.DataFrame:
    per, extra = trigger_components()
    rows = []
    for w in SENSITIVITY_WITNESS_SHARE:
        for e in SENSITIVITY_FALSE_CONFIRMATION:
            g = trigger.score_grid(per, extra, w, e)
            c = g[g["chosen"]].iloc[0]
            rows.append({"witness_share": w, "false_confirmation_rate": e, "theta_lo": float(c.theta_lo), "theta_hi": float(c.theta_hi), "theta_hi_status": theta_hi_status(c.theta_hi), "objective": r6(c.objective)})
    return pd.DataFrame(rows)


def chosen_thresholds() -> tuple[float, float]:
    per, extra = trigger_components()
    g = trigger.score_grid(per, extra, WITNESS_SHARE, FALSE_CONFIRMATION_RATE)
    c = g[g["chosen"]].iloc[0]
    return float(c.theta_lo), float(c.theta_hi)


def build_trigger_params() -> dict:
    per, extra = trigger_components()
    g = trigger.score_grid(per, extra, WITNESS_SHARE, FALSE_CONFIRMATION_RATE)
    c = g[g["chosen"]].iloc[0]
    sens = sensitivity_table()
    used = fitted_county_years()
    grid_max = max(trigger.THETA_GRID)
    at_edge = c.theta_hi == grid_max
    # flatness: best objective for each theta_hi
    best_hi = g.groupby("theta_hi")["objective"].min()
    never_r2 = (1 - WITNESS_SHARE) * extra["T"] + WITNESS_SHARE * per.at[c.theta_lo, "M"] + WITNESS_SHARE * FALSE_CONFIRMATION_RATE * per.at[c.theta_lo, "W"]
    notes = " ".join(
        [
            f"Chosen on training years only: {len(used)} fitted county-years (model universe, coverage floor, see data_quality.json), 2014-2022 rows only.",
            f"Events: runs at or above the threshold, missing gaps up to {GAP_BRIDGE_MINUTES} min and dropout readings (at most {DROPOUT_MAX_FRACTION} of the last reading at or above) bridged, a run lasting 4 h or more qualifies, qualifying runs within {EVENT_MERGE_HOURS} h merged (CONTRACTS 1.6 section 3).",
            f"Truth proxy (SPEC 5.5 version 1): a member at a random premises is out 4 h or more in an outage episode with probability equal to the episode's mean share; episodes are qualifying events at the lowest grid value {trigger.THETA_FLOOR}. Expected member outages of 4 h or more visible this way: {extra['T']:.4f} per member-year.",
            f"Grid values are per member-year in units of one full scheduled payout. No-witness members (share {1 - WITNESS_SHARE}): R2 pays 50% on theta_hi events; wrong = 0.5 x sum(1 - p_e) over theta_hi events, missed = outages not covered plus half of those covered. Witness members (share {WITNESS_SHARE}): R1/R3 pay in full when the premises was out and a theta_lo event overlaps; missed = outages with no theta_lo event; wrong = false_confirmation_rate x sum(1 - p_e) over theta_lo events with no theta_hi event (R3).",
            f"Product parameters set by the humans on {PRODUCT_PARAMS_SET_ON}: witness_share {WITNESS_SHARE}, false_confirmation_rate {FALSE_CONFIRMATION_RATE}, gap_bridge_minutes {GAP_BRIDGE_MINUTES}, event_merge_hours {EVENT_MERGE_HOURS}, dropout_max_fraction {DROPOUT_MAX_FRACTION}.",
            f"Result: the minimum is at theta_lo {c.theta_lo}, theta_hi {c.theta_hi} (objective {c.objective:.4f}). theta_hi_status is {theta_hi_status(c.theta_hi)}"
            + (": theta_hi is the edge of the grid, not a fitted optimum; it was accepted by the humans on 2026-11-12 (CONTRACTS 1.7)." if at_edge else "."),
            "Version 1 prices theta_lo events for a member with a registered witness (CONTRACTS 1.7); R2 cost is reported in backtest.json, not priced.",
            (
                f"theta_hi is at the top of the grid ({grid_max}) and the objective is nearly flat in theta_hi above 0.5 (best objective by theta_hi: "
                + ", ".join(f"{k}: {v:.4f}" for k, v in best_hi[best_hi.index >= 0.3].items())
                + f"). Never paying on the index alone (no R2) would score {never_r2:.4f}. Reading: under this proxy an index-only payment is wrong for most members at almost any threshold, because the mean share during an event is small; R2 (50% with no witness) is the source of most wrong payouts."
                if at_edge
                else ""
            ),
            "Sensitivity of the chosen pair (witness_share x false_confirmation_rate -> theta_lo, theta_hi): "
            + "; ".join(f"{r.witness_share} x {r.false_confirmation_rate} -> {r.theta_lo}, {r.theta_hi}" for r in sens.itertuples())
            + ".",
            "R3 exposure (theta_lo events without a theta_hi event, per county-year in training): "
            + f"{(per.at[c.theta_lo, 'events_per_county_year'] - per.at[c.theta_hi, 'events_per_county_year']):.4f} (difference of event rates; not priced in version 1).",
            "Per-threshold training quantities (events per county-year): "
            + ", ".join(f"{t}: {r.events_per_county_year:.3f}" for t, r in per.iterrows())
            + ".",
        ]
    )
    grid = [
        {
            "theta_lo": float(r.theta_lo),
            "theta_hi": float(r.theta_hi),
            "expected_wrong_payouts": r6(r.expected_wrong_payouts),
            "expected_missed_payouts": r6(r.expected_missed_payouts),
            "objective": r6(r.objective),
            "chosen": bool(r.chosen),
        }
        for r in g.sort_values(["theta_lo", "theta_hi"]).itertuples()
    ]
    rows_used = train_readings().groupby("year").size().to_dict()
    sources = panel.data_source_entries(TRAIN, rows_used)
    return {
        "theta_hi": float(c.theta_hi),
        "theta_lo": float(c.theta_lo),
        "gap_bridge_minutes": GAP_BRIDGE_MINUTES,
        "event_merge_hours": EVENT_MERGE_HOURS,
        "dropout_max_fraction": DROPOUT_MAX_FRACTION,
        "theta_hi_status": theta_hi_status(c.theta_hi),
        "sensitivity": sens.to_dict("records"),
        "grid": grid,
        "provenance": make_provenance("ml/fourhour_ml/trigger.py, ml/fourhour_ml/build.py:build_trigger_params", sources, notes),
    }


# ------------------------------------------------------------------ M3 / M4 models

from decimal import ROUND_HALF_UP, Decimal  # noqa: E402

from . import models  # noqa: E402
from .config import (  # noqa: E402
    COST_OF_CAPITAL_RATE,
    COVERS_CENTS,
    DEMO_COUNTIES,
    EXPENSE_LOAD_CENTS,
    PAYOUT_SCHEDULE,
    POOL_MEMBERS,
    QUALIFY_HOURS,
    R2_FACTOR,
    SEED,
    T24_HOURS,
)

N_SIMULATIONS = 20000
LOSS_QUANTILES = (0.5, 0.9, 0.99, 0.995)
STOP_LOSS_ATTACH_Q = 0.95
STOP_LOSS_EXHAUST_Q = 0.995


def test_county_years() -> pd.DataFrame:
    cov = coverage()
    m = cov["in_model_universe"] & cov["year"].between(*TEST_YEARS)
    return cov[m][["fips", "year", "state", "county", "coverage_share"]].reset_index(drop=True)


@functools.lru_cache(maxsize=1)
def model_run() -> dict:
    lo, hi = chosen_thresholds()
    first = {s["year"]: s["first_t"] for s in panel.year_stats()}
    out: dict = {"theta_lo": lo, "theta_hi": hi, "variants": {}}
    tcy = test_county_years()
    td = readings_for(tcy, TEST)
    test_ev = trigger.county_events(td[["fips", "t", "customers_out"]], lo)
    test_ev_hi = trigger.county_events(td[["fips", "t", "customers_out"]], hi)
    cm_test = models.county_months(tcy, test_ev)
    out.update(test_cy=tcy, test_events=test_ev, test_events_hi=test_ev_hi, cm_test=cm_test, test_rows=td.groupby("year").size().to_dict())
    for name, inc in (("exclude_low_coverage", False), ("include_low_coverage", True)):
        cy = fitted_county_years(inc)
        rd = readings_for(cy, TRAIN)
        ev = trigger.county_events(rd[["fips", "t", "customers_out"]], lo)
        ev_hi = trigger.county_events(rd[["fips", "t", "customers_out"]], hi)
        cm = models.county_months(cy, ev, first)
        fit = models.fit_frequency(cm)
        pred = models.predict_frequency(fit, cm_test)
        out["variants"][name] = {
            "county_years": cy,
            "events": ev,
            "events_hi": ev_hi,
            "cm": cm,
            "fit": fit,
            "pred": pred,
            "eval": models.evaluate_frequency(fit, pred),
            "rows": rd.groupby("year").size().to_dict(),
        }
    prim = out["variants"]["exclude_low_coverage"]
    out["severity"] = models.fit_severity(prim["events"])
    out["credibility"] = models.fit_credibility(prim["cm"])
    out["p_affected"] = float(prim["events"]["mean_share"].mean())
    return out


def _variant_doc(v: dict) -> dict:
    pred = v["pred"]
    pcy = pred.groupby(["fips", "year"]).agg(realised=("events", "sum"), nb_glm=("nb_glm", "sum"), poisson_glm=("poisson_glm", "sum"), historical_mean=("historical_mean", "sum")).reset_index()
    ev = v["eval"]
    return {
        "county_years_fitted": int(len(v["county_years"])),
        "per_county_year": [
            {"fips": r.fips, "year": int(r.year), "realised": int(r.realised), "nb_glm": r6(r.nb_glm), "poisson_glm": r6(r.poisson_glm), "historical_mean": r6(r.historical_mean)}
            for r in pcy.itertuples()
        ],
        "deviance": {k: r6(x) for k, x in ev["deviance"].items()},
        "log_score": {k: r6(x) for k, x in ev["log_score"].items()},
        "calibration": {m: [{**b, "predicted_mean": r6(b["predicted_mean"]), "realised_mean": r6(b["realised_mean"])} for b in bins] for m, bins in ev["calibration"].items()},
        "beats_baselines": ev["beats_baselines"],
    }


def not_offered_reason(fips: str) -> str | None:
    """SPEC 5.5 / CONTRACTS 1.8: a county is not offered when its priced expected loss is zero
    because no theta_lo qualifying event exists in its credibility group (its state's
    model-universe counties) in the fitted training years. Returns one sentence, or None."""
    mr = model_run()
    cred = mr["credibility"]["table"]
    row = cred.loc[fips]
    if float(12 * row["blended"]) * mr["p_affected"] > 0:
        return None
    group = cred[cred["state"] == row["state"]]
    names = coverage().drop_duplicates("fips").set_index("fips")["county"]
    members = ", ".join(sorted(str(names[f]) for f in group.index))
    if int(group["ev"].sum()) == 0:
        return (
            f"No outage reached {mr['theta_lo']:.0%} of customers for 4 hours in its pricing group "
            f"({row['state']} counties with usable data: {members}) in the 2014-2022 data, so there is no history to set a price from."
        )
    return "The priced expected loss is zero, so no price can be set from the 2014-2022 data."


def pricing_components() -> dict:
    mr = model_run()
    cred = mr["credibility"]["table"]
    comps = {}
    for fips in DEMO_COUNTIES:
        row = cred.loc[fips]
        reason = not_offered_reason(fips)
        comps[fips] = {
            "expected_events_per_year": float(12 * row["blended"]),
            "prob_premises_affected": mr["p_affected"],
            "credibility_weight": float(row["Z"]),
            "offered": reason is None,
            "not_offered_reason": reason,
        }
    return comps


def offered_counties() -> list[str]:
    return [f for f, c in pricing_components().items() if c["offered"]]


def expected_annual_payout_cents(c: dict, cover: int) -> Decimal:
    """SPEC 5.5, CONTRACTS 1.8: expected payout share = t4, the only tier paid."""
    share = Decimal(str(PAYOUT_SCHEDULE["t4"]))
    return Decimal(repr(c["expected_events_per_year"])) * share * Decimal(repr(c["prob_premises_affected"])) * cover


def premium_cents(c: dict, cover: int, margin: float, expense: int) -> int:
    """CONTRACTS section 4: exact decimal, round only the final premium half up."""
    p = expected_annual_payout_cents(c, cover) / 12 * (1 + Decimal(repr(margin))) + expense
    return int(p.quantize(Decimal(1), rounding=ROUND_HALF_UP))


def pool_members() -> pd.DataFrame:
    """Simulated pool: POOL_MEMBERS split evenly over the offered demo counties, covers in
    turn. A county that is not offered (CONTRACTS 1.8) has no quote, so it has no members."""
    fips = offered_counties()
    rows = [(fips[i % len(fips)], COVERS_CENTS[(i // len(fips)) % len(COVERS_CENTS)]) for i in range(POOL_MEMBERS)]
    return pd.DataFrame(rows, columns=["fips", "cover_cents"])


@functools.lru_cache(maxsize=1)
def solvency_run() -> dict:
    mr = model_run()
    prim = mr["variants"]["exclude_low_coverage"]
    ev = prim["events"].assign(year=prim["events"]["start"].dt.year)
    cy = prim["county_years"]
    members = pool_members()
    rng = np.random.default_rng(SEED)
    losses = np.zeros(N_SIMULATIONS, dtype=np.int64)
    for fips in DEMO_COUNTIES:
        years = cy[cy["fips"] == fips]["year"].to_numpy()
        covers = members[members["fips"] == fips]["cover_cents"].to_numpy()
        draws = rng.choice(years, size=N_SIMULATIONS)
        by_year = {y: ev[(ev["fips"] == fips) & (ev["year"] == y)] for y in years}
        for s, y in enumerate(draws):
            losses[s] += models.simulate_year_payouts(rng, by_year[y], covers)[0]
    mean = float(losses.mean())
    var995 = float(np.quantile(losses, 0.995))
    margin = None
    if COST_OF_CAPITAL_RATE is not None and mean > 0:
        margin = COST_OF_CAPITAL_RATE * (var995 - mean) / mean
    return {"losses": losses, "mean": mean, "var995": var995, "margin": margin, "members": members}


def premiums_table() -> dict:
    comps = pricing_components()
    margin = solvency_run()["margin"]
    if margin is None or EXPENSE_LOAD_CENTS is None:
        return {}
    return {f: {cov: premium_cents(c, cov, margin, EXPENSE_LOAD_CENTS) for cov in COVERS_CENTS} for f, c in comps.items() if c["offered"]}


def model_version() -> str:
    from .schema import git_commit

    return f"{pd.Timestamp.now(tz='UTC').strftime('%Y-%m-%d')}-{git_commit()}"


def _sources_train_test(rows_train: dict, rows_test: dict) -> list[dict]:
    rows = {**rows_train, **rows_test}
    return panel.data_source_entries(TRAIN + TEST, rows)


def build_pricing_table() -> dict:
    mr = model_run()
    comps = pricing_components()
    sr = solvency_run()
    prem = premiums_table()
    cred = mr["credibility"]
    sev = mr["severity"]
    prim = mr["variants"]["exclude_low_coverage"]
    ev = prim["events"]
    notes = " ".join(
        [
            f"Model inputs fitted on training years only (2014-2022, {len(prim['county_years'])} fitted county-years in the {prim['county_years']['fips'].nunique()}-county model universe). Events are theta_lo = {mr['theta_lo']} qualifying events (runs of 4 h or more after gap bridging, dropouts and the {EVENT_MERGE_HOURS} h hours clause), CONTRACTS 1.7.",
            "expected_events_per_year = 12 x the Bühlmann-Straub blended monthly rate (county rate blended with its state rate; "
            + (("k = " + format(cred["k"], ".6g")) if cred["k"] is not None else "the between-county variance estimate a is not positive, so k is undefined (null) and every county's credibility weight is 0: each county gets its state's rate")
            + "). Training theta_lo events per demo county: "
            + ", ".join(f"{DEMO_COUNTIES[f]}: {int((ev['fips'] == f).sum())}" for f in DEMO_COUNTIES)
            + ".",
            f"Expected payout share = t4 {PAYOUT_SCHEDULE['t4']}, the only tier paid (CONTRACTS 1.8: the 24-hour tier was cut on 2026-11-12 and is neither paid nor priced). P(24 h | event) stays in backtest.json severity as a report (fitted {sev['p24_fitted']:.4f}, {sev['n']} training events).",
            f"prob_premises_affected: SPEC 5.5 version 1, the mean share of customers out during training event windows, pooled over all training events ({len(ev)}); the same for every county.",
            f"risk_margin_rate = cost of capital {COST_OF_CAPITAL_RATE} x (1-in-200 simulated annual pool loss - mean loss) / mean loss = {COST_OF_CAPITAL_RATE} x ({sr['var995']:.0f} - {sr['mean']:.0f}) / {sr['mean']:.0f} (solvency.json). expense_load_cents {EXPENSE_LOAD_CENTS} per member per month.",
            f"Product parameters set by the humans on {PRODUCT_PARAMS_SET_ON}: expense_load_cents {EXPENSE_LOAD_CENTS}, cost of capital {COST_OF_CAPITAL_RATE}, payout_schedule t4 {PAYOUT_SCHEDULE['t4']} (60% of cover is a product assumption).",
            "Offered (SPEC 5.5, CONTRACTS 1.8): a county is not offered when its priced expected loss is zero because no theta_lo qualifying event exists in its credibility group (its state's model-universe counties) in the fitted training years. "
            + "; ".join(f"{DEMO_COUNTIES[f]}: " + ("offered" if c["offered"] else "not offered. " + c["not_offered_reason"]) for f, c in comps.items())
            + " Not-offered counties have no premium and stay in backtest.json and data_quality.json.",
            "Premiums this table gives under the CONTRACTS section 4 formula (cents per month, cover 5000/15000/30000): "
            + "; ".join(f"{DEMO_COUNTIES[f]}: {', '.join(str(prem[f][c]) for c in COVERS_CENTS)}" for f in prem)
            + ".",
            "Priced case: a member with a registered witness. R2 (no witness, 50% on theta_hi events) is not priced in version 1; its cost is reported in backtest.json.",
        ]
    )
    return {
        "model_version": model_version(),
        "counties": {f: {k: (x if isinstance(x, (bool, str)) or x is None else r6(x)) for k, x in c.items()} for f, c in comps.items()},
        "rates": {"risk_margin_rate": r6(sr["margin"]), "expense_load_cents": EXPENSE_LOAD_CENTS},
        "payout_schedule": dict(PAYOUT_SCHEDULE),
        "provenance": make_provenance("ml/fourhour_ml/models.py, ml/fourhour_ml/build.py:build_pricing_table", panel.data_source_entries(TRAIN, prim["rows"]), notes),
    }


def build_solvency() -> dict:
    sr = solvency_run()
    L = sr["losses"]
    prem = premiums_table()
    members = sr["members"]
    comps = pricing_components()
    margin = sr["margin"]
    # premium income for risk (net of the expense load) for one year
    risk_income = 0
    for r in members.itertuples():
        risk_income += 12 * (prem[r.fips][r.cover_cents] - EXPENSE_LOAD_CENTS)
    ruin = float((L > risk_income).mean())
    capital = int(max(0, np.ceil(np.quantile(L, 0.995) - risk_income)))
    att = int(np.ceil(np.quantile(L, STOP_LOSS_ATTACH_Q)))
    exh = int(np.ceil(np.quantile(L, STOP_LOSS_EXHAUST_Q)))
    lim = max(0, exh - att)
    layer = np.clip(L - att, 0, lim)
    price = int(np.ceil(layer.mean() * (1 + margin))) if margin is not None else None
    ev = model_run()["variants"]["exclude_low_coverage"]["events"]
    offered = offered_counties()
    not_off = [DEMO_COUNTIES[f] for f in DEMO_COUNTIES if f not in offered]
    # What the simulation really does: no capital at the start of each simulated year; ruin is
    # a year whose payouts exceed that year's premium income net of the expense load.
    capital_assumption = {
        "initial_capital_cents": 0,
        "description": (
            f"The pool starts each simulated year with no capital: only that year's premiums net of the expense load ({risk_income} cents) are available to pay claims. "
            "Ruin is a simulated year whose payouts exceed that premium income."
        ),
    }
    notes = " ".join(
        [
            f"Simulated pool: {POOL_MEMBERS} members split evenly over the offered demo counties ({', '.join(DEMO_COUNTIES[f] for f in offered)}), covers $50/$150/$300 in turn, every member with a witness (the priced case). Members and their losses are simulated; the outage events are real."
            + (f" Not offered, so no members: {', '.join(not_off)} (pricing_table.json)." if not_off else ""),
            f"Payouts: the 4-hour tier only, {PAYOUT_SCHEDULE['t4']:.0%} of cover per affected member per event, rounded down to a cent (CONTRACTS 1.8: no 24-hour top-up).",
            f"{N_SIMULATIONS} simulated years, seed {SEED}. For each county, a whole fitted training year (2014-2022, coverage floor) is drawn at random and its real theta_lo events replayed, so one event hits every member in the county at once. Each member is affected by an event with probability equal to the event's mean share (SPEC 5.5 version 1).",
            "Training theta_lo qualifying events in the fitted years per demo county: "
            + ", ".join(f"{DEMO_COUNTIES[f]} {int((ev['fips'] == f).sum())}" for f in DEMO_COUNTIES)
            + ". A county with none has zero simulated loss, while an offered county's premium can still carry a non-zero rate from credibility with its state rate (pricing_table.json), so simulation and pricing disagree for it; this is a limit of resampling a county's own history with so few events.",
            f"Annual risk income = 12 x (premium - expense load) summed over members = {risk_income} cents. ruin_probability = share of simulated years whose payouts exceed that income with no other capital. capital_1_in_200_cents = 99.5th percentile loss - risk income (0 if negative).",
            f"Aggregate stop-loss layer: attaches at the {STOP_LOSS_ATTACH_Q:.0%} annual loss and exhausts at the {STOP_LOSS_EXHAUST_Q:.1%} loss; price = expected layer loss x (1 + risk_margin_rate {margin:.4f}).",
            f"Mean simulated annual loss {L.mean():.0f} cents; share of simulated years with no payout {(L == 0).mean():.4f}.",
        ]
    )
    mr = model_run()
    return {
        "members": POOL_MEMBERS,
        "simulations": N_SIMULATIONS,
        "capital_assumption": capital_assumption,
        "ruin_probability": r6(ruin),
        "capital_1_in_200_cents": capital,
        "stop_loss": {"attachment_cents": att, "limit_cents": lim, "price_cents": price},
        "annual_loss_quantiles": [{"q": q, "loss_cents": int(np.ceil(np.quantile(L, q)))} for q in LOSS_QUANTILES],
        "provenance": make_provenance("ml/fourhour_ml/models.py, ml/fourhour_ml/build.py:build_solvency", panel.data_source_entries(TRAIN, mr["variants"]["exclude_low_coverage"]["rows"]), notes),
    }


def _r2_multiplier() -> dict:
    """Training-year ratio of a no-witness member's expected payout (R2: 50% on every theta_hi
    event) to a witness member's (theta_lo events x prob affected), pooled over the universe."""
    mr = model_run()
    prim = mr["variants"]["exclude_low_coverage"]
    n_cy = len(prim["county_years"])
    sev_share = PAYOUT_SCHEDULE["t4"]  # CONTRACTS 1.8: 4-hour tier only
    hi = prim["events_hi"]
    hi_share = float(models.payout_share(len(hi)).mean()) if len(hi) else 0.0
    r_lo = len(prim["events"]) / n_cy
    r_hi = len(hi) / n_cy
    wit = r_lo * sev_share * mr["p_affected"]
    nowit = r_hi * R2_FACTOR * hi_share
    return {"rate_lo": r_lo, "rate_hi": r_hi, "witness": wit, "no_witness": nowit, "ratio": nowit / wit if wit > 0 else None}


def build_backtest() -> dict:
    mr = model_run()
    sev = mr["severity"]
    cred = mr["credibility"]
    prem = premiums_table()
    members = pool_members()
    test_ev = mr["test_events"]
    test_hi = mr["test_events_hi"]
    rng = np.random.default_rng(SEED + 1)
    years = []
    r2_lines = []
    for y in TEST:
        prem_c = int(sum(12 * prem[r.fips][r.cover_cents] for r in members.itertuples()))
        pay, claims, ebc = 0, 0, {}
        r2 = 0
        for f in DEMO_COUNTIES:
            e = test_ev[(test_ev["fips"] == f) & (test_ev["start"].dt.year == y)]
            ebc[f] = int(len(e))
            covers = members[members["fips"] == f]["cover_cents"].to_numpy()
            p, c = models.simulate_year_payouts(rng, e, covers)
            pay += p
            claims += c
            eh = test_hi[(test_hi["fips"] == f) & (test_hi["start"].dt.year == y)]
            share100 = int(round(PAYOUT_SCHEDULE["t4"] * 100))  # 4-hour tier only (CONTRACTS 1.8)
            for _ in range(len(eh)):
                r2 += int(sum(((int(cv) * share100) // 100) * R2_FACTOR for cv in covers))
        r2_lines.append(f"{y}: {r2} cents on {int(((test_hi['start'].dt.year == y) & test_hi['fips'].isin(list(DEMO_COUNTIES))).sum())} theta_hi events")
        years.append({"year": y, "premiums_cents": prem_c, "payouts_cents": int(pay), "loss_ratio": r6(pay / prem_c) if prem_c else None, "events": int(sum(ebc.values())), "events_by_county": ebc, "claims_paid": int(claims)})
    r2m = _r2_multiplier()
    var = mr["variants"]
    pv = var["exclude_low_coverage"]["eval"]
    iv = var["include_low_coverage"]["eval"]
    test_p24 = float((mr["test_events"]["longest_run_hours"] >= T24_HOURS).mean()) if len(mr["test_events"]) else None
    sev_doc = {
        "threshold_hours": QUALIFY_HOURS,
        "n_events_train": sev["n"],
        "n_events_test": int(len(mr["test_events"])),
        "gpd": {"shape": r6(sev["shape"]), "scale": r6(sev["scale"]), "location_hours": QUALIFY_HOURS},
        "ks_statistic": r6(sev["ks_statistic"]),
        "ks_pvalue": r6(sev["ks_pvalue"]),
        "prob_24h_given_event": {"fitted": r6(sev["p24_fitted"]), "empirical_train": r6(sev["p24_empirical"]), "empirical_test": r6(test_p24)},
        "qq": [{k: r6(x) for k, x in q.items()} for q in sev["qq"]],
    }
    ct = cred["table"]
    cred_doc = {
        "method": "buhlmann_straub",
        "k": r6(cred["k"]),
        "counties": {
            f: {"state": r.state, "county_rate": r6(r.xbar), "state_rate": r6(r.rate), "exposure_months": int(r.m), "weight": r6(r.Z), "blended_rate": r6(r.blended)}
            for f, r in ct.iterrows()
        },
    }

    def fmt(ev):
        return ", ".join(f"{m}: deviance {ev['deviance'][m] if ev['deviance'][m] is None else round(ev['deviance'][m], 3)}, log score {ev['log_score'][m] if ev['log_score'][m] is None else round(ev['log_score'][m], 4)}" for m in models.FREQ_MODELS)

    fitp = var["exclude_low_coverage"]["fit"]
    hist_inf = [m for m in models.FREQ_MODELS if pv["deviance"][m] is None or pv["log_score"][m] is None]
    notes = " ".join(
        [
            f"Fitted on 2014-2022, evaluated on 2023-2024 ({len(mr['test_cy'])} test county-years in the model universe, all included). theta_lo {mr['theta_lo']} qualifying events: {len(var['exclude_low_coverage']['events'])} in training (exclude_low_coverage), {len(var['include_low_coverage']['events'])} (include_low_coverage), {len(mr['test_events'])} in the test years.",
            "Frequency models: Poisson and negative-binomial (NB2) GLMs with a month-of-year effect (one annual harmonic, sin and cos of month) and state effects, and the county's historical mean rate per county-month.",
            f"Primary variant (exclude_low_coverage): {fmt(pv)}. beats_baselines = {pv['beats_baselines']}. With thin early years (include_low_coverage): {fmt(iv)}. beats_baselines = {iv['beats_baselines']}.",
            f"The NB dispersion alpha is estimated at {fitp['alpha']:.6f}, so the NB model collapses to the Poisson model: the data shows no overdispersion at this event count and the NB adds nothing over the Poisson baseline.",
            f"Illinois is Cook County only and Cook has no theta_lo event in training, so the Illinois coefficient diverges ({fitp['nb'].params['state_IL']:.2f} on the log scale) and the Hessian could not be inverted; predictions for Cook are effectively zero.",
            (f"{', '.join(hist_inf)}: deviance or log score is null because the model predicts 0 events for a county (no training events) that had test events (Harris: the 2024 derecho and Beryl), which makes the score infinite." if hist_inf else ""),
            "beats_baselines is true only if the NB model has lower deviance and lower log score than each baseline whose score is finite.",
            f"Severity: GPD on the excess of the longest run over 4 h, location fixed at 0. The KS p-value is computed with parameters estimated from the same data, so it is optimistic. P(24 h | event): fitted {sev['p24_fitted']:.4f}, training {sev['p24_empirical']:.4f}, test {test_p24:.4f}.",
            f"Credibility: Bühlmann-Straub across the {len(ct)} universe counties with each state's exposure-weighted rate as the collective; s2 = {cred['s2']:.6g}, a = {cred['a']:.6g}. " + ("k = " + format(cred["k"], ".6g") if cred["k"] is not None else "Because a is not positive, k is null and every credibility weight is 0: the county histories carry no credible signal beyond their state's rate at this event count") + ". Rates are events per county-month.",
            f"Pool: simulated members (pool of {POOL_MEMBERS} as in solvency.json, all with a witness, split over the offered demo counties: {', '.join(DEMO_COUNTIES[f] for f in offered_counties())}), premiums frozen from pricing_table.json, real 2023-2024 theta_lo events; each affected member (probability equal to the event's mean share, seed {SEED + 1}) is paid the 4-hour tier only, {PAYOUT_SCHEDULE['t4']:.0%} of cover, no 24-hour top-up (CONTRACTS 1.8). events and events_by_county count events in all three demo counties, including any not offered (no members there, so no payouts). Members and their losses are simulated.",
            "R2 cost, not priced (CONTRACTS 1.7): if every member had no witness, R2 would pay 50% of the schedule on theta_hi events: " + "; ".join(r2_lines) + ".",
            (f"Estimated R2 multiplier (training years, pooled universe): a no-witness member's expected payout is {r2m['ratio']:.3f} times a witness member's (theta_hi rate {r2m['rate_hi']:.4f} vs theta_lo rate {r2m['rate_lo']:.4f} events per county-year; prob affected {mr['p_affected']:.4f})." if r2m["ratio"] is not None else "R2 multiplier not computed: no witness-member payout in training."),
            "Hurricane Beryl (Harris, July 2024) is in the test years; the feed is missing its onset (data_quality.json storm_windows), so its event starts later than the storm did. The models never saw 2023-2024.",
        ]
    )
    return {
        "frequency": {
            "threshold": "theta_lo",
            "theta": mr["theta_lo"],
            "unit": "qualifying events per county-month",
            "primary_variant": "exclude_low_coverage",
            "variants": {k: _variant_doc(v) for k, v in var.items()},
        },
        "severity": sev_doc,
        "credibility": cred_doc,
        "pool": {"members": POOL_MEMBERS, "years": years},
        "provenance": make_provenance(
            "ml/fourhour_ml/models.py, ml/fourhour_ml/build.py:build_backtest",
            _sources_train_test(var["include_low_coverage"]["rows"], mr["test_rows"]),
            notes,
        ),
    }


# ------------------------------------------------------------------ M5 scenarios

SCENARIO_SOURCE_DATASET = "EAGLE-I recorded outages 2014-2025 (ORNL)"
SPOOF_QUIET_DAYS = 7
DEAD_FEED_FRACTION = 0.1


def _day_spans(days: pd.DatetimeIndex) -> str:
    if len(days) == 0:
        return "none"
    s = pd.Series(days)
    grp = (s.diff() != pd.Timedelta(days=1)).cumsum()
    return ", ".join(f"{g.iloc[0].date()} to {g.iloc[-1].date()}" for _, g in s.groupby(grp))


def _series_points(full: pd.Series, n_cust: int) -> list[dict]:
    pts = []
    for t, v in full.items():
        if pd.isna(v):
            pts.append({"t": iso(t), "customers_out": None, "share": None})
        else:
            pts.append({"t": iso(t), "customers_out": int(v), "share": r6(min(v / n_cust, 1.0))})
    return pts


def _window(fips: str, start: pd.Timestamp, end: pd.Timestamp) -> pd.Series:
    years = tuple(range(start.year, (end - pd.Timedelta(minutes=15)).year + 1))
    df = panel.readings(years)
    s = df[(df["fips"] == fips) & (df["t"] >= start) & (df["t"] < end)].set_index("t")["customers_out"]
    return s.reindex(pd.date_range(start, end, freq="15min", inclusive="left"))


def _scenario(sid: str, title: str, description: str, fips: str, start: pd.Timestamp, end: pd.Timestamp, notes: str) -> dict:
    n = int(panel.county_customers()[fips])
    full = _window(fips, start, end)
    m = panel.manifest(start.year)
    missing = int(full.isna().sum())
    prov_notes = (
        f"Real data: EAGLE-I county customers out for FIPS {fips}, every 15-minute interval from {iso(start)} to {iso(end)} (end exclusive). "
        f"share = customers_out / MCC county customers ({n:,}), capped at 1.0. {missing} of {len(full)} intervals have no reading and are points with null values; they are never filled. "
        f"Off-grid timestamps were moved to the start of their 15-minute slot. Timestamps are UTC (data_quality.json timestamp_check). " + notes
    )
    src = {"dataset": SCENARIO_SOURCE_DATASET, "url": panel.EAGLEI_DOI, "file": m["source_name"], "sha256": m["source_sha256"]}
    sources = panel.data_source_entries((start.year,), {start.year: int(full.notna().sum())})
    return {
        "id": sid,
        "title": title,
        "description": description,
        "fips": fips,
        "county_customers": n,
        "start": iso(start),
        "end": iso(end - pd.Timedelta(minutes=15)),
        "series": _series_points(full, n),
        "source": src,
        "provenance": make_provenance("ml/fourhour_ml/build.py:build_scenarios", sources, prov_notes),
    }


def _describe_runs(full: pd.Series, n: int, lo: float, hi: float) -> str:
    from .events import reconstruct_events, reconstruct_runs

    s = full.dropna().rename("customers_out").rename_axis("run_start_time").reset_index()
    out = []
    for name, th in (("theta_lo", lo), ("theta_hi", hi)):
        r = reconstruct_runs(s, n, th)
        q = r[r["duration_hours"] >= QUALIFY_HOURS]
        ev = reconstruct_events(s, n, th)
        ev = ev[ev["qualifies_t4"]]
        if q.empty:
            out.append(f"at {name} {th}: no run of 4 h or more")
            continue
        parts = []
        for x in q.itertuples():
            t24 = x.start + pd.Timedelta(hours=T24_HOURS) if x.duration_hours >= T24_HOURS else None
            parts.append(
                f"run {iso(x.start)} to {iso(x.end)} ({x.duration_hours:.2f} h, reaches 4 h at {iso(x.start + pd.Timedelta(hours=QUALIFY_HOURS))}"
                + (f", 24 h at {iso(t24)}" if t24 is not None else ", never 24 h")
                + f", {int(x.n_missing_bridged)} bridged intervals)"
            )
        out.append(f"at {name} {th}: " + "; ".join(parts) + f"; {len(ev)} qualifying event(s) after the hours clause")
    return ". ".join(out) + "."


def build_scenarios() -> list[dict]:
    lo, hi = chosen_thresholds()
    harris = "48201"
    n = int(panel.county_customers()[harris])
    docs = []
    # derecho-harris-2024 (D5): window from data_quality.json storm_windows
    w = next(x for x in quality.STORM_WINDOWS if x["id"] == "derecho-harris-2024")
    full = _window(harris, w["start"], w["end"])
    runs = _describe_runs(full, n, lo, hi)
    rule = "R3 (index between theta_lo and theta_hi; a witness must confirm)" if "theta_hi " + str(hi) + ": no run" in runs else "R1 or R2"
    docs.append(
        _scenario(
            "derecho-harris-2024",
            "Houston derecho, Harris County, May 2024",
            f"Real EAGLE-I replay of the 16 May 2024 derecho in Harris County, TX. Under the current thresholds (theta_lo {lo}, theta_hi {hi}) this window qualifies under {rule}. Missing intervals are shown as gaps.",
            harris,
            w["start"],
            w["end"],
            "Window chosen before thresholds as the D5 storm window. Under trigger_params.json thresholds: " + runs + " Data quality in this window: see data_quality.json storm_windows (dropout readings bridged by the dropout rule; a frozen value of 131,113 repeats on 20-22 May; no rule is applied to it).",
        )
    )
    # spoof-quiet: the quietest fully observed 7-day window in Harris 2024 (rule, not hand-picked)
    d = _window(harris, pd.Timestamp("2024-01-01"), pd.Timestamp("2025-01-01"))
    daily = d.resample("D").median()
    alive_floor = DEAD_FEED_FRACTION * float(daily.median())
    dead_days = daily[daily < alive_floor].index
    k = SPOOF_QUIET_DAYS * 96
    best = None
    for day in pd.date_range("2024-01-01", pd.Timestamp("2025-01-01") - pd.Timedelta(days=SPOOF_QUIET_DAYS), freq="D"):
        seg = d[day : day + pd.Timedelta(days=SPOOF_QUIET_DAYS) - pd.Timedelta(minutes=15)]
        if len(seg) != k or seg.isna().any():
            continue
        if any(day <= x < day + pd.Timedelta(days=SPOOF_QUIET_DAYS) for x in dead_days):
            continue
        mx = float(seg.max())
        if best is None or mx < best[1]:
            best = (day, mx)
    if best is not None:
        s0 = best[0]
        s1 = s0 + pd.Timedelta(days=SPOOF_QUIET_DAYS)
        docs.append(
            _scenario(
                "spoof-quiet",
                "Quiet week, Harris County, 2024",
                f"Real EAGLE-I replay of the quietest fully observed week of 2024 in Harris County, TX: at most {int(best[1]):,} customers out ({best[1] / n:.4%} of customers). The index never reaches theta_lo, so a witness reporting an outage here gets no payout (R5).",
                harris,
                s0,
                s1,
                f"Selection rule: among 7-day windows starting at 00:00Z in Harris 2024 with every interval present and no dead-feed day, the one with the lowest maximum customers out (earliest on ties). A dead-feed day is a day whose median reading is below {DEAD_FEED_FRACTION} of the year's median daily median ({alive_floor:.1f} customers): {len(dead_days)} days in 2024 ({_day_spans(dead_days)}), when the feed reports near zero for a county of {n:,} customers. Peak share {best[1] / n:.6f}, below theta_lo {lo}.",
            )
        )
    return docs


# ------------------------------------------------------------------ M6 basis risk

def build_basis_risk() -> dict:
    from . import basis, esmi
    from .eaglei import file_sha256

    r = basis.run_study()
    d = r["districts"]
    sch = {k: {kk: (r6(vv) if kk.endswith("_rate") else int(vv)) for kk, vv in v.items()} for k, v in r["schemes"].items()}
    nulls = [f"{k}.{kk}" for k, v in sch.items() for kk, vv in v.items() if vv is None]
    lo_edge = r["theta_lo"] == min(basis.THETA_GRID)
    hi_edge = r["theta_hi"] == max(basis.THETA_GRID)
    best = r["grid"].sort_values("objective").iloc[0]
    fs = r["file_stats"]
    notes = " ".join(
        [
            "Real data: Prayas ESMI minute-wise voltage at individual premises in India (Harvard Dataverse doi:10.7910/DVN/CLLZZM), files 2014-2018. The 2019 files are excluded: "
            + "; ".join(f"{s['file']}: {s['unparseable_cells']} #VALUE! dates, {s['duplicate_keys']} repeated (location, date, hour) keys" for s in fs if not s["used"])
            + "; the July-Dec 2019 Hour column holds only 0 and 1. Dates were parsed with an explicit format per file, confirmed from the data (2014-2018 DD-MM-YYYY, 2019 M/D/YYYY).",
            "No-supply rule, from 'About the initiative.pdf': a 0 V reading is a power outage; missing data is unavailable, not an interruption. So a monitor that is offline is told apart from no supply.",
            f"Eligibility (S4): a district is used if, in each half of the period, it has at least {esmi.MIN_USABLE_HOURS} hours with {r['cutoff']} or more monitors reporting concurrently. {len(d)} districts, {r['n_monitors']} monitors. Cut-off reason: {esmi.CUTOFF_REASON}.",
            "Share of minutes in the study period with the district index defined (8 or more monitors reporting): "
            + ", ".join(f"{k} {v:.2f}" for k, v in r["defined_share"].items())
            + ".",
            f"A district index built from this few monitors (median {d['median_active_when_reporting'].min():.0f} to {d['median_active_when_reporting'].max():.0f} reporting) is far noisier than a utility-wide county count in EAGLE-I, where the share is over tens of thousands to millions of customers: one premises moves a district index by 6% to 12.5%. The result shows the mechanism, not the size of basis risk for a US county index.",
            "Upper bound: in this study the witness is the premises' own monitor, so it is perfect by construction (rules_perfect_witness has no wrong payments). rules_10pct_missing and rules_30pct_missing drop that share of witness logs at random (per premises and event, seed "
            + str(SEED)
            + "); a missing log on an event reaching theta_hi is paid at 50% under R2 and counted as one payment. A real witness can also be wrong or spoofed; this study does not measure that.",
            f"Thresholds for this study are its own (the share of monitors without supply), chosen on the first half with the same objective and product parameters as trigger_params.json (witness_share {WITNESS_SHARE}, false_confirmation_rate {FALSE_CONFIRMATION_RATE}) using the premises truth, and scored on the second half. Chosen theta_lo {r['theta_lo']}, theta_hi {r['theta_hi']} (objective {best.objective:.2f} in counts)."
            + (" theta_lo is the bottom edge of the grid (0.1 to 1.0), not an interior optimum." if lo_edge else "")
            + (" theta_hi is the top edge of the grid." if hi_edge else "")
            + " They are not the EAGLE-I thresholds and are not comparable to them.",
            "Definitions: a premises-event is an outage of 4 h or more at a premises (0 V run, missing gaps up to 120 min bridged, duration last minus first reading) starting when its district index is defined. A payment is a (premises, index event) pair with the premises' monitor reporting during the event; it is wrong if the premises had no 4 h outage overlapping the event. missed_rate = missed / premises_events; wrong_rate = wrong / paid. Index events are runs of 4 h or more (same run rules, 1-minute steps, no dropout rule, no hours-clause merge).",
            f"Fit period, index_only, for reference: {r['fit_index_only']}.",
            ("Null rates: " + ", ".join(nulls) + " (no premises-events or no payments)." if nulls else ""),
            "The dataset's documentation ('About the initiative.pdf', Terms of use) allows non-commercial, academic and research use with the source acknowledged; Dataverse lists the licence as CC0 1.0. Task S6 is for the humans.",
            "train_years and test_years in this block are the EAGLE-I years required by the contract; this study's own periods are fit_period and score_period.",
        ]
    )
    sources = [{"name": "ESMI location information.csv", "url": esmi.DATASET_URL, "sha256": file_sha256(esmi.ESMI_DIR / esmi.LOCATION_FILE), "rows_used": 528}]
    for s in fs:
        if s["used"]:
            sources.append({"name": s["file"], "url": esmi.DATASET_URL, "sha256": file_sha256(esmi.ESMI_DIR / s["file"]), "rows_used": int(s["rows_kept"])})
    return {
        "schemes": sch,
        "districts_used": [{"state": x.State, "district": x.District, "median_active_monitors": float(x.median_active_when_reporting)} for x in d.itertuples()],
        "min_active_monitors": int(r["cutoff"]),
        "theta_hi": r["theta_hi"],
        "theta_lo": r["theta_lo"],
        "fit_period": {"start": str(r["fit_period"][0].date()), "end": str(r["fit_period"][1].date())},
        "score_period": {"start": str(r["score_period"][0].date()), "end": str(r["score_period"][1].date())},
        "provenance": make_provenance("ml/fourhour_ml/basis.py, ml/fourhour_ml/esmi.py, ml/fourhour_ml/build.py:build_basis_risk", sources, notes),
    }
