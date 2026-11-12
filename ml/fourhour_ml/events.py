"""Outage runs and events from a county's 15-minute EAGLE-I series.

Definitions (SPEC.md 5.1 and section 4, CONTRACTS.md 1.4 section 3, decisions D1 to D3):

* A row at time ``t`` is one reading. ``share = min(customers_out / county_customers, 1.0)``
  (D3: county customers from MCC.csv, share capped at 1.0).
* A reading is *at or above* when ``share >= threshold``. A missing row or a null
  ``customers_out`` is not a reading: it is unknown, never zero, and never above (D1).
* A **run** is a sequence of readings at or above the threshold. Missing intervals of up to
  ``gap_bridge_minutes`` between two such readings are bridged and stay inside the run.
  A run ends on an observed reading below the threshold, or on a gap longer than
  ``gap_bridge_minutes``.
* **Dropouts** (CONTRACTS 1.6 section 3): a reading below the threshold that is at most
  ``dropout_max_fraction`` of the last reading at or above it, together with any such
  readings right after it, is treated as missing when the next remaining reading is at or
  above the threshold and the gap it leaves is bridgeable (``gap_bridge_minutes``). Otherwise
  it is an ordinary below reading and ends the run.
* A run's **duration** is the time of its last reading at or above minus the time of its
  first, bridged gaps included. Its end time is its last reading at or above. A single
  reading is a run of 0 hours; a run qualifies for 4 hours at the reading 4 hours after its
  first (CONTRACTS 1.4 section 3).
* An **event** chains qualifying runs (4 hours or more) in the same county: a qualifying
  run that starts within ``event_merge_hours`` of the end of the previous qualifying run
  joins its event (hours clause, D2, CONTRACTS 1.5 section 3). A run that never qualifies is
  an event of its own and never joins two events. Durations are per run and never summed; an event qualifies for a tier if any run inside it
  does. ``longest_run_hours`` is the event's duration for severity and tiers.
"""

from __future__ import annotations

import numpy as np
import pandas as pd

from .config import DROPOUT_MAX_FRACTION, EVENT_MERGE_HOURS, GAP_BRIDGE_MINUTES, INTERVAL_MINUTES

STEP = pd.Timedelta(minutes=INTERVAL_MINUTES)
_NS_STEP = np.int64(INTERVAL_MINUTES * 60 * 10**9)
_NS_HOUR = np.int64(3600 * 10**9)

RUN_COLUMNS = ["start", "end", "duration_hours", "n_readings", "n_missing_bridged", "peak_share"]
EVENT_COLUMNS = [
    "start",
    "end",
    "longest_run_hours",
    "n_runs",
    "peak_customers_out",
    "peak_share",
    "mean_share",
    "n_missing_bridged",
    "qualifies_t4",
    "qualifies_t24",
    "first_t4_at",
    "touches_series_start",
    "touches_series_end",
]


def _clean_series(series: pd.DataFrame, time_col: str, out_col: str) -> pd.DataFrame:
    s = series[[time_col, out_col]].rename(columns={time_col: "t", out_col: "customers_out"})
    s = s.assign(t=pd.to_datetime(s["t"])).sort_values("t", kind="stable")
    if s["t"].duplicated().any():
        raise ValueError("series has duplicate timestamps; de-duplicate before reconstruction")
    if ((s["t"] - s["t"].dt.floor(STEP)) != pd.Timedelta(0)).any():
        raise ValueError(f"series has timestamps off the {INTERVAL_MINUTES}-minute grid")
    # A null customers_out is not a reading (D1): drop it, so it behaves like a missing row.
    return s[s["customers_out"].notna()].reset_index(drop=True)


