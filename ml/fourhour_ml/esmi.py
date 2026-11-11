"""Reading Prayas ESMI files (Harvard Dataverse doi:10.7910/DVN/CLLZZM, version 2.0).

Column names below come from the Dataverse DDI metadata of the ingested .tab files
(fetched 2026-11-11). The dataset's files sit behind a Dataverse guestbook, so they are
downloaded by a person in a browser and placed in ml/data/raw/esmi/ (see ml/README.md).

Voltage files are wide: one row per (location, date, hour) with ``Min 0`` .. ``Min 59``
holding the voltage for each minute. The two 2019 files are CSV and were not ingested by
Dataverse, so their columns are not known until someone opens them.
"""

from __future__ import annotations

import csv
from pathlib import Path

import pandas as pd

LOCATION_COLUMNS = ["Location name", "From date", "To date", "District", "State", "Category", "Connection Type"]
VOLTAGE_ID_COLUMNS = ["Location name", "Date", "Hour"]
MINUTE_COLUMNS = [f"Min {i}" for i in range(60)]
VOLTAGE_COLUMNS = VOLTAGE_ID_COLUMNS + MINUTE_COLUMNS


def _sep(path: Path) -> str:
    if path.suffix.lower() in (".tab", ".tsv"):
        return "\t"
    with open(path, newline="", encoding="utf-8-sig", errors="replace") as f:
        return csv.Sniffer().sniff(f.read(65536), delimiters=",\t;").delimiter


def read_header(path: Path) -> list[str]:
    with open(path, newline="", encoding="utf-8-sig", errors="replace") as f:
        return next(csv.reader(f, delimiter=_sep(path)))


def read_locations(path: Path) -> pd.DataFrame:
    df = pd.read_csv(path, sep=_sep(path), dtype=str, encoding="utf-8-sig")
    df.columns = [c.strip() for c in df.columns]
    missing = [c for c in LOCATION_COLUMNS if c not in df.columns]
    if missing:
        raise ValueError(f"{path.name}: missing location columns {missing}; got {list(df.columns)}")
    return df


def read_location_days(path: Path) -> pd.DataFrame:
    """Distinct (location, date) pairs in a voltage file, reading only the id columns."""
    header = [c.strip() for c in read_header(path)]
    missing = [c for c in ("Location name", "Date") if c not in header]
    if missing:
        raise ValueError(f"{path.name}: missing columns {missing}; header starts {header[:6]}")
    df = pd.read_csv(path, sep=_sep(path), usecols=lambda c: c.strip() in ("Location name", "Date"), dtype=str)
    df.columns = [c.strip() for c in df.columns]
    df = df.drop_duplicates().rename(columns={"Date": "Date_raw"})
    # The date format is not documented in the metadata. ISO first; anything that does not
    # parse stays NaT and the caller must report it. No day/month guessing.
    df["Date"] = pd.to_datetime(df["Date_raw"], errors="coerce", format="ISO8601")
    return df


def premises_per_district(locations: pd.DataFrame) -> pd.DataFrame:
    """Number of distinct monitored locations per (State, District) in the location file."""
    return (
        locations.groupby(["State", "District"])["Location name"].nunique()
        .rename("premises").reset_index().sort_values("premises", ascending=False, ignore_index=True)
    )


def daily_active_per_district(location_days: pd.DataFrame, locations: pd.DataFrame) -> pd.DataFrame:
    """Per (State, District, Date): number of locations with any data that day."""
    m = location_days.merge(locations[["Location name", "State", "District"]], on="Location name", how="left")
    return m.groupby(["State", "District", "Date"])["Location name"].nunique().rename("active").reset_index()


def district_eligibility(daily: pd.DataFrame, split_date: pd.Timestamp, min_median_active: int) -> pd.DataFrame:
    """Median daily active monitors per district in each half; passes if both halves reach
    ``min_median_active``. Days with no active monitors count as zero."""
    rows = []
    lo, hi = daily["Date"].min(), daily["Date"].max()
    for (state, district), g in daily.groupby(["State", "District"]):
        s = g.set_index("Date")["active"].reindex(pd.date_range(lo, hi, freq="D"), fill_value=0)
        first, second = s[s.index < split_date], s[s.index >= split_date]
        rows.append({
            "State": state, "District": district,
            "median_active_first_half": float(first.median()) if len(first) else 0.0,
            "median_active_second_half": float(second.median()) if len(second) else 0.0,
        })
    out = pd.DataFrame(rows)
    out["passes"] = (out["median_active_first_half"] >= min_median_active) & (out["median_active_second_half"] >= min_median_active)
    return out.sort_values(["passes", "median_active_first_half"], ascending=False, ignore_index=True)
