"""Task M6: basis-risk study on ESMI premises data (SPEC.md section 6).

Data rules (ml/README.md, esmi.py): a 0 V minute is no supply; a minute with no reading is a
monitor that is offline or not reporting (source: "About the initiative.pdf").

* District index, per minute: monitors reading 0 / monitors reporting. It is defined only in
  minutes when at least ``cutoff`` monitors report (S4 eligibility); otherwise it is missing.
* Index runs and premises outage runs use the same run rules as EAGLE-I (missing gaps of up
  to 120 minutes between readings at or above the threshold are bridged; an observed reading
  below ends the run; duration = last minus first reading; 4 hours qualifies), at 1-minute
  steps. The EAGLE-I dropout rule is not applied (it describes a utility-feed failure). Runs
  are not merged by the hours clause, so each qualifying run is one index event.
* A premises-event is a premises outage run of 4 hours or more that starts in a minute when
  its district index is defined (so the index had a chance to see it).
* A payment is a (premises, index event) pair with the premises' monitor reporting during the
  event. It is wrong when the premises had no outage run of 4 hours or more overlapping the
  event. A premises-event is missed when no payment overlaps it.
* Schemes: index_only pays on every theta_hi event. The rule table (SPEC section 4) with the
  premises' own monitor as witness pays when the premises' outage of 4 hours or more overlaps
  a theta_lo event (R1, R3) and declines otherwise (R4). With a share of witness logs missing
  at random, a pair with no log is paid at 50% under R2 if the event reaches theta_hi (counted
  as a payment), and not paid otherwise.
* Thresholds are chosen on the first half of the period with the same objective and product
  parameters as trigger_params.json (M2), using the premises truth instead of a proxy, and
  scored on the second half.
"""

from __future__ import annotations

import numpy as np
import pandas as pd

from . import esmi
from .config import FALSE_CONFIRMATION_RATE, GAP_BRIDGE_MINUTES, QUALIFY_HOURS, R2_FACTOR, SEED, WITNESS_SHARE
from .events import find_runs

MIN_NS = np.int64(60 * 10**9)
Q_MIN = QUALIFY_HOURS * 60
THETA_GRID = (0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0)
MISSING_SHARES = {"rules_10pct_missing": 0.10, "rules_30pct_missing": 0.30}


def load_minutes(districts: pd.DataFrame, locations: pd.DataFrame) -> dict:
    """Per-monitor minute state arrays (int8: -1 no reading, 0 no supply, 1 supply)."""
    keep = locations.merge(districts[["State", "District"]], on=["State", "District"])
    locs = sorted(keep["Location name"])
    pos = {l: i for i, l in enumerate(locs)}
    t0 = pd.Timestamp("2014-10-01")
    t1 = pd.Timestamp("2019-01-01")
    n_min = int((t1 - t0) / pd.Timedelta(minutes=1))
    state = np.full((len(locs), n_min), -1, dtype=np.int8)
    stats = []
    for vf in esmi.VOLTAGE_FILES:
        if not vf.use:
            continue
        df, st = esmi.read_voltage(vf, keep_locations=set(locs))
        vals = st.pop("_values")
        if len(df) == 0:
            continue
        base = ((df["date"] + pd.to_timedelta(df["hour"], unit="h") - t0) / pd.Timedelta(minutes=1)).astype(np.int64).to_numpy()
        rows = df["loc"].map(pos).to_numpy()
        code = np.where(vals == 0, 0, 1).astype(np.int8)
        cols = base[:, None] + np.arange(60)[None, :]
        state[rows[:, None], cols] = code
        stats.append({"file": vf.name, "rows": int(len(df))})
    return {"locs": locs, "loc_district": keep.set_index("Location name").loc[locs, ["State", "District"]].reset_index(drop=True), "t0": t0, "state": state, "stats": stats}


def _runs_minutes(t_min: np.ndarray, share: np.ndarray, threshold: float) -> tuple[np.ndarray, np.ndarray]:
    """Qualifying runs (4 h or more): start and end minute indices."""
    r = find_runs(t_min.astype(np.int64) * MIN_NS, share, threshold, GAP_BRIDGE_MINUTES, step_minutes=1)
    s = r["start_ns"] // MIN_NS
    e = r["end_ns"] // MIN_NS
    q = (e - s) >= Q_MIN
    return s[q], e[q]