def dropout_mask(t_ns: np.ndarray, share: np.ndarray, threshold: float, gap_bridge_minutes: int = GAP_BRIDGE_MINUTES, dropout_max_fraction: float | None = DROPOUT_MAX_FRACTION) -> np.ndarray:
    """True for readings to keep; False for partial-feed dropout readings (treated as missing)."""
    keep = np.ones(share.size, dtype=bool)
    if dropout_max_fraction is None or share.size < 3:
        return keep
    above = share >= threshold
    max_step = _NS_STEP + np.int64(gap_bridge_minutes) * 60 * 10**9
    n = share.size
    cand = np.flatnonzero(above[:-1] & ~above[1:] & (share[1:] <= dropout_max_fraction * share[:-1]))
    for k in cand:
        lim = dropout_max_fraction * share[k]
        j = k + 1
        while j < n and not above[j] and share[j] <= lim:
            j += 1
        if j < n and above[j] and t_ns[j] - t_ns[k] <= max_step:
            keep[k + 1 : j] = False
    return keep


def find_runs(t_ns: np.ndarray, share: np.ndarray, threshold: float, gap_bridge_minutes: int = GAP_BRIDGE_MINUTES, step_minutes: int = INTERVAL_MINUTES) -> dict[str, np.ndarray]:
    """Runs over observed readings sorted by time.

    ``t_ns`` holds int64 nanoseconds and ``share`` the share of each observed reading (no
    nulls). Returns arrays ``i0``/``i1`` (first and last reading index of each run, inclusive),
    ``start_ns``, ``end_ns``, ``n_readings`` and ``n_missing_bridged``.
    """
    if not 0 < threshold <= 1:
        raise ValueError("threshold must be a share in (0, 1]")
    above = share >= threshold
    idx = np.flatnonzero(above)
    if idx.size == 0:
        e = np.array([], dtype=np.int64)
        return {"i0": e, "i1": e, "start_ns": e, "end_ns": e, "n_readings": e, "n_missing_bridged": e}
    step_ns = np.int64(step_minutes) * 60 * 10**9
    max_step = step_ns + np.int64(gap_bridge_minutes) * 60 * 10**9
    # Two consecutive above readings stay in one run only if no observed reading lies between
    # them (an observed reading between them would be below) and the time gap is bridgeable.
    brk = (np.diff(idx) != 1) | (np.diff(t_ns[idx]) > max_step)
    first = np.r_[0, np.flatnonzero(brk) + 1]
    last = np.r_[np.flatnonzero(brk), idx.size - 1]
    i0, i1 = idx[first], idx[last]
    start_ns, end_ns = t_ns[i0], t_ns[i1]
    n_readings = i1 - i0 + 1
    n_slots = (end_ns - start_ns) // step_ns + 1
    return {
        "i0": i0,
        "i1": i1,
        "start_ns": start_ns,
        "end_ns": end_ns,
        "n_readings": n_readings,
        "n_missing_bridged": n_slots - n_readings,
    }


def merge_runs(start_ns: np.ndarray, end_ns: np.ndarray, event_merge_hours: float = EVENT_MERGE_HOURS, qualifies: np.ndarray | None = None) -> np.ndarray:
    """Event number for each run (runs sorted by start).

    Hours clause (SPEC section 4, CONTRACTS 1.5 section 3): a qualifying run that starts
    within ``event_merge_hours`` of the end of the previous qualifying run joins that run's
    event. A run that does not qualify is an event of its own: it is never merged and never
    joins two events. ``qualifies`` defaults to every run qualifying.
    """
    n = start_ns.size
    out = np.zeros(n, dtype=np.int64)
    if n == 0:
        return out
    q = np.ones(n, dtype=bool) if qualifies is None else np.asarray(qualifies, dtype=bool)
    lim = np.int64(round(event_merge_hours * 3600)) * 10**9
    ev = -1
    open_ev = -1  # event id of the last qualifying run
    last_q_end = None
    for k in range(n):
        if q[k] and last_q_end is not None and start_ns[k] - last_q_end <= lim:
            out[k] = open_ev
            last_q_end = max(last_q_end, end_ns[k])
            continue
        ev += 1
        out[k] = ev
        if q[k]:
            open_ev = ev
            last_q_end = end_ns[k]
    return out


