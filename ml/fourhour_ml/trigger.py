"""Task M2: choose theta_lo and theta_hi on training years by grid search (SPEC.md 5.6).

Everything is per member-year, in units of one full scheduled payout, averaged over the
training county-years that are fitted (model universe, coverage floor). A member is assumed
to sit at a random premises in the county.

Proxy for the truth (no premises data exists for the US):
* A member's premises is "truly out 4 hours or more" in an outage episode with probability
  equal to the episode's mean share (SPEC 5.5 version 1). Episodes are the qualifying events
  at the lowest grid threshold (THETA_FLOOR). Outages below that share are invisible to
  EAGLE-I county counts and are the same for every candidate, so they do not move the choice.

For a threshold theta, E(theta) = qualifying events (4 h, hours clause) and p_e = mean share.
* W(theta) = sum over E(theta) of (1 - p_e): index payments where the premises had power.
* M(theta) = sum of p_f over floor episodes f that overlap no event in E(theta).
* T = sum of p_f over all floor episodes.

Rule table (SPEC section 4) with a share ``w`` of members holding a witness:
* No witness (share 1 - w): R2 pays 50% on theta_hi events.
  wrong = 0.5 W(hi); missed = M(hi) + 0.5 (T - M(hi)) (covered outages are half paid).
* Witness (share w): pays in full when the premises was out and a theta_lo event overlaps
  (R1, R3); R4 declines otherwise. A witness wrongly confirms an R3 payment (spoof or error)
  at rate ``eps``. wrong = eps * W_R3(lo, hi), where W_R3 sums (1 - p_e) over theta_lo events
  that contain no qualifying theta_hi event; missed = M(lo).
objective = wrong + missed. The chosen pair is the minimum; ties go to the higher pair.
"""

from __future__ import annotations

import numpy as np
import pandas as pd

from .config import R2_FACTOR
from .events import events_from_arrays
from .panel import county_customers
from .split import train_only

THETA_GRID = (0.0025, 0.005, 0.0075, 0.01, 0.015, 0.02, 0.03, 0.04, 0.05, 0.075, 0.10, 0.15, 0.20, 0.30, 0.40, 0.50, 0.60, 0.70, 0.80, 0.90)
THETA_FLOOR = THETA_GRID[0]


def county_events(df: pd.DataFrame, theta: float) -> pd.DataFrame:
    """Qualifying events (longest run 4 h or more) at ``theta`` for every county in ``df``.
    ``df`` holds readings: fips, t, customers_out."""
    cust = county_customers()
    out = []
    for fips, g in df.groupby("fips", sort=True):
        g = g.sort_values("t")
        ev = events_from_arrays(
            g["t"].to_numpy("datetime64[ns]").astype(np.int64),
            g["customers_out"].to_numpy(float),
            float(cust[fips]),
            theta,
        )
        ev = ev[ev["qualifies_t4"]]
        if len(ev):
            out.append(ev.assign(fips=fips))
    if not out:
        return pd.DataFrame(columns=["fips", "start", "end", "mean_share", "longest_run_hours", "qualifies_t24"])
    return pd.concat(out, ignore_index=True)


def _overlaps(a: pd.DataFrame, b: pd.DataFrame) -> np.ndarray:
    """For each row of ``a``: does any row of ``b`` in the same county overlap it in time?"""
    hit = np.zeros(len(a), dtype=bool)
    if a.empty or b.empty:
        return hit
    for fips, ga in a.groupby("fips"):
        gb = b[b["fips"] == fips]
        if gb.empty:
            continue
        bs = gb["start"].to_numpy()
        be = gb["end"].to_numpy()
        for i, (s, e) in zip(ga.index, zip(ga["start"].to_numpy(), ga["end"].to_numpy())):
            hit[a.index.get_loc(i)] = bool(((bs <= e) & (be >= s)).any())
    return hit


@train_only
def grid_search_components(readings: pd.DataFrame, county_years: int) -> tuple[pd.DataFrame, dict]:
    """Per-threshold quantities (per member-year) and the per-pair R3 wrong-payout term.

    ``readings`` are fitted training county-years only (the guard refuses test years).
    """
    if county_years <= 0:
        raise ValueError("county_years must be positive")
    ev = {th: county_events(readings, th) for th in THETA_GRID}
    floor = ev[THETA_FLOOR]
    T = float(floor["mean_share"].sum())
    rows = []
    for th in THETA_GRID:
        e = ev[th]
        covered = _overlaps(floor.reset_index(drop=True), e)
        rows.append(
            {
                "theta": th,
                "events": int(len(e)),
                "W": float((1 - e["mean_share"]).sum()),
                "C": float(e["mean_share"].sum()),
                "M": float(floor["mean_share"].to_numpy()[~covered].sum()),
            }
        )
    per = pd.DataFrame(rows).set_index("theta")
    w_r3 = {}
    for lo in THETA_GRID:
        e_lo = ev[lo].reset_index(drop=True)
        for hi in THETA_GRID:
            if hi <= lo:
                continue
            has_hi = _overlaps(e_lo, ev[hi])
            w_r3[(lo, hi)] = float((1 - e_lo["mean_share"].to_numpy()[~has_hi]).sum())
    scale = 1.0 / county_years
    per[["W", "C", "M"]] *= scale
    per["events_per_county_year"] = per["events"] * scale
    return per, {"T": T * scale, "W_R3": {k: v * scale for k, v in w_r3.items()}, "events": ev}


def score_grid(per: pd.DataFrame, extra: dict, witness_share: float, false_confirmation_rate: float) -> pd.DataFrame:
    w, eps, T = witness_share, false_confirmation_rate, extra["T"]
    rows = []
    for (lo, hi), wr3 in extra["W_R3"].items():
        wrong = (1 - w) * R2_FACTOR * per.at[hi, "W"] + w * eps * wr3
        missed = (1 - w) * (per.at[hi, "M"] + R2_FACTOR * (T - per.at[hi, "M"])) + w * per.at[lo, "M"]
        rows.append({"theta_lo": lo, "theta_hi": hi, "expected_wrong_payouts": wrong, "expected_missed_payouts": missed, "objective": wrong + missed})
    g = pd.DataFrame(rows)
    best = g.sort_values(["objective", "theta_hi", "theta_lo"], ascending=[True, False, False]).index[0]
    g["chosen"] = g.index == best
    return g
