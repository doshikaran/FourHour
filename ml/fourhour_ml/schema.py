"""Artifact schema check against CONTRACTS.md section 8 and 8.1 (version 1.7).

``write_artifact`` validates before writing; a file that fails is never written. Every key
section 8.1 pins is enforced exactly (name, type, nullability); unknown keys are rejected.
"""

from __future__ import annotations

import json
import math
import re
import subprocess
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, TypeGuard

from .config import ARTIFACTS_DIR, REPO_DIR, SEED, TEST_YEARS, TRAIN_YEARS

CONTRACT_VERSION = "1.7"

ISO_Z = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$")
FIPS = re.compile(r"^\d{5}$")
SCENARIO_ID = re.compile(r"^[a-z0-9][a-z0-9-]*$")

ARTIFACT_NAMES = (
    "trigger_params",
    "pricing_table",
    "backtest",
    "solvency",
    "basis_risk",
    "data_quality",
)
BASIS_RISK_SCHEMES = ("index_only", "rules_perfect_witness", "rules_10pct_missing", "rules_30pct_missing")
# The quote breakdown fields (CONTRACTS.md section 4) that do not depend on cover_cents.
STOP_LOSS_KEYS = ("attachment_cents", "limit_cents", "price_cents")
PRICING_COUNTY_FIELDS = ("expected_events_per_year", "prob_24h_given_event", "prob_premises_affected", "credibility_weight")

GRID_KEYS = ("theta_lo", "theta_hi", "expected_wrong_payouts", "expected_missed_payouts", "objective", "chosen")
FREQ_MODELS = ("nb_glm", "poisson_glm", "historical_mean")
VARIANTS = ("exclude_low_coverage", "include_low_coverage")
VARIANT_KEYS = ("county_years_fitted", "per_county_year", "deviance", "log_score", "calibration", "beats_baselines")
LOSS_QUANTILES = (0.5, 0.9, 0.99, 0.995)
DQ_ROW_KEYS = ("fips", "county", "state", "year", "county_customers", "expected_intervals", "intervals_present", "coverage_share", "zero_rows", "max_customers_out", "intervals_over_county_customers", "in_model_universe", "excluded_from_fitting", "exclusion_reason")
THETA_HI_STATUS = ("interior_optimum", "grid_edge_no_interior_optimum")
DATE = re.compile(r"^\d{4}-\d{2}-\d{2}$")

# CONTRACTS 1.7 section 8.1 pins every shape; nothing is left open.
UNSPECIFIED: dict[str, str] = {}


class ArtifactSchemaError(ValueError):
    def __init__(self, errors: list[str]):
        super().__init__("; ".join(errors))
        self.errors = errors


# ---------------------------------------------------------------- primitive checks


def _is_num(v: Any) -> bool:
    return isinstance(v, (int, float)) and not isinstance(v, bool) and math.isfinite(v)


def _is_int(v: Any) -> bool:
    return isinstance(v, int) and not isinstance(v, bool)