def reconstruct_runs(
    series: pd.DataFrame,
    county_customers: float,
    threshold: float,
    *,
    gap_bridge_minutes: int = GAP_BRIDGE_MINUTES,
    dropout_max_fraction: float | None = DROPOUT_MAX_FRACTION,
    time_col: str = "run_start_time",
    out_col: str = "customers_out",
) -> pd.DataFrame:
    """One row per run (before the hours clause)."""
    if not county_customers or county_customers <= 0:
        raise ValueError("county_customers must be positive")
    s = _clean_series(series, time_col, out_col)
    t_ns = s["t"].to_numpy("datetime64[ns]").astype(np.int64)
    share = np.minimum(s["customers_out"].to_numpy(float) / float(county_customers), 1.0)
    keep = dropout_mask(t_ns, share, threshold, gap_bridge_minutes, dropout_max_fraction)
    t_ns, share = t_ns[keep], share[keep]
    r = find_runs(t_ns, share, threshold, gap_bridge_minutes)
    peak = np.array([share[a : b + 1].max() for a, b in zip(r["i0"], r["i1"])])
    return pd.DataFrame(
        {
            "start": pd.to_datetime(r["start_ns"]),
            "end": pd.to_datetime(r["end_ns"]),
            "duration_hours": (r["end_ns"] - r["start_ns"]) / _NS_HOUR,
            "n_readings": r["n_readings"],
            "n_missing_bridged": r["n_missing_bridged"],
            "peak_share": peak,
        },
        columns=RUN_COLUMNS,
    )


def events_from_arrays(
    t_ns: np.ndarray,
    customers_out: np.ndarray,
    county_customers: float,
    threshold: float,
    *,
    gap_bridge_minutes: int = GAP_BRIDGE_MINUTES,
    event_merge_hours: float = EVENT_MERGE_HOURS,
    dropout_max_fraction: float | None = DROPOUT_MAX_FRACTION,
    series_start_ns: int | None = None,
    series_end_ns: int | None = None,
) -> pd.DataFrame:
    """Events from sorted observed readings (no nulls). Fast path used by the pipeline.
    Dropout readings are removed first, so they count as bridged missing intervals and do
    not enter peak or mean share."""
    share = np.minimum(customers_out / float(county_customers), 1.0)
    keep = dropout_mask(t_ns, share, threshold, gap_bridge_minutes, dropout_max_fraction)
    t_ns, share, customers_out = t_ns[keep], share[keep], customers_out[keep]
    if t_ns.size == 0:
        return pd.DataFrame(columns=EVENT_COLUMNS)
    r = find_runs(t_ns, share, threshold, gap_bridge_minutes)
    if r["i0"].size == 0:
        return pd.DataFrame(columns=EVENT_COLUMNS)
    dur_ns = r["end_ns"] - r["start_ns"]
    ev = merge_runs(r["start_ns"], r["end_ns"], event_merge_hours, qualifies=dur_ns >= 4 * _NS_HOUR)
    cs = np.r_[0.0, np.cumsum(share)]
    rows = []
    order = np.argsort(ev, kind="stable")
    r = {k: v[order] for k, v in r.items()}
    dur_ns, ev = dur_ns[order], ev[order]
    bounds = np.flatnonzero(np.r_[True, np.diff(ev) != 0, True])
    lo_ns = t_ns[0] if series_start_ns is None else np.int64(series_start_ns)
    hi_ns = t_ns[-1] if series_end_ns is None else np.int64(series_end_ns)
    edge = _NS_STEP + np.int64(gap_bridge_minutes) * 60 * 10**9
    for a, b in zip(bounds[:-1], bounds[1:]):
        k = slice(a, b)
        i_first, i_last = r["i0"][k].min(), r["i1"][k].max()
        start, end = r["start_ns"][k].min(), r["end_ns"][k].max()
        win = slice(i_first, i_last + 1)
        d = dur_ns[k]
        q4 = d >= 4 * _NS_HOUR
        first_t4 = (r["start_ns"][k][q4] + 4 * _NS_HOUR).min() if q4.any() else None
        rows.append(
            {
                "start": start,
                "end": end,
                "longest_run_hours": float(d.max() / _NS_HOUR),
                "n_runs": int(b - a),
                "peak_customers_out": float(customers_out[win].max()),
                "peak_share": float(share[win].max()),
                "mean_share": float((cs[i_last + 1] - cs[i_first]) / (i_last + 1 - i_first)),
                "n_missing_bridged": int(r["n_missing_bridged"][k].sum()),
                "qualifies_t4": bool(q4.any()),
                "qualifies_t24": bool((d >= 24 * _NS_HOUR).any()),
                "first_t4_at": first_t4,
                "touches_series_start": bool(start - lo_ns < edge),
                "touches_series_end": bool(hi_ns - end < edge),
            }
        )
    out = pd.DataFrame(rows, columns=EVENT_COLUMNS).sort_values("start", kind="stable").reset_index(drop=True)
    out["start"] = pd.to_datetime(out["start"])
    out["end"] = pd.to_datetime(out["end"])
    out["first_t4_at"] = pd.to_datetime(out["first_t4_at"])
    return out


