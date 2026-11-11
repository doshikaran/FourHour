"""Artifact schema check against CONTRACTS.md section 8 (version 1.2).

``write_artifact`` validates before writing; a file that fails is never written.

Where section 8 names a key, it is enforced exactly (name, type, nullability). Where
section 8 only describes contents in prose without naming keys (for example the items of
``trigger_params.grid`` or the per county-year rows of ``data_quality.json``), this check
enforces only what is named and ``UNSPECIFIED`` records the gap. Those gaps are open
contract questions for the lead; they are not filled in with guessed key names here.
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

CONTRACT_VERSION = "1.2"

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

UNSPECIFIED = {
    "trigger_params.grid[]": "item key names (the pair and its expected wrong/missed payouts)",
    "backtest.frequency": "inner keys (per county and year counts, deviance, calibration bins)",
    "backtest.severity": "inner keys (GPD parameters, fit statistic, QQ points)",
    "backtest.credibility": "inner shape (weight per county)",
    "backtest.pool": "inner keys (per test year premiums, payouts, loss ratio, events) and units",
    "solvency.annual_loss_quantiles": "shape",
    "basis_risk.<scheme>": "where the four schemes sit (checked here at top level, provisionally) and their count keys",
    "basis_risk.fit_period/score_period": "type",
    "data_quality": "top-level key holding the per county-year rows, and their key names",
    "scenario.source": "type (only presence is checked)",
    "scenario.series[]": "how a missing 15-minute row appears (checked here as no gaps, integer customers_out, provisionally)",
}


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
    v.obj(d, "", ("theta_hi", "theta_lo", "gap_bridge_minutes", "grid", "provenance"), allow_extra=False)
    v.num(d, "theta_hi", "", lo=0, hi=1)
    v.num(d, "theta_lo", "", lo=0, hi=1)
    v.int_(d, "gap_bridge_minutes", "", nullable=False, lo=0)
    if _is_num(d.get("theta_hi")) and _is_num(d.get("theta_lo")) and not d["theta_lo"] < d["theta_hi"]:
        v.err("theta_lo", "must be below theta_hi (SPEC.md section 4)")
    g = d.get("grid")
    if "grid" in d:
        if not isinstance(g, list) or not g:
            v.err("grid", "must be a non-empty list")
        elif not all(isinstance(x, dict) for x in g):
            v.err("grid", "items must be objects")


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


def _check_backtest(v: _V, d: dict) -> None:
    v.obj(d, "", ("frequency", "severity", "credibility", "pool", "provenance"), allow_extra=False)
    for k in ("frequency", "severity", "credibility", "pool"):
        v.present(d, k, "")
        if d.get(k) is not None and not isinstance(d[k], (dict, list)):
            v.err(k, "must be an object, a list or null")


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
    v.present(d, "annual_loss_quantiles", "")
    if q is not None and not isinstance(q, (dict, list)):
        v.err("annual_loss_quantiles", "must be an object, a list or null")


def _check_basis_risk(v: _V, d: dict) -> None:
    v.obj(d, "", BASIS_RISK_SCHEMES + ("districts_used", "fit_period", "score_period", "provenance"), allow_extra=False)
    for s in BASIS_RISK_SCHEMES:
        row = d.get(s)
        if row is None:
            v.present(d, s, "")
            continue
        if v.obj(row, s, ("missed_rate", "wrong_rate"), allow_extra=True):
            v.num(row, "missed_rate", s, lo=0, hi=1)
            v.num(row, "wrong_rate", s, lo=0, hi=1)
    if "districts_used" in d and not isinstance(d["districts_used"], list):
        v.err("districts_used", "must be a list")
    for k in ("fit_period", "score_period"):
        v.present(d, k, "")


def _check_data_quality(v: _V, d: dict) -> None:
    if not isinstance(d, dict) or len(d) < 2:
        v.err("", "must hold per county-year rows besides provenance")


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
    v.present(d, "source", "")  # type is open in CONTRACTS 1.2
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
                v.int_(pt, "customers_out", path, nullable=False, lo=0)
                v.num(pt, "share", path, nullable=False, lo=0)
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