def district_inputs(data: dict, cutoff: int) -> list[dict]:
    """Per district: index (NaN where undefined), premises outage runs, monitor reporting."""
    st = data["state"]
    out = []
    for (state_name, district), g in data["loc_district"].groupby(["State", "District"]):
        idx = g.index.to_numpy()
        S = st[idx]
        rep = S >= 0
        active = rep.sum(0)
        outc = (S == 0).sum(0)
        defined = active >= cutoff
        index = np.full(S.shape[1], np.nan)
        index[defined] = outc[defined] / active[defined]
        prem = []
        for k in range(S.shape[0]):
            obs = np.flatnonzero(rep[k])
            if obs.size == 0:
                prem.append((np.array([], dtype=np.int64), np.array([], dtype=np.int64)))
                continue
            s, e = _runs_minutes(obs, (S[k, obs] == 0).astype(float), 0.5)
            ok = defined[s] if s.size else np.array([], dtype=bool)
            prem.append((s[ok], e[ok]))
        out.append({"state": state_name, "district": district, "index": index, "defined": defined, "reporting": rep, "premises": prem, "n": S.shape[0]})
    return out


def index_events(d: dict, theta: float) -> tuple[np.ndarray, np.ndarray]:
    t = np.flatnonzero(~np.isnan(d["index"]))
    if t.size == 0:
        return np.array([], dtype=np.int64), np.array([], dtype=np.int64)
    return _runs_minutes(t, d["index"][t], theta)


def _overlap(s1, e1, s2, e2) -> np.ndarray:
    """Matrix [i, j]: interval i of set 1 overlaps interval j of set 2."""
    if len(s1) == 0 or len(s2) == 0:
        return np.zeros((len(s1), len(s2)), dtype=bool)
    return (s1[:, None] <= e2[None, :]) & (e1[:, None] >= s2[None, :])


def pairs(d: dict, ev_s: np.ndarray, ev_e: np.ndarray) -> dict:
    """For each (premises k, event j): reporting during event, and true outage overlap."""
    n, m = d["n"], len(ev_s)
    rep = np.zeros((n, m), dtype=bool)
    true = np.zeros((n, m), dtype=bool)
    cs = np.concatenate([np.zeros((n, 1), dtype=np.int64), np.cumsum(d["reporting"], axis=1, dtype=np.int64)], axis=1)
    for j in range(m):
        rep[:, j] = (cs[:, ev_e[j] + 1] - cs[:, ev_s[j]]) > 0
        for k in range(n):
            ps, pe = d["premises"][k]
            if len(ps):
                true[k, j] = bool(((ps <= ev_e[j]) & (pe >= ev_s[j])).any())
    return {"rep": rep, "true": true}


def _period(s: np.ndarray, lo: int, hi: int) -> np.ndarray:
    return (s >= lo) & (s < hi)


def score(dists: list[dict], theta_lo: float, theta_hi: float, lo: int, hi: int, missing: float | None, rng: np.random.Generator | None) -> dict:
    """Counts for one scheme in the period [lo, hi) of minute indices (by start time).

    ``missing`` None with theta_lo None means index_only at theta_hi; a float is the share of
    witness logs missing at random (0.0 = perfect witness)."""
    premises_events = paid = wrong = missed = 0
    for d in dists:
        hs, he = index_events(d, theta_hi)
        keep = _period(hs, lo, hi)
        hs, he = hs[keep], he[keep]
        if theta_lo is None:
            es, ee = hs, he
            has_hi = np.ones(len(es), dtype=bool)
        else:
            es, ee = index_events(d, theta_lo)
            keep = _period(es, lo, hi)
            es, ee = es[keep], ee[keep]
            has_hi = _overlap(es, ee, hs, he).any(axis=1)
        P = pairs(d, es, ee)
        rep, true = P["rep"], P["true"]
        if theta_lo is None:
            pay = rep
        else:
            log = rep & (rng.random(rep.shape) >= missing) if missing else rep.copy()
            pay = (log & true) | (rep & ~log & has_hi[None, :])
        paid += int(pay.sum())
        wrong += int((pay & ~true).sum())
        for k in range(d["n"]):
            ps, pe = d["premises"][k]
            sel = _period(ps, lo, hi)
            ps, pe = ps[sel], pe[sel]
            premises_events += len(ps)
            if len(ps) == 0:
                continue
            ov = _overlap(ps, pe, es, ee) & pay[k][None, :]
            missed += int((~ov.any(axis=1)).sum())
    return {
        "premises_events": premises_events,
        "paid": paid,
        "missed": missed,
        "wrong": wrong,
        "missed_rate": missed / premises_events if premises_events else None,
        "wrong_rate": wrong / paid if paid else None,
    }