class _V:
    def __init__(self) -> None:
        self.errors: list[str] = []
        self.has_null = False

    def err(self, path: str, msg: str) -> None:
        self.errors.append(f"{path}: {msg}")

    def obj(self, d: Any, path: str, required: tuple[str, ...], allow_extra: bool) -> TypeGuard[dict[str, Any]]:
        """True when ``d`` is a dict (missing or extra keys are recorded as errors but do not
        make it False), so callers can go on checking its fields."""
        if not isinstance(d, dict):
            self.err(path, "must be an object")
            return False
        for k in required:
            if k not in d:
                self.err(path, f"missing key {k!r}")
        if not allow_extra:
            for k in d:
                if k not in required:
                    self.err(path, f"unexpected key {k!r}")
        return True

    def _null(self, d: dict, k: str, nullable: bool, path: str) -> bool:
        """True if the value is null (and acceptable), so the caller stops checking it."""
        if d.get(k) is None:
            if not nullable:
                self.err(f"{path}.{k}", "must not be null")
            else:
                self.has_null = True
            return True
        return False

    def num(self, d: dict, k: str, path: str, nullable: bool = True, lo: float | None = None, hi: float | None = None) -> None:
        if k not in d or self._null(d, k, nullable, path):
            return
        v = d[k]
        if not _is_num(v):
            self.err(f"{path}.{k}", f"must be a finite number, got {v!r}")
        elif (lo is not None and v < lo) or (hi is not None and v > hi):
            self.err(f"{path}.{k}", f"{v} outside [{lo}, {hi}]")

    def int_(self, d: dict, k: str, path: str, nullable: bool = True, lo: int | None = None) -> None:
        if k not in d or self._null(d, k, nullable, path):
            return
        v = d[k]
        if not _is_int(v):
            self.err(f"{path}.{k}", f"must be an integer, got {v!r}")
        elif lo is not None and v < lo:
            self.err(f"{path}.{k}", f"{v} < {lo}")

    def str_(self, d: dict, k: str, path: str, nullable: bool = False, pattern: re.Pattern | None = None) -> None:
        if k not in d or self._null(d, k, nullable, path):
            return
        v = d[k]
        if not isinstance(v, str) or not v:
            self.err(f"{path}.{k}", "must be a non-empty string")
        elif pattern is not None and not pattern.match(v):
            self.err(f"{path}.{k}", f"{v!r} does not match {pattern.pattern}")

    def present(self, d: dict, k: str, path: str) -> None:
        if k in d and d[k] is None:
            self.has_null = True


# ---------------------------------------------------------------- provenance


PROVENANCE_KEYS = ("generated_at", "git_commit", "code", "data_sources", "train_years", "test_years", "seed", "notes")
DATA_SOURCE_KEYS = ("name", "url", "sha256", "rows_used")


def _check_provenance(v: _V, doc: dict) -> None:
    p = doc.get("provenance")
    if not isinstance(p, dict):
        v.err("provenance", "must be an object")
        return
    v.obj(p, "provenance", PROVENANCE_KEYS, allow_extra=False)
    v.str_(p, "generated_at", "provenance", pattern=ISO_Z)
    v.str_(p, "git_commit", "provenance")
    v.str_(p, "code", "provenance")
    if "notes" in p and not isinstance(p["notes"], str):
        v.err("provenance.notes", "must be a string")
    v.int_(p, "seed", "provenance", nullable=False)
    for key, expected in (("train_years", TRAIN_YEARS), ("test_years", TEST_YEARS)):
        if key in p and p[key] != list(expected):
            v.err(f"provenance.{key}", f"must be {list(expected)}, got {p[key]!r}")
    ds = p.get("data_sources")
    if "data_sources" in p:
        if not isinstance(ds, list) or not ds:
            v.err("provenance.data_sources", "must be a non-empty list")
        else:
            for i, s in enumerate(ds):
                path = f"provenance.data_sources[{i}]"
                if v.obj(s, path, DATA_SOURCE_KEYS, allow_extra=False):
                    v.str_(s, "name", path)
                    v.str_(s, "url", path)
                    v.str_(s, "sha256", path, pattern=re.compile(r"^[0-9a-f]{64}$"))
                    v.int_(s, "rows_used", path, nullable=False, lo=0)


# ---------------------------------------------------------------- per-artifact checks


