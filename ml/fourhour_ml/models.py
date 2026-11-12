"""Tasks M3 and M4: frequency, severity, credibility, pricing inputs, pool simulation.

All fitting functions are ``@train_only``: they refuse any row outside 2014-2022.
Events are theta_lo qualifying events (CONTRACTS 1.7): runs at or above theta_lo lasting
4 hours or more, after gap bridging, dropouts and the hours clause.
"""

from __future__ import annotations

import numpy as np
import pandas as pd
import statsmodels.api as sm
from scipy import stats

from .config import PAYOUT_SCHEDULE, QUALIFY_HOURS, T24_HOURS
from .split import train_only

FREQ_MODELS = ("nb_glm", "poisson_glm", "historical_mean")
STATES = ("FL", "IL", "TX")


# ------------------------------------------------------------------ panel of county-months


def county_months(county_years: pd.DataFrame, events: pd.DataFrame, first_t: dict[int, pd.Timestamp] | None = None) -> pd.DataFrame:
    """One row per (fips, year, month) of the given county-years, with the number of events
    starting in it. ``first_t`` limits a year to months on or after the file's first reading
    (the 2014 file starts in November)."""
    rows = []
    for r in county_years.itertuples():
        m0 = 1
        if first_t and r.year in first_t and first_t[r.year].year == r.year:
            m0 = first_t[r.year].month
        for m in range(m0, 13):
            rows.append((r.fips, r.state, r.year, m))
    cm = pd.DataFrame(rows, columns=["fips", "state", "year", "month"])
    if len(events):
        e = events.assign(year=events["start"].dt.year, month=events["start"].dt.month)
        cnt = e.groupby(["fips", "year", "month"]).size().rename("events")
        cm = cm.join(cnt, on=["fips", "year", "month"])
    else:
        cm["events"] = 0
    cm["events"] = cm["events"].fillna(0).astype(int)
    return cm


def _design(cm: pd.DataFrame) -> pd.DataFrame:
    X = pd.DataFrame(index=cm.index)
    X["const"] = 1.0
    ang = 2 * np.pi * (cm["month"] - 0.5) / 12
    X["sin_month"] = np.sin(ang)
    X["cos_month"] = np.cos(ang)
    for s in STATES[1:]:
        X[f"state_{s}"] = (cm["state"] == s).astype(float)
    return X


# ------------------------------------------------------------------ frequency


@train_only
def fit_frequency(cm_train: pd.DataFrame) -> dict:
    """Poisson GLM and negative-binomial GLM (log link) with month-of-year (annual harmonic)
    and state effects, and the per-county historical mean. NB dispersion alpha is estimated by
    maximum likelihood (statsmodels discrete NegativeBinomial, NB2)."""
    X = _design(cm_train)
    y = cm_train["events"].to_numpy(float)
    pois = sm.GLM(y, X, family=sm.families.Poisson()).fit()
    nb = sm.NegativeBinomial(y, X, loglike_method="nb2").fit(disp=0, maxiter=500)
    alpha = float(nb.params["alpha"])
    hist = cm_train.groupby("fips")["events"].mean()
    return {"poisson": pois, "nb": nb, "alpha": alpha, "hist": hist, "columns": list(X.columns)}


def predict_frequency(fit: dict, cm: pd.DataFrame) -> pd.DataFrame:
    X = _design(cm)[fit["columns"]]
    out = cm.copy()
    out["poisson_glm"] = fit["poisson"].predict(X)
    beta = fit["nb"].params.drop("alpha")
    out["nb_glm"] = np.exp(X.to_numpy() @ beta[fit["columns"]].to_numpy())
    out["historical_mean"] = cm["fips"].map(fit["hist"]).to_numpy(float)
    return out


def _poisson_deviance(y: np.ndarray, mu: np.ndarray) -> float:
    with np.errstate(divide="ignore", invalid="ignore"):
        term = np.where(y > 0, y * np.log(y / mu), 0.0) - (y - mu)
    return float(2 * term.sum())


def _nb_logpmf(y: np.ndarray, mu: np.ndarray, alpha: float) -> np.ndarray:
    n = 1.0 / alpha
    p = n / (n + mu)
    return stats.nbinom.logpmf(y, n, p)


def evaluate_frequency(fit: dict, pred: pd.DataFrame, n_bins: int = 5) -> dict:
    """Out-of-sample deviance, log score, calibration bins on test county-months."""
    y = pred["events"].to_numpy(float)
    dev, ls, cal = {}, {}, {}
    for m in FREQ_MODELS:
        mu = pred[m].to_numpy(float)
        d = _poisson_deviance(y, mu)
        dev[m] = d if np.isfinite(d) else None
        lp = _nb_logpmf(y, mu, fit["alpha"]) if m == "nb_glm" else stats.poisson.logpmf(y, mu)
        s = float(-lp.mean())
        ls[m] = s if np.isfinite(s) else None
        ranks = pd.Series(mu).rank(method="first")
        bins = pd.qcut(ranks, n_bins, labels=False)
        cal[m] = [
            {"bin": int(b) + 1, "n": int((bins == b).sum()), "predicted_mean": float(mu[bins == b].mean()), "realised_mean": float(y[bins == b].mean())}
            for b in sorted(bins.unique())
        ]
    beats = None
    if dev["nb_glm"] is not None and ls["nb_glm"] is not None:
        others = [m for m in ("poisson_glm", "historical_mean")]
        beats = all(
            (dev[o] is None or dev["nb_glm"] < dev[o]) and (ls[o] is None or ls["nb_glm"] < ls[o]) for o in others
        )
    return {"deviance": dev, "log_score": ls, "calibration": cal, "beats_baselines": beats}