def choose_thresholds(dists: list[dict], lo: int, split: int) -> tuple[pd.DataFrame, float, float]:
    """Grid search on the first half only (minutes before ``split``), M2 objective."""
    if lo >= split:
        raise ValueError("fit period is empty")
    w, eps = WITNESS_SHARE, FALSE_CONFIRMATION_RATE
    per = {}
    for th in THETA_GRID:
        tot_pairs = tot_wrong = 0
        cov = 0
        n_true = 0
        evs = {}
        for d in dists:
            s, e = index_events(d, th)
            keep = s < split
            s, e = s[keep & (s >= lo)], e[keep & (s >= lo)]
            evs[d["district"]] = (s, e)
            P = pairs(d, s, e)
            tot_pairs += int(P["rep"].sum())
            tot_wrong += int((P["rep"] & ~P["true"]).sum())
            for k in range(d["n"]):
                ps, pe = d["premises"][k]
                sel = (ps >= lo) & (ps < split)
                n_true += int(sel.sum())
                if sel.any():
                    cov += int(_overlap(ps[sel], pe[sel], s, e).any(axis=1).sum())
        per[th] = {"pairs": tot_pairs, "wrong": tot_wrong, "covered": cov, "n_true": n_true, "events": evs}
    rows = []
    for tl in THETA_GRID:
        for th in THETA_GRID:
            if th <= tl:
                continue
            w_r3 = 0
            for d in dists:
                s, e = per[tl]["events"][d["district"]]
                hs, he = per[th]["events"][d["district"]]
                no_hi = ~_overlap(s, e, hs, he).any(axis=1)
                P = pairs(d, s[no_hi], e[no_hi])
                w_r3 += int((P["rep"] & ~P["true"]).sum())
            n_true = per[th]["n_true"]
            wrong = (1 - w) * R2_FACTOR * per[th]["wrong"] + w * eps * w_r3
            missed = (1 - w) * ((n_true - per[th]["covered"]) + R2_FACTOR * per[th]["covered"]) + w * (n_true - per[tl]["covered"])
            rows.append({"theta_lo": tl, "theta_hi": th, "wrong": wrong, "missed": missed, "objective": wrong + missed})
    g = pd.DataFrame(rows)
    best = g.sort_values(["objective", "theta_hi", "theta_lo"], ascending=[True, False, False]).iloc[0]
    return g, float(best.theta_lo), float(best.theta_hi)


def run_study(cutoff: int = esmi.CHOSEN_CUTOFF) -> dict:
    locations = esmi.read_locations()
    act, file_stats = esmi.active_hours()
    split_t = esmi.split_time(act)
    conc = esmi.concurrency(act, locations)
    el = esmi.eligibility(conc, split_t, cutoff, esmi.MIN_USABLE_HOURS)
    districts = el[el["passes"]].reset_index(drop=True)
    data = load_minutes(districts, locations)
    dists = district_inputs(data, cutoff)
    t0 = data["t0"]
    lo = int((act["t"].min() - t0) / pd.Timedelta(minutes=1))
    split = int((split_t - t0) / pd.Timedelta(minutes=1))
    hi = int((act["t"].max() + pd.Timedelta(hours=1) - t0) / pd.Timedelta(minutes=1))
    grid, tl, th = choose_thresholds(dists, lo, split)
    rng = np.random.default_rng(SEED)
    schemes = {
        "index_only": score(dists, None, th, split, hi, None, None),
        "rules_perfect_witness": score(dists, tl, th, split, hi, 0.0, rng),
    }
    for name, m in MISSING_SHARES.items():
        schemes[name] = score(dists, tl, th, split, hi, m, rng)
    fit_counts = score(dists, None, th, lo, split, None, None)
    return {
        "districts": districts,
        "schemes": schemes,
        "fit_index_only": fit_counts,
        "grid": grid,
        "theta_lo": tl,
        "theta_hi": th,
        "fit_period": (act["t"].min(), split_t - pd.Timedelta(days=1)),
        "score_period": (split_t, act["t"].max()),
        "file_stats": file_stats,
        "n_monitors": int(sum(d["n"] for d in dists)),
        "defined_share": {d["district"]: float(d["defined"][lo:hi].mean()) for d in dists},
        "cutoff": cutoff,
    }
