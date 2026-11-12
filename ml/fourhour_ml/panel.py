"""EAGLE-I readings for TX, FL and IL, county customers, coverage, model universe, exclusions.

Rules (ml/README.md, decisions D1 and D3):

* Readings come from the per-year Parquet slices built by ``download.py``. Rows with a
  null ``customers_out`` are not readings. Timestamps are treated as UTC (D4).
* Off-grid timestamps (a few seconds or minutes past a 15-minute slot, scrape jitter) are
  moved to the start of their slot. If the slot already has an on-grid reading, the
  off-grid row is dropped. Both counts are reported.
* County customers are MCC.csv (D3), never the 2024 ``total_customers`` column.
* Coverage of a county-year = slots with a reading / slots in the UTC calendar year.
  Zero-outage rows are not systematically published in any year, so a missing slot may be
  a scrape gap or a true zero; this coverage is a lower bound on feed coverage, and it
  measures feed coverage only where some customers are out in nearly every slot.
* Model universe: counties whose median coverage over UNIVERSE_YEARS (training years
  only) is at least UNIVERSE_MIN_MEDIAN_COVERAGE. In those counties a missing slot is
  mostly a feed gap. A universe county-year is excluded from fitting when its coverage is
  below COVERAGE_FLOOR. The 2014 file starts 2014-11-01, so every 2014 county-year falls
  below the floor.
"""

from __future__ import annotations

import functools
import json

import numpy as np
import pandas as pd

from .config import (
    COVERAGE_FLOOR,
    DERIVED_DIR,
    RAW_DIR,
    STATE_ABBR,
    TEST_YEARS,
    TRAIN_YEARS,
    UNIVERSE_MIN_MEDIAN_COVERAGE,
    UNIVERSE_YEARS,
)
from .eaglei import AUX_FILES, YEAR_FILES, file_sha256, load_mcc

ALL_YEARS = tuple(range(TRAIN_YEARS[0], TEST_YEARS[1] + 1))
STATE_FIPS = {"48": "TX", "12": "FL", "17": "IL"}
EAGLEI_DOI = "https://doi.org/10.6084/m9.figshare.24237376.v4"


def parquet_path(year: int):
    return DERIVED_DIR / "eaglei" / f"eaglei_{year}_TX_FL_IL.parquet"


def manifest(year: int) -> dict:
    p = parquet_path(year).with_suffix(".manifest.json")
    m = json.loads(p.read_text())
    if m["source_md5"] != YEAR_FILES[year].md5:
        raise RuntimeError(f"{year}: manifest MD5 does not match the published MD5")
    return m


def slots_in_year(year: int) -> int:
    return int((pd.Timestamp(f"{year + 1}-01-01") - pd.Timestamp(f"{year}-01-01")) / pd.Timedelta(minutes=15))


@functools.lru_cache(maxsize=1)
def county_customers() -> pd.Series:
    """MCC customers for TX, FL and IL counties, indexed by 5-digit FIPS."""
    mcc = load_mcc(RAW_DIR / "eaglei" / "MCC.csv").set_index("fips")["customers"]
    return mcc[mcc.index.str[:2].isin(STATE_FIPS)].astype(int)


def mcc_sha256() -> str:
    return file_sha256(RAW_DIR / "eaglei" / AUX_FILES["MCC"].name)


def load_year(year: int) -> tuple[pd.DataFrame, dict]:
    """Readings for one year: fips, county, state, t, customers_out (no nulls, on grid,
    unique per fips and t). Also returns the row accounting for ``year_summary``."""
    raw = pd.read_parquet(parquet_path(year), columns=["fips_code", "county", "state", "customers_out", "run_start_time"])
    raw = raw.rename(columns={"fips_code": "fips", "run_start_time": "t"})
    stats = {
        "year": year,
        "rows": int(len(raw)),
        "zero_rows": int((raw["customers_out"] == 0).sum()),
        "null_rows": int(raw["customers_out"].isna().sum()),
        "first_t": raw["t"].min(),
        "last_t": raw["t"].max(),
    }
    df = raw[raw["customers_out"].notna()].copy()
    floor = df["t"].dt.floor("15min")
    off = floor != df["t"]
    stats["off_grid_rows"] = int(off.sum())
    df["t"] = floor
    df["_off"] = off
    # keep the on-grid reading when an off-grid one lands in the same slot
    df = df.sort_values(["fips", "t", "_off"], kind="stable")
    dup = df.duplicated(["fips", "t"], keep="first")
    stats["off_grid_dropped_collisions"] = int(dup.sum())
    df = df[~dup].drop(columns="_off").reset_index(drop=True)
    df["customers_out"] = df["customers_out"].astype(float)
    return df, stats


@functools.lru_cache(maxsize=None)
def _load_year_cached(year: int):
    return load_year(year)