def _check_trigger_params(v: _V, d: dict) -> None:
    v.obj(d, "", ("theta_hi", "theta_lo", "gap_bridge_minutes", "event_merge_hours", "dropout_max_fraction", "theta_hi_status", "sensitivity", "grid", "provenance"), allow_extra=False)
    if d.get("theta_hi_status") not in THETA_HI_STATUS:
        v.err("theta_hi_status", f"must be one of {THETA_HI_STATUS}")
    sens = d.get("sensitivity")
    if not isinstance(sens, list) or not sens:
        v.err("sensitivity", "must be a non-empty list")
    else:
        for i, it in enumerate(sens):
            path = f"sensitivity[{i}]"
            if v.obj(it, path, ("witness_share", "false_confirmation_rate", "theta_lo", "theta_hi", "theta_hi_status", "objective"), allow_extra=False):
                for k in ("witness_share", "false_confirmation_rate", "theta_lo", "theta_hi"):
                    v.num(it, k, path, nullable=False, lo=0, hi=1)
                v.num(it, "objective", path, lo=0)
                if it.get("theta_hi_status") not in THETA_HI_STATUS:
                    v.err(f"{path}.theta_hi_status", f"must be one of {THETA_HI_STATUS}")
    v.num(d, "theta_hi", "", lo=0, hi=1)
    v.num(d, "theta_lo", "", lo=0, hi=1)
    v.int_(d, "gap_bridge_minutes", "", nullable=False, lo=0)
    v.num(d, "event_merge_hours", "", nullable=False, lo=0)
    v.num(d, "dropout_max_fraction", "", nullable=False, lo=0, hi=1)
    if _is_num(d.get("theta_hi")) and _is_num(d.get("theta_lo")) and not d["theta_lo"] < d["theta_hi"]:
        v.err("theta_lo", "must be below theta_hi (SPEC.md section 4)")
    g = d.get("grid")
    if "grid" in d:
        if not isinstance(g, list) or not g:
            v.err("grid", "must be a non-empty list")
            return
        chosen = 0
        for i, it in enumerate(g):
            path = f"grid[{i}]"
            if v.obj(it, path, GRID_KEYS, allow_extra=False):
                v.num(it, "theta_lo", path, nullable=False, lo=0, hi=1)
                v.num(it, "theta_hi", path, nullable=False, lo=0, hi=1)
                for k in ("expected_wrong_payouts", "expected_missed_payouts", "objective"):
                    v.num(it, k, path, lo=0)
                if not isinstance(it.get("chosen"), bool):
                    v.err(f"{path}.chosen", "must be a boolean")
                elif it["chosen"]:
                    chosen += 1
                    if it.get("theta_lo") != d.get("theta_lo") or it.get("theta_hi") != d.get("theta_hi"):
                        v.err(path, "the chosen pair must equal theta_lo and theta_hi")
        if d.get("theta_hi") is not None and chosen != 1:
            v.err("grid", f"exactly one item must be chosen, got {chosen}")


def _check_pricing_table(v: _V, d: dict) -> None:
    v.obj(d, "", ("model_version", "counties", "rates", "payout_schedule", "provenance"), allow_extra=False)
    v.str_(d, "model_version", "")
    c = d.get("counties")
    if "counties" in d and v.obj(c, "counties", (), allow_extra=True):
        if not c:
            v.err("counties", "must not be empty")
        for fips, row in c.items():
            path = f"counties.{fips}"
            if not FIPS.match(str(fips)):
                v.err(path, "key must be a 5-digit FIPS string")
            if v.obj(row, path, PRICING_COUNTY_FIELDS, allow_extra=False):
                v.num(row, "expected_events_per_year", path, lo=0)
                v.num(row, "prob_24h_given_event", path, lo=0, hi=1)
                v.num(row, "prob_premises_affected", path, lo=0, hi=1)
                v.num(row, "credibility_weight", path, lo=0, hi=1)
    r = d.get("rates")
    if "rates" in d and v.obj(r, "rates", ("risk_margin_rate", "expense_load_cents"), allow_extra=False):
        v.num(r, "risk_margin_rate", "rates", lo=0)
        v.int_(r, "expense_load_cents", "rates", lo=0)
    s = d.get("payout_schedule")
    if "payout_schedule" in d and v.obj(s, "payout_schedule", ("t4", "t24"), allow_extra=False):
        v.num(s, "t4", "payout_schedule", nullable=False, lo=0, hi=1)
        v.num(s, "t24", "payout_schedule", nullable=False, lo=0, hi=1)
        if _is_num(s.get("t4")) and _is_num(s.get("t24")) and s["t4"] > s["t24"]:
            v.err("payout_schedule", "t4 must not exceed t24")


def _num_map(v: _V, d: Any, path: str, keys: tuple[str, ...], lo: float | None = None) -> None:
    if v.obj(d, path, keys, allow_extra=False):
        for k in keys:
            v.num(d, k, path, lo=lo)