# ------------------------------------------------------------------ severity


@train_only
def fit_severity(events_train: pd.DataFrame) -> dict:
    """Generalised Pareto on the excess of the longest run over 4 hours (location fixed at 0)."""
    x = events_train["longest_run_hours"].to_numpy(float) - QUALIFY_HOURS
    x = x[x >= 0]
    shape, _, scale = stats.genpareto.fit(x, floc=0)
    ks = stats.kstest(x, "genpareto", args=(shape, 0, scale))
    n = len(x)
    q = (np.arange(1, n + 1) - 0.5) / n
    qq = [
        {"empirical_hours": float(e + QUALIFY_HOURS), "model_hours": float(m + QUALIFY_HOURS)}
        for e, m in zip(np.sort(x), stats.genpareto.ppf(q, shape, 0, scale))
    ]
    return {
        "shape": float(shape),
        "scale": float(scale),
        "n": n,
        "ks_statistic": float(ks.statistic),
        "ks_pvalue": float(ks.pvalue),
        "p24_fitted": float(stats.genpareto.sf(T24_HOURS - QUALIFY_HOURS, shape, 0, scale)),
        "p24_empirical": float((x >= T24_HOURS - QUALIFY_HOURS).mean()),
        "qq": qq,
    }


# ------------------------------------------------------------------ credibility


@train_only
def fit_credibility(cm_train: pd.DataFrame) -> dict:
    """Bühlmann-Straub: county rate (events per county-month) blended with its state's rate.

    X_ij = events per month in county i, year j; weight m_ij = months. Structural parameters
    are estimated across all counties with each state's exposure-weighted mean as its
    collective mean: s2 (expected process variance) and a (variance of county means within
    states). k = s2 / a; Z_i = m_i / (m_i + k). If a <= 0, Z_i = 0 and every county gets its
    state rate.
    """
    g = cm_train.groupby(["fips", "state", "year"]).agg(events=("events", "sum"), months=("month", "size")).reset_index()
    g["x"] = g["events"] / g["months"]
    c = g.groupby(["fips", "state"]).agg(m=("months", "sum"), ev=("events", "sum"), n=("year", "size")).reset_index()
    c["xbar"] = c["ev"] / c["m"]
    g = g.merge(c[["fips", "xbar"]], on="fips")
    dof = (c["n"] - 1).sum()
    s2 = float((g["months"] * (g["x"] - g["xbar"]) ** 2).sum() / dof) if dof > 0 else float("nan")
    st = c.groupby("state").apply(lambda d: pd.Series({"m_s": d["m"].sum(), "rate": (d["ev"].sum() / d["m"].sum()), "sum_m2": (d["m"] ** 2).sum(), "I": len(d)}), include_groups=False)
    c = c.merge(st[["rate", "m_s"]], left_on="state", right_index=True)
    num = float((c["m"] * (c["xbar"] - c["rate"]) ** 2).sum() - (len(c) - len(st)) * s2)
    den = float((st["m_s"] - st["sum_m2"] / st["m_s"]).sum())
    a = num / den if den > 0 else float("nan")
    if np.isfinite(a) and a > 0:
        k = s2 / a
        c["Z"] = c["m"] / (c["m"] + k)
    else:
        k = None
        c["Z"] = 0.0
    c["blended"] = c["Z"] * c["xbar"] + (1 - c["Z"]) * c["rate"]
    return {"s2": s2, "a": a, "k": k, "table": c.set_index("fips")}


# ------------------------------------------------------------------ pool simulation


def payout_share(n_events: int) -> np.ndarray:
    """Share of cover paid per qualifying event: the 4-hour tier only (CONTRACTS 1.8)."""
    return np.full(n_events, PAYOUT_SCHEDULE["t4"])


def simulate_year_payouts(rng: np.random.Generator, events: pd.DataFrame, covers: np.ndarray) -> tuple[int, int]:
    """Payouts (cents) and claims for one county-year of events and its members' covers.

    Every member has a witness (the priced case). For each event, each member is affected
    independently with probability equal to the event's mean share; an affected member is
    paid the 4-hour tier (t4 share of cover), rounded down to a cent. There is no 24-hour
    top-up (cut in CONTRACTS 1.8).
    """
    total, claims = 0, 0
    if events.empty or covers.size == 0:
        return 0, 0
    ucov, ncov = np.unique(covers, return_counts=True)
    t4_amt = (ucov * int(round(PAYOUT_SCHEDULE["t4"] * 100))) // 100
    for p in events["mean_share"].to_numpy(float):
        hit = rng.binomial(ncov, min(max(p, 0.0), 1.0))
        total += int((hit * t4_amt).sum())
        claims += int(hit.sum())
    return total, claims