def readings(years: tuple[int, ...]) -> pd.DataFrame:
    return pd.concat([_load_year_cached(y)[0] for y in years], ignore_index=True)


def year_stats(years: tuple[int, ...] = ALL_YEARS) -> list[dict]:
    return [_load_year_cached(y)[1] for y in years]


def coverage_table(years: tuple[int, ...] = ALL_YEARS) -> pd.DataFrame:
    """One row per (TX/FL/IL county in MCC, year)."""
    cust = county_customers()
    rows = []
    for y in years:
        df = _load_year_cached(y)[0]
        cnt = cust.reindex(df["fips"]).to_numpy()
        df = df.assign(_over=df["customers_out"].to_numpy() > cnt)
        g = df.groupby("fips").agg(
            intervals_present=("t", "size"),
            zero_rows=("customers_out", lambda s: int((s == 0).sum())),
            max_customers_out=("customers_out", "max"),
            intervals_over_county_customers=("_over", "sum"),
        )
        names = df.drop_duplicates("fips").set_index("fips")[["county", "state"]]
        g = g.reindex(cust.index)
        g["year"] = y
        g["county"] = names["county"].reindex(cust.index)
        g["state"] = [STATE_FIPS[f[:2]] for f in cust.index]
        rows.append(g)
    out = pd.concat(rows).rename_axis("fips").reset_index()
    out["county_customers"] = out["fips"].map(cust).astype(int)
    out["expected_intervals"] = out["year"].map(slots_in_year)
    out["intervals_present"] = out["intervals_present"].fillna(0).astype(int)
    out["zero_rows"] = out["zero_rows"].fillna(0).astype(int)
    out["intervals_over_county_customers"] = out["intervals_over_county_customers"].fillna(0).astype(int)
    out["coverage_share"] = out["intervals_present"] / out["expected_intervals"]
    # county names: fill from other years where a county has no rows in this one
    name = out.dropna(subset=["county"]).drop_duplicates("fips").set_index("fips")["county"]
    out["county"] = out["fips"].map(name)
    return out


def universe(cov: pd.DataFrame) -> pd.Index:
    lo, hi = UNIVERSE_YEARS
    if not (TRAIN_YEARS[0] <= lo and hi <= TRAIN_YEARS[1]):
        raise RuntimeError("the universe rule may only use training years")
    m = cov[cov["year"].between(lo, hi)].groupby("fips")["coverage_share"].median()
    return m[m >= UNIVERSE_MIN_MEDIAN_COVERAGE].index


def mark_exclusions(cov: pd.DataFrame) -> pd.DataFrame:
    u = set(universe(cov))
    cov = cov.copy()
    cov["in_model_universe"] = cov["fips"].isin(u)
    train = cov["year"].between(*TRAIN_YEARS)
    low = cov["coverage_share"] < COVERAGE_FLOOR
    cov["excluded_from_fitting"] = cov["in_model_universe"] & train & low
    reason = np.where(
        ~cov["in_model_universe"],
        f"not in model universe: median coverage over {UNIVERSE_YEARS[0]}-{UNIVERSE_YEARS[1]} below {UNIVERSE_MIN_MEDIAN_COVERAGE}",
        np.where(~train, "test year: never used for fitting", np.where(low, "coverage_share below coverage_floor", "")),
    )
    cov["exclusion_reason"] = reason
    return cov


def data_source_entries(years: tuple[int, ...], rows_used: dict[int, int] | None = None) -> list[dict]:
    """Provenance data_sources: one entry per EAGLE-I year file plus MCC.csv."""
    out = []
    for y in years:
        m = manifest(y)
        out.append(
            {
                "name": f"EAGLE-I recorded outages {y} ({m['source_name']}, filtered to TX/FL/IL)",
                "url": m["source_url"],
                "sha256": m["source_sha256"],
                "rows_used": int(rows_used.get(y, 0) if rows_used else m["rows"]),
            }
        )
    out.append(
        {
            "name": "EAGLE-I modeled county customers (MCC.csv)",
            "url": AUX_FILES["MCC"].url,
            "sha256": mcc_sha256(),
            "rows_used": int(len(county_customers())),
        }
    )
    return out


def county_series(df: pd.DataFrame, fips: str) -> tuple[np.ndarray, np.ndarray]:
    s = df[df["fips"] == fips].sort_values("t")
    return s["t"].to_numpy("datetime64[ns]").astype(np.int64), s["customers_out"].to_numpy(float)


def state_of(fips: str) -> str:
    return STATE_FIPS[fips[:2]]


__all__ = [
    "ALL_YEARS",
    "STATE_ABBR",
    "county_customers",
    "coverage_table",
    "mark_exclusions",
    "readings",
    "year_stats",
    "universe",
    "data_source_entries",
    "county_series",
]