def _check_variant(v: _V, V: Any, path: str) -> None:
    if not v.obj(V, path, VARIANT_KEYS, allow_extra=False):
        return
    v.int_(V, "county_years_fitted", path, nullable=False, lo=0)
    rows = V.get("per_county_year")
    if not isinstance(rows, list):
        v.err(f"{path}.per_county_year", "must be a list")
    else:
        for i, r in enumerate(rows):
            p = f"{path}.per_county_year[{i}]"
            if v.obj(r, p, ("fips", "year", "realised") + FREQ_MODELS, allow_extra=False):
                v.str_(r, "fips", p, pattern=FIPS)
                v.int_(r, "year", p, nullable=False)
                v.int_(r, "realised", p, nullable=False, lo=0)
                for m in FREQ_MODELS:
                    v.num(r, m, p, lo=0)
    _num_map(v, V.get("deviance"), f"{path}.deviance", FREQ_MODELS, lo=0)
    _num_map(v, V.get("log_score"), f"{path}.log_score", FREQ_MODELS)
    cal = V.get("calibration")
    if v.obj(cal, f"{path}.calibration", FREQ_MODELS, allow_extra=False):
        for m in FREQ_MODELS:
            bins = cal.get(m)
            if not isinstance(bins, list):
                v.err(f"{path}.calibration.{m}", "must be a list")
                continue
            for i, b in enumerate(bins):
                p = f"{path}.calibration.{m}[{i}]"
                if v.obj(b, p, ("bin", "n", "predicted_mean", "realised_mean"), allow_extra=False):
                    v.int_(b, "bin", p, nullable=False, lo=1)
                    v.int_(b, "n", p, nullable=False, lo=0)
                    v.num(b, "predicted_mean", p, lo=0)
                    v.num(b, "realised_mean", p, lo=0)
    if not (V.get("beats_baselines") is None or isinstance(V.get("beats_baselines"), bool)):
        v.err(f"{path}.beats_baselines", "must be a boolean or null")
    v.present(V, "beats_baselines", path)


