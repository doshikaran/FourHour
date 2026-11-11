"""Outage event reconstruction from a county's 15-minute EAGLE-I series (SPEC.md 5.1).

Definitions used here (also in ml/README.md):

* Each row at time ``t`` stands for the interval ``[t, t + 15 min)``.
* ``share = customers_out / county_customers``.
* An interval is *above* when ``share >= threshold``. A missing row or a null
  ``customers_out`` is never above: it is unknown, not zero.
* Two above intervals belong to the same event when the gap between the end of the
  first and the start of the next is at most ``gap_bridge_minutes`` (30 by default).
  The gap may be below-threshold rows, missing rows, or both; it is counted inside
  the event's duration, and reported in ``n_bridged_below`` / ``n_bridged_missing``.
* ``start`` is the first above interval's ``t``; ``end`` is the last above interval's
  ``t + 15 min``; ``duration_hours = (end - start)``.
"""

from __future__ import annotations

import numpy as np
import pandas as pd

from .config import GAP_BRIDGE_MINUTES, INTERVAL_MINUTES

EVENT_COLUMNS = [
    "start",
    "end",
    "duration_hours",
    "peak_customers_out",
    "peak_share",
    "mean_share",
    "n_above",
    "n_bridged_below",
    "n_bridged_missing",
    "touches_series_start",
    "touches_series_end",
]


def _clean_series(series: pd.DataFrame, time_col: str, out_col: str) -> pd.DataFrame:
    s = series[[time_col, out_col]].rename(columns={time_col: "t", out_col: "customers_out"})
    s = s.assign(t=pd.to_datetime(s["t"])).sort_values("t")
    if s["t"].duplicated().any():
        raise ValueError("series has duplicate timestamps; de-duplicate before reconstruction")
    step = pd.Timedelta(minutes=INTERVAL_MINUTES)
    if ((s["t"] - s["t"].dt.floor(step)) != pd.Timedelta(0)).any():
        raise ValueError(f"series has timestamps off the {INTERVAL_MINUTES}-minute grid")
    return s.reset_index(drop=True)


def reconstruct_events(
    series: pd.DataFrame,
    county_customers: float,
    threshold: float,
    *,
    min_hours: float = 0.0,
    gap_bridge_minutes: int = GAP_BRIDGE_MINUTES,
    time_col: str = "run_start_time",
    out_col: str = "customers_out",
    series_start: pd.Timestamp | None = None,
    series_end: pd.Timestamp | None = None,
) -> pd.DataFrame:
    """Return one row per event with ``duration_hours >= min_hours``.

    ``series_start`` / ``series_end`` bound the period the series covers (for example a
    calendar year); an event within one interval of either bound is flagged, because it
    may continue outside the data given.
    """
    if not county_customers or county_customers <= 0:
        raise ValueError("county_customers must be positive")
    if not 0 < threshold <= 1:
        raise ValueError("threshold must be a share in (0, 1]")
    s = _clean_series(series, time_col, out_col)
    step = pd.Timedelta(minutes=INTERVAL_MINUTES)
    bridge = pd.Timedelta(minutes=gap_bridge_minutes)
    s["share"] = s["customers_out"] / float(county_customers)
    above = s[s["share"].fillna(-1.0) >= threshold]
    if above.empty:
        return pd.DataFrame(columns=EVENT_COLUMNS)

    t = above["t"]
    new_event = (t - t.shift()) > (step + bridge)
    event_id = new_event.cumsum()

    lo = pd.Timestamp(series_start) if series_start is not None else s["t"].min()
    hi = pd.Timestamp(series_end) if series_end is not None else s["t"].max() + step

    present_times = s.set_index("t")
    rows = []
    for _, grp in above.groupby(event_id, sort=True):
        start = grp["t"].iloc[0]
        end = grp["t"].iloc[-1] + step
        n_slots = int((end - start) / step)
        inside = present_times.loc[start : end - step]
        n_present = int(inside["customers_out"].notna().sum())
        n_above = len(grp)
        rows.append(
            {
                "start": start,
                "end": end,
                "duration_hours": (end - start) / pd.Timedelta(hours=1),
                "peak_customers_out": float(grp["customers_out"].max()),
                "peak_share": float(grp["share"].max()),
                "mean_share": float(inside["share"].mean()),
                "n_above": n_above,
                "n_bridged_below": n_present - n_above,
                "n_bridged_missing": n_slots - n_present,
                "touches_series_start": bool(start - lo < step + bridge),
                "touches_series_end": bool(hi - end < step + bridge),
            }
        )
    ev = pd.DataFrame(rows, columns=EVENT_COLUMNS)
    return ev[ev["duration_hours"] >= min_hours].reset_index(drop=True)


def interval_coverage(series: pd.DataFrame, start: pd.Timestamp, end: pd.Timestamp, time_col: str = "run_start_time", out_col: str = "customers_out") -> dict:
    """Expected 15-minute intervals in ``[start, end)``, intervals with a non-null value, and
    the longest run of consecutive missing intervals."""
    step = pd.Timedelta(minutes=INTERVAL_MINUTES)
    grid = pd.date_range(start, end - step, freq=step)
    t = pd.to_datetime(series.loc[series[out_col].notna(), time_col])
    present = grid.isin(t)
    longest = 0
    run = 0
    for p in present:
        run = 0 if p else run + 1
        longest = max(longest, run)
    return {
        "expected_intervals": int(len(grid)),
        "intervals_present": int(present.sum()),
        "coverage_share": float(present.mean()) if len(grid) else float("nan"),
        "longest_missing_run_intervals": int(longest),
    }


def missing_runs(series: pd.DataFrame, start: pd.Timestamp, end: pd.Timestamp, time_col: str = "run_start_time", out_col: str = "customers_out", min_intervals: int = 1) -> pd.DataFrame:
    """Runs of consecutive missing 15-minute intervals in ``[start, end)``."""
    step = pd.Timedelta(minutes=INTERVAL_MINUTES)
    grid = pd.date_range(start, end - step, freq=step)
    t = pd.to_datetime(series.loc[series[out_col].notna(), time_col])
    miss = ~grid.isin(t)
    if not miss.any():
        return pd.DataFrame(columns=["gap_start", "gap_end", "intervals"])
    idx = np.flatnonzero(miss)
    breaks = np.flatnonzero(np.diff(idx) > 1)
    starts = np.r_[idx[0], idx[breaks + 1]]
    ends = np.r_[idx[breaks], idx[-1]]
    out = pd.DataFrame({"gap_start": grid[starts], "gap_end": grid[ends] + step, "intervals": ends - starts + 1})
    return out[out["intervals"] >= min_intervals].reset_index(drop=True)