def reconstruct_events(
    series: pd.DataFrame,
    county_customers: float,
    threshold: float,
    *,
    min_hours: float = 0.0,
    gap_bridge_minutes: int = GAP_BRIDGE_MINUTES,
    event_merge_hours: float = EVENT_MERGE_HOURS,
    dropout_max_fraction: float | None = DROPOUT_MAX_FRACTION,
    time_col: str = "run_start_time",
    out_col: str = "customers_out",
    series_start: pd.Timestamp | None = None,
    series_end: pd.Timestamp | None = None,
) -> pd.DataFrame:
    """One row per event whose longest run lasts ``min_hours`` or more.

    ``mean_share`` is the mean share over the observed readings in the event window
    (``start`` to ``end``), below-threshold readings between merged runs included.
    ``series_start`` / ``series_end`` bound the period the series covers; an event within a
    bridgeable gap of either bound is flagged, because it may continue outside the data.
    """
    if not county_customers or county_customers <= 0:
        raise ValueError("county_customers must be positive")
    if not 0 < threshold <= 1:
        raise ValueError("threshold must be a share in (0, 1]")
    s = _clean_series(series, time_col, out_col)
    if s.empty:
        return pd.DataFrame(columns=EVENT_COLUMNS)
    t_ns = s["t"].to_numpy("datetime64[ns]").astype(np.int64)
    ev = events_from_arrays(
        t_ns,
        s["customers_out"].to_numpy(float),
        county_customers,
        threshold,
        gap_bridge_minutes=gap_bridge_minutes,
        event_merge_hours=event_merge_hours,
        dropout_max_fraction=dropout_max_fraction,
        series_start_ns=None if series_start is None else pd.Timestamp(series_start).value,
        series_end_ns=None if series_end is None else pd.Timestamp(series_end).value,
    )
    return ev[ev["longest_run_hours"] >= min_hours].reset_index(drop=True)


def interval_coverage(series: pd.DataFrame, start: pd.Timestamp, end: pd.Timestamp, time_col: str = "run_start_time", out_col: str = "customers_out") -> dict:
    """Expected 15-minute intervals in ``[start, end)``, intervals with a non-null value, and
    the longest run of consecutive missing intervals."""
    grid = pd.date_range(start, end - STEP, freq=STEP)
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
    grid = pd.date_range(start, end - STEP, freq=STEP)
    t = pd.to_datetime(series.loc[series[out_col].notna(), time_col])
    miss = ~grid.isin(t)
    if not miss.any():
        return pd.DataFrame(columns=["gap_start", "gap_end", "intervals"])
    idx = np.flatnonzero(miss)
    breaks = np.flatnonzero(np.diff(idx) > 1)
    starts = np.r_[idx[0], idx[breaks + 1]]
    ends = np.r_[idx[breaks], idx[-1]]
    out = pd.DataFrame({"gap_start": grid[starts], "gap_end": grid[ends] + STEP, "intervals": ends - starts + 1})
    return out[out["intervals"] >= min_intervals].reset_index(drop=True)