def _check_backtest(v: _V, d: dict) -> None:
    v.obj(d, "", ("frequency", "severity", "credibility", "pool", "provenance"), allow_extra=False)
    f = d.get("frequency")
    if v.obj(f, "frequency", ("threshold", "theta", "unit", "primary_variant", "variants"), allow_extra=False):
        if f.get("threshold") != "theta_lo":
            v.err("frequency.threshold", "must be 'theta_lo' (CONTRACTS 1.7 section 8.1)")
        v.num(f, "theta", "frequency", nullable=False, lo=0, hi=1)
        v.str_(f, "unit", "frequency")
        if f.get("primary_variant") not in VARIANTS:
            v.err("frequency.primary_variant", f"must be one of {VARIANTS}")
        vs = f.get("variants")
        if v.obj(vs, "frequency.variants", VARIANTS, allow_extra=False):
            for k in VARIANTS:
                _check_variant(v, vs.get(k), f"frequency.variants.{k}")
    sv = d.get("severity")
    sev_keys = ("threshold_hours", "n_events_train", "n_events_test", "gpd", "ks_statistic", "ks_pvalue", "prob_24h_given_event", "qq")
    if v.obj(sv, "severity", sev_keys, allow_extra=False):
        v.num(sv, "threshold_hours", "severity", nullable=False, lo=0)
        v.int_(sv, "n_events_train", "severity", nullable=False, lo=0)
        v.int_(sv, "n_events_test", "severity", nullable=False, lo=0)
        g = sv.get("gpd")
        if g is None:
            v.present(sv, "gpd", "severity")
        elif v.obj(g, "severity.gpd", ("shape", "scale", "location_hours"), allow_extra=False):
            v.num(g, "shape", "severity.gpd")
            v.num(g, "scale", "severity.gpd", lo=0)
            v.num(g, "location_hours", "severity.gpd", lo=0)
        v.num(sv, "ks_statistic", "severity", lo=0, hi=1)
        v.num(sv, "ks_pvalue", "severity", lo=0, hi=1)
        p24 = sv.get("prob_24h_given_event")
        if v.obj(p24, "severity.prob_24h_given_event", ("fitted", "empirical_train", "empirical_test"), allow_extra=False):
            for k in ("fitted", "empirical_train", "empirical_test"):
                v.num(p24, k, "severity.prob_24h_given_event", lo=0, hi=1)
        qq = sv.get("qq")
        if not isinstance(qq, list):
            v.err("severity.qq", "must be a list")
        else:
            for i, pt in enumerate(qq):
                _num_map(v, pt, f"severity.qq[{i}]", ("empirical_hours", "model_hours"), lo=0)
    c = d.get("credibility")
    if v.obj(c, "credibility", ("method", "k", "counties"), allow_extra=False):
        if c.get("method") != "buhlmann_straub":
            v.err("credibility.method", "must be 'buhlmann_straub'")
        v.num(c, "k", "credibility", lo=0)
        cs = c.get("counties")
        if v.obj(cs, "credibility.counties", (), allow_extra=True):
            for fips, row in cs.items():
                p = f"credibility.counties.{fips}"
                if not FIPS.match(str(fips)):
                    v.err(p, "key must be a 5-digit FIPS string")
                if v.obj(row, p, ("state", "county_rate", "state_rate", "exposure_months", "weight", "blended_rate"), allow_extra=False):
                    v.str_(row, "state", p)
                    v.num(row, "county_rate", p, lo=0)
                    v.num(row, "state_rate", p, lo=0)
                    v.int_(row, "exposure_months", p, nullable=False, lo=0)
                    v.num(row, "weight", p, lo=0, hi=1)
                    v.num(row, "blended_rate", p, lo=0)
    pool = d.get("pool")
    if v.obj(pool, "pool", ("members", "years"), allow_extra=False):
        v.int_(pool, "members", "pool", nullable=False, lo=1)
        ys = pool.get("years")
        if not isinstance(ys, list):
            v.err("pool.years", "must be a list")
        else:
            for i, y in enumerate(ys):
                p = f"pool.years[{i}]"
                if v.obj(y, p, ("year", "premiums_cents", "payouts_cents", "loss_ratio", "events", "events_by_county", "claims_paid"), allow_extra=False):
                    v.int_(y, "year", p, nullable=False)
                    v.int_(y, "premiums_cents", p, lo=0)
                    v.int_(y, "payouts_cents", p, lo=0)
                    v.num(y, "loss_ratio", p, lo=0)
                    v.int_(y, "events", p, nullable=False, lo=0)
                    v.int_(y, "claims_paid", p, lo=0)
                    ebc = y.get("events_by_county")
                    if v.obj(ebc, f"{p}.events_by_county", (), allow_extra=True):
                        for fips in ebc:
                            if not FIPS.match(str(fips)):
                                v.err(f"{p}.events_by_county", "keys must be FIPS")
                            v.int_(ebc, fips, f"{p}.events_by_county", nullable=False, lo=0)


def _check_solvency(v: _V, d: dict) -> None:
    keys = ("members", "simulations", "ruin_probability", "capital_1_in_200_cents", "stop_loss", "annual_loss_quantiles", "provenance")
    v.obj(d, "", keys, allow_extra=False)
    v.int_(d, "members", "", nullable=False, lo=1)
    v.int_(d, "simulations", "", nullable=False, lo=1)
    v.num(d, "ruin_probability", "", lo=0, hi=1)
    v.int_(d, "capital_1_in_200_cents", "", lo=0)
    sl = d.get("stop_loss")
    if sl is None:
        v.present(d, "stop_loss", "")
    elif v.obj(sl, "stop_loss", STOP_LOSS_KEYS, allow_extra=False):
        for k in STOP_LOSS_KEYS:
            v.int_(sl, k, "stop_loss", lo=0)
    q = d.get("annual_loss_quantiles")
    if "annual_loss_quantiles" in d:
        if not isinstance(q, list) or [x.get("q") if isinstance(x, dict) else None for x in q] != list(LOSS_QUANTILES):
            v.err("annual_loss_quantiles", f"must be a list of {{q, loss_cents}} for q = {list(LOSS_QUANTILES)}")
        else:
            for i, x in enumerate(q):
                p = f"annual_loss_quantiles[{i}]"
                if v.obj(x, p, ("q", "loss_cents"), allow_extra=False):
                    v.int_(x, "loss_cents", p, lo=0)


