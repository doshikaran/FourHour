"""Task M1: data quality evidence for data_quality.json (CONTRACTS 1.5 section 8.1).

* D1: zero-outage rows per year, and gap lengths inside large events in training years only.
* D3: county-years where customers out exceeds the MCC county count.
* D4: the Houston derecho (16 May 2024) onset time against the UTC assumption.
* D5: completeness of the derecho and Beryl windows in Harris County.
"""

from __future__ import annotations

import numpy as np
import pandas as pd

from . import panel
from .config import DROPOUT_MAX_FRACTION, GAP_BRIDGE_MINUTES, TRAIN_YEARS
from .events import dropout_mask, find_runs

# "Large event" for the D1 evidence. Chosen before thresholds exist, so it is threshold-free:
# a stretch where the share stays at or above 1%, traced with a 24-hour bridge so every
# missing gap inside it is visible, that peaks at 5% or more and lasts 4 hours or more.
LARGE_TRACE_SHARE = 0.01
LARGE_PEAK_SHARE = 0.05
LARGE_TRACE_BRIDGE_MINUTES = 24 * 60

# A "dropout-like reading" (reported, not used by D1): a reading that falls by 90% or more
# from a previous reading at or above 1% share, after which the series is back to at least
# half the previous level within 2 hours.
DROPOUT_FALL = 0.9

STORM_WINDOWS = [
    {
        "id": "derecho-harris-2024",
        "fips": "48201",
        "start": pd.Timestamp("2024-05-16T00:00"),
        "end": pd.Timestamp("2024-05-26T00:00"),
        "name": "Houston derecho",
    },
    {
        "id": "beryl-harris-2024",
        "fips": "48201",
        "start": pd.Timestamp("2024-07-07T00:00"),
        "end": pd.Timestamp("2024-07-21T00:00"),
        "name": "Hurricane Beryl",
    },
]


def iso(t) -> str:
    return pd.Timestamp(t).strftime("%Y-%m-%dT%H:%M:%SZ")


def gap_bridge_evidence(df_train: pd.DataFrame, fips_years: pd.DataFrame, dropout_max_fraction: float | None = DROPOUT_MAX_FRACTION) -> dict:
    """Missing-gap lengths inside large events, training years only.

    With ``dropout_max_fraction`` set, dropout readings (at the 1% trace share) are removed
    first, so they appear as missing gaps, as event reconstruction treats them. ``_splits``
    lists, per large event, how many runs the production rule (120-minute bridge) makes of it.
    """
    yrs = set(df_train["t"].dt.year.unique())
    if not yrs <= set(range(TRAIN_YEARS[0], TRAIN_YEARS[1] + 1)):
        raise RuntimeError("gap evidence may only use training years")
    cust = panel.county_customers()
    gaps: list[int] = []
    events = 0
    dropouts: list[int] = []
    splits: list[int] = []
    keep_cy = set(map(tuple, fips_years[["fips", "year"]].to_numpy()))
    df_train = df_train.assign(year=df_train["t"].dt.year)
    for (fips, year), g in df_train.groupby(["fips", "year"], sort=True):
        if (fips, year) not in keep_cy:
            continue
        g = g.sort_values("t")
        t = g["t"].to_numpy("datetime64[ns]").astype(np.int64)
        x = g["customers_out"].to_numpy(float)
        share = np.minimum(x / cust[fips], 1.0)
        # dropout-like readings anywhere in the county-year, counted on the raw readings
        dropouts.extend(_dropout_episodes(t, share))
        keep = dropout_mask(t, share, LARGE_TRACE_SHARE, GAP_BRIDGE_MINUTES, dropout_max_fraction)
        t, share = t[keep], share[keep]
        r = find_runs(t, share, LARGE_TRACE_SHARE, LARGE_TRACE_BRIDGE_MINUTES)
        for i0, i1 in zip(r["i0"], r["i1"]):
            if share[i0 : i1 + 1].max() < LARGE_PEAK_SHARE or t[i1] - t[i0] < 4 * 3600 * 10**9:
                continue
            events += 1
            d = np.diff(t[i0 : i1 + 1]) // (60 * 10**9) - 15
            gaps.extend(int(m) for m in d[d > 0])
            sub = find_runs(t[i0 : i1 + 1], share[i0 : i1 + 1], LARGE_TRACE_SHARE, GAP_BRIDGE_MINUTES)
            splits.append(int(sub["i0"].size))
    counts = pd.Series(gaps, dtype=int).value_counts().sort_index()
    total = int(counts.sum())
    within = int(counts[counts.index <= GAP_BRIDGE_MINUTES].sum())
    return {
        "events_examined": int(events),
        "gaps": [{"minutes": int(m), "count": int(c)} for m, c in counts.items()],
        "share_of_gaps_within_bridge": (within / total) if total else None,
        "_dropouts": dropouts,
        "_splits": splits,
        "_gap_minutes": gaps,
    }


def _dropout_episodes(t: np.ndarray, share: np.ndarray) -> list[int]:
    """Lengths in minutes of dropout-like episodes (module docstring), on raw readings."""
    out = []
    prev, nxt = share[:-1], share[1:]
    cand = np.flatnonzero((prev >= LARGE_TRACE_SHARE) & (nxt <= prev * (1 - DROPOUT_FALL))) + 1
    for i in cand:
        j = i
        while j < len(t) and t[j] - t[i - 1] <= 2 * 3600 * 10**9 + 15 * 60 * 10**9:
            if share[j] >= 0.5 * share[i - 1]:
                out.append(int((t[j] - t[i - 1]) // (60 * 10**9) - 15))
                break
            j += 1
    return out


def storm_window(df2024: pd.DataFrame, w: dict) -> dict:
    s = df2024[(df2024["fips"] == w["fips"]) & (df2024["t"] >= w["start"]) & (df2024["t"] < w["end"])]
    s = s.set_index("t")["customers_out"]
    grid = pd.date_range(w["start"], w["end"], freq="15min", inclusive="left")
    full = s.reindex(grid)
    miss = full.isna().to_numpy()
    longest = run = 0
    for m in miss:
        run = run + 1 if m else 0
        longest = max(longest, run)
    return {
        "id": w["id"],
        "fips": w["fips"],
        "start": iso(w["start"]),
        "end": iso(w["end"]),
        "expected_intervals": int(len(grid)),
        "intervals_present": int((~miss).sum()),
        "zero_rows": int((full == 0).sum()),
        "longest_missing_minutes": int(longest * 15),
        "_series": full,
    }


def derecho_onset(full: pd.Series, county_customers: int) -> dict:
    """First reading of the sharp rise: the first reading at or above 1% share that is at
    least ten times the reading before it."""
    share = full / county_customers
    rise = (share >= 0.01) & (full >= 10 * full.shift(1))
    t = rise[rise].index[0]
    i = full.index.get_loc(t)
    before = full.iloc[i - 4 : i + 4]
    local = t - pd.Timedelta(hours=5)  # CDT = UTC-5 in May
    return {
        "first_sharp_rise_utc": iso(t),
        "local_cdt": local.strftime("%Y-%m-%d %H:%M CDT"),
        "readings": {iso(k): (None if pd.isna(v) else int(v)) for k, v in before.items()},
    }