def _check_basis_risk(v: _V, d: dict) -> None:
    keys = ("schemes", "districts_used", "min_active_monitors", "theta_hi", "theta_lo", "fit_period", "score_period", "provenance")
    v.obj(d, "", keys, allow_extra=False)
    sc = d.get("schemes")
    if v.obj(sc, "schemes", BASIS_RISK_SCHEMES, allow_extra=False):
        for s in BASIS_RISK_SCHEMES:
            row = sc.get(s)
            p = f"schemes.{s}"
            if v.obj(row, p, ("premises_events", "paid", "missed", "wrong", "missed_rate", "wrong_rate"), allow_extra=False):
                for k in ("premises_events", "paid", "missed", "wrong"):
                    v.int_(row, k, p, lo=0)
                v.num(row, "missed_rate", p, lo=0, hi=1)
                v.num(row, "wrong_rate", p, lo=0, hi=1)
    du = d.get("districts_used")
    if not isinstance(du, list):
        v.err("districts_used", "must be a list")
    else:
        for i, x in enumerate(du):
            p = f"districts_used[{i}]"
            if v.obj(x, p, ("state", "district", "median_active_monitors"), allow_extra=False):
                v.str_(x, "state", p)
                v.str_(x, "district", p)
                v.num(x, "median_active_monitors", p, nullable=False, lo=0)
    v.int_(d, "min_active_monitors", "", nullable=False, lo=1)
    v.num(d, "theta_hi", "", lo=0, hi=1)
    v.num(d, "theta_lo", "", lo=0, hi=1)
    for k in ("fit_period", "score_period"):
        per = d.get(k)
        if v.obj(per, k, ("start", "end"), allow_extra=False):
            v.str_(per, "start", k, pattern=DATE)
            v.str_(per, "end", k, pattern=DATE)


def _check_data_quality(v: _V, d: dict) -> None:
    keys = ("coverage_floor", "rows", "year_summary", "gap_bridge_evidence", "storm_windows", "timestamp_check", "county_coverage", "provenance")
    v.obj(d, "", keys, allow_extra=False)
    v.num(d, "coverage_floor", "", nullable=False, lo=0, hi=1)
    rows = d.get("rows")
    if not isinstance(rows, list) or not rows:
        v.err("rows", "must be a non-empty list")
    else:
        for i, r in enumerate(rows):
            p = f"rows[{i}]"
            if v.obj(r, p, DQ_ROW_KEYS, allow_extra=False):
                v.str_(r, "fips", p, pattern=FIPS)
                v.str_(r, "county", p)
                v.str_(r, "state", p)
                for k in ("year", "county_customers", "expected_intervals", "intervals_present", "zero_rows", "intervals_over_county_customers"):
                    v.int_(r, k, p, nullable=False, lo=0)
                v.num(r, "coverage_share", p, nullable=False, lo=0, hi=1)
                v.num(r, "max_customers_out", p, lo=0)
                for k in ("in_model_universe", "excluded_from_fitting"):
                    if not isinstance(r.get(k), bool):
                        v.err(f"{p}.{k}", "must be a boolean")
                if not isinstance(r.get("exclusion_reason"), str):
                    v.err(f"{p}.exclusion_reason", "must be a string")
    ys = d.get("year_summary")
    if not isinstance(ys, list):
        v.err("year_summary", "must be a list")
    else:
        for i, y in enumerate(ys):
            p = f"year_summary[{i}]"
            if v.obj(y, p, ("year", "rows", "zero_rows", "null_rows", "off_grid_rows", "first_t", "last_t"), allow_extra=False):
                for k in ("year", "rows", "zero_rows", "null_rows", "off_grid_rows"):
                    v.int_(y, k, p, nullable=False, lo=0)
                v.str_(y, "first_t", p, pattern=ISO_Z)
                v.str_(y, "last_t", p, pattern=ISO_Z)
    g = d.get("gap_bridge_evidence")
    if v.obj(g, "gap_bridge_evidence", ("events_examined", "gaps", "share_of_gaps_within_bridge"), allow_extra=False):
        v.int_(g, "events_examined", "gap_bridge_evidence", nullable=False, lo=0)
        v.num(g, "share_of_gaps_within_bridge", "gap_bridge_evidence", lo=0, hi=1)
        if not isinstance(g.get("gaps"), list):
            v.err("gap_bridge_evidence.gaps", "must be a list")
        else:
            for i, x in enumerate(g["gaps"]):
                p = f"gap_bridge_evidence.gaps[{i}]"
                if v.obj(x, p, ("minutes", "count"), allow_extra=False):
                    v.int_(x, "minutes", p, nullable=False, lo=1)
                    v.int_(x, "count", p, nullable=False, lo=0)
    sw = d.get("storm_windows")
    if not isinstance(sw, list):
        v.err("storm_windows", "must be a list")
    else:
        for i, w in enumerate(sw):
            p = f"storm_windows[{i}]"
            if v.obj(w, p, ("id", "fips", "start", "end", "expected_intervals", "intervals_present", "zero_rows", "longest_missing_minutes", "notes"), allow_extra=False):
                v.str_(w, "id", p, pattern=SCENARIO_ID)
                v.str_(w, "fips", p, pattern=FIPS)
                v.str_(w, "start", p, pattern=ISO_Z)
                v.str_(w, "end", p, pattern=ISO_Z)
                for k in ("expected_intervals", "intervals_present", "zero_rows", "longest_missing_minutes"):
                    v.int_(w, k, p, nullable=False, lo=0)
                v.str_(w, "notes", p)
    tc = d.get("timestamp_check")
    if v.obj(tc, "timestamp_check", ("assumed", "storm", "first_sharp_rise_utc", "consistent_with_utc", "evidence"), allow_extra=False):
        v.str_(tc, "assumed", "timestamp_check")
        v.str_(tc, "storm", "timestamp_check")
        v.str_(tc, "first_sharp_rise_utc", "timestamp_check", nullable=True, pattern=ISO_Z)
        if not (tc.get("consistent_with_utc") is None or isinstance(tc.get("consistent_with_utc"), bool)):
            v.err("timestamp_check.consistent_with_utc", "must be a boolean or null")
        v.present(tc, "consistent_with_utc", "timestamp_check")
        v.str_(tc, "evidence", "timestamp_check")
    cc = d.get("county_coverage")
    if v.obj(cc, "county_coverage", (), allow_extra=True):
        for fips, row in cc.items():
            p = f"county_coverage.{fips}"
            if not FIPS.match(str(fips)):
                v.err(p, "key must be a 5-digit FIPS string")
            if v.obj(row, p, ("data_coverage",), allow_extra=False):
                v.num(row, "data_coverage", p, lo=0, hi=1)


def _check_scenario(v: _V, d: dict) -> None:
    keys = ("id", "title", "description", "fips", "county_customers", "start", "end", "series", "source", "provenance")
    v.obj(d, "", keys, allow_extra=False)
    v.str_(d, "id", "", pattern=SCENARIO_ID)
    v.str_(d, "title", "")
    v.str_(d, "description", "")
    v.str_(d, "fips", "", pattern=FIPS)
    v.int_(d, "county_customers", "", nullable=False, lo=1)
    v.str_(d, "start", "", pattern=ISO_Z)
    v.str_(d, "end", "", pattern=ISO_Z)
    src = d.get("source")
    if v.obj(src, "source", ("dataset", "url", "file", "sha256"), allow_extra=False):
        v.str_(src, "dataset", "source")
        v.str_(src, "url", "source")
        v.str_(src, "file", "source")
        v.str_(src, "sha256", "source", pattern=re.compile(r"^[0-9a-f]{64}$"))
    times: list[datetime] = []
    series = d.get("series")
    if "series" in d:
        if not isinstance(series, list) or not series:
            v.err("series", "must be a non-empty list")
            series = []
        for i, pt in enumerate(series):
            path = f"series[{i}]"
            if v.obj(pt, path, ("t", "customers_out", "share"), allow_extra=False):
                v.str_(pt, "t", path, pattern=ISO_Z)
                # CONTRACTS 1.3: a missing 15-minute row is a point with both values null.
                if (pt.get("customers_out") is None) != (pt.get("share") is None):
                    v.err(path, "customers_out and share must both be null or both be set")
                elif pt.get("customers_out") is not None:
                    v.int_(pt, "customers_out", path, nullable=False, lo=0)
                    v.num(pt, "share", path, nullable=False, lo=0, hi=1)
                if isinstance(pt.get("t"), str) and ISO_Z.match(pt["t"]):
                    times.append(_parse_z(pt["t"]))
    if len(times) > 1:
        steps = {(b - a).total_seconds() for a, b in zip(times, times[1:])}
        if steps != {900.0}:
            v.err("series", "points must be in order at 15-minute steps")
    for k in ("start", "end"):
        if not (isinstance(d.get(k), str) and ISO_Z.match(d[k])):
            return
    s, e = _parse_z(d["start"]), _parse_z(d["end"])
    if not s < e:
        v.err("", "start must be before end")
    if times and (times[0] < s or times[-1] > e):
        v.err("series", "points must lie within [start, end]")


def _parse_z(s: str) -> datetime:
    return datetime.fromisoformat(s.replace("Z", "+00:00"))


CHECKS = {
    "trigger_params": _check_trigger_params,
    "pricing_table": _check_pricing_table,
    "backtest": _check_backtest,
    "solvency": _check_solvency,
    "basis_risk": _check_basis_risk,
    "data_quality": _check_data_quality,
    "scenario": _check_scenario,
}


def validate_artifact(kind: str, doc: Any) -> None:
    """Raise ArtifactSchemaError listing every problem, or return None if ``doc`` is valid.

    ``kind`` is one of ARTIFACT_NAMES or ``"scenario"``.
    """
    if kind not in CHECKS:
        raise ValueError(f"unknown artifact kind {kind!r}")
    v = _V()
    if not isinstance(doc, dict):
        raise ArtifactSchemaError(["artifact must be a JSON object"])
    _check_provenance(v, doc)
    CHECKS[kind](v, doc)
    # CONTRACTS.md section 8: a null field needs its reason in provenance.notes.
    notes = doc.get("provenance", {}).get("notes") if isinstance(doc.get("provenance"), dict) else None
    if v.has_null and not (isinstance(notes, str) and notes.strip()):
        v.err("provenance.notes", "a field is null, so notes must give the reason")
    try:
        text = json.dumps(doc, allow_nan=False)
    except (TypeError, ValueError) as e:
        v.err("", f"not serialisable as strict JSON (NaN/inf must be null): {e}")
    else:
        if "placeholder" in text.lower():
            v.err("", "artifact mentions a placeholder; placeholders never go into artifacts/")
    if v.errors:
        raise ArtifactSchemaError(v.errors)


def git_commit() -> str:
    out = subprocess.run(["git", "rev-parse", "--short", "HEAD"], cwd=REPO_DIR, capture_output=True, text=True)
    if out.returncode != 0 or not out.stdout.strip():
        raise RuntimeError(f"cannot read git commit for provenance: {out.stderr.strip()}")
    dirty = subprocess.run(["git", "status", "--porcelain", "--", "ml"], cwd=REPO_DIR, capture_output=True, text=True)
    return out.stdout.strip() + ("-dirty" if dirty.stdout.strip() else "")


def make_provenance(code: str, data_sources: list[dict], notes: str) -> dict:
    return {
        "generated_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "git_commit": git_commit(),
        "code": code,
        "data_sources": data_sources,
        "train_years": list(TRAIN_YEARS),
        "test_years": list(TEST_YEARS),
        "seed": SEED,
        "notes": notes,
    }


def write_artifact(kind: str, doc: dict, out_dir: Path = ARTIFACTS_DIR, name: str | None = None) -> Path:
    """Validate ``doc`` and write it atomically. Nothing is written if validation fails."""
    validate_artifact(kind, doc)
    if kind == "scenario":
        path = out_dir / "scenarios" / f"{doc['id']}.json"
    else:
        path = out_dir / f"{name or kind}.json"
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(".json.part")
    tmp.write_text(json.dumps(doc, indent=2, allow_nan=False) + "\n")
    tmp.replace(path)
    return path
