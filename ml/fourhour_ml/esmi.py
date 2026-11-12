"""Reading Prayas ESMI files (Harvard Dataverse doi:10.7910/DVN/CLLZZM, version 2.0).

Facts established from the files and from "About the initiative.pdf" (ml/README.md):

* Voltage files are wide: one row per (location, date, hour), columns ``Min 0`` .. ``Min 59``.
* No-supply rule (source: "About the initiative.pdf", "A few pointers"): "A '0' voltage
  reading means a power outage" and "Missing data should be interpreted as unavailable data
  and not as interruption". So a minute reading 0 is no supply; a minute with no reading
  (no row for that hour) is a monitor that is offline or not reporting. The 2014-2018 files
  have no blank minute cells, so missing minutes are exactly the missing hour-rows.
* Date formats, confirmed per file from rows where one of the two numbers is above 12
  (``check_date_format``): 2014-2018 files are DD-MM-YYYY; the two 2019 files are M/D/YYYY.
* The two 2019 files are excluded from the study: the July-Dec file's Hour column only takes
  the values 0 and 1, so minutes cannot be placed in time; the Jan-June file has thousands of
  rows that repeat a (location, date, hour) with conflicting values (one reading supply, the
  other 0); both have ``#VALUE!`` dates; and the PDF states the period as November 2014 to
  December 2018.
* Names are stripped of surrounding spaces ("Chandauli " and "Chandauli" are one district).
  Hyderabad appears under "Andhra Pradesh/Telangana" and "Telangana"; it is one district,
  labelled Telangana (Hyderabad district has been in Telangana since 2014).
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from pathlib import Path

import numpy as np
import pandas as pd
import pyarrow as pa
import pyarrow.compute as pc
import pyarrow.csv as pacsv

from .config import RAW_DIR

ESMI_DIR = RAW_DIR / "esmi"
DATASET_URL = "https://doi.org/10.7910/DVN/CLLZZM"
LOCATION_FILE = "ESMI location information.csv"
LOCATION_COLUMNS = ["Location name", "From date", "To date", "District", "State", "Category", "Connection Type"]
MINUTE_COLUMNS = [f"Min {i}" for i in range(60)]
VOLTAGE_COLUMNS = ["Location name", "Date", "Hour"] + MINUTE_COLUMNS


@dataclass(frozen=True)
class VoltageFile:
    name: str
    date_format: str  # strptime format, confirmed by check_date_format
    use: bool
    reason: str = ""


DMY = "%d-%m-%Y"
MDY = "%m/%d/%Y"
_2019_REASON = (
    "excluded: Hour column corrupt or conflicting duplicate rows, #VALUE! dates, and outside the "
    "period the dataset documentation states (Nov 2014 - Dec 2018)"
)
VOLTAGE_FILES = [
    VoltageFile("ESMI minute-wise voltage data 2014.csv", DMY, True),
    VoltageFile("ESMI minute-wise voltage data 2015.csv", DMY, True),
    VoltageFile("ESMI minute-wise voltage data 2016 Jan-June.csv", DMY, True),
    VoltageFile("ESMI minute-wise voltage data 2016 July-Dec.csv", DMY, True),
    VoltageFile("ESMI minute-wise voltage data 2017 Jan-April.csv", DMY, True),
    VoltageFile("ESMI minute-wise voltage data 2017 May-Aug.csv", DMY, True),
    VoltageFile("ESMI minute-wise voltage data 2017 Sept-Dec.csv", DMY, True),
    VoltageFile("ESMI minute-wise voltage data 2018 Jan-May.csv", DMY, True),
    VoltageFile("ESMI minute-wise voltage data 2018 June-Aug.csv", DMY, True),
    VoltageFile("ESMI minute-wise voltage data 2018 Sep-Dec.csv", DMY, True),
    VoltageFile("ESMI voltage data 2019 Jan-June.csv", MDY, False, _2019_REASON),
    VoltageFile("ESMI voltage data 2019 July-Dec.csv", MDY, False, _2019_REASON),
]

_DATE_RE = re.compile(r"^(\d{1,2})([-/])(\d{1,2})\2(\d{4})$")


class ESMIError(RuntimeError):
    pass


def norm(s: pd.Series) -> pd.Series:
    return s.astype(str).str.strip().str.replace(r"\s+", " ", regex=True)


def check_date_format(raw: pd.Series, fmt: str, file: str) -> dict:
    """Confirm ``fmt`` from the data: in a day-first format the first number is above 12 in
    some rows and the second never is; month-first is the reverse. Raises if the data
    contradicts ``fmt`` or the separator differs. ``#VALUE!`` cells are counted, not parsed."""
    m = raw.str.extract(_DATE_RE.pattern)
    ok = m[0].notna()
    first, second = m.loc[ok, 0].astype(int), m.loc[ok, 2].astype(int)
    seps = set(m.loc[ok, 1].unique())
    stats = {
        "rows": int(len(raw)),
        "unparseable_cells": int((~ok).sum()),
        "unparseable_values": sorted(set(raw[~ok].astype(str)))[:5],
        "first_above_12": int((first > 12).sum()),
        "second_above_12": int((second > 12).sum()),
        "separator": "".join(sorted(seps)),
    }
    want_sep = "-" if "-" in fmt else "/"
    day_first = fmt.startswith("%d")
    if seps != {want_sep}:
        raise ESMIError(f"{file}: separators {seps} do not match format {fmt}")
    if day_first and not (stats["first_above_12"] > 0 and stats["second_above_12"] == 0):
        raise ESMIError(f"{file}: data does not confirm day-first {fmt}: {stats}")
    if not day_first and not (stats["second_above_12"] > 0 and stats["first_above_12"] == 0):
        raise ESMIError(f"{file}: data does not confirm month-first {fmt}: {stats}")
    return stats


def read_locations(path: Path | None = None) -> pd.DataFrame:
    path = path or ESMI_DIR / LOCATION_FILE
    df = pd.read_csv(path, dtype=str, encoding="utf-8-sig")
    df.columns = [c.strip() for c in df.columns]
    missing = [c for c in LOCATION_COLUMNS if c not in df.columns]
    if missing:
        raise ESMIError(f"{path.name}: missing location columns {missing}; got {list(df.columns)}")
    for c in LOCATION_COLUMNS:
        df[c] = norm(df[c])
    df.loc[df["District"] == "Hyderabad", "State"] = "Telangana"
    df["From date"] = pd.to_datetime(df["From date"], format=DMY)
    df["To date"] = pd.to_datetime(df["To date"], format=DMY)
    if df["Location name"].duplicated().any():
        raise ESMIError("duplicate location names after normalisation")
    return df


def read_voltage(vf: VoltageFile, keep_locations: set[str] | None = None, with_values: bool = True) -> tuple[pd.DataFrame, dict]:
    """Rows of one voltage file: location, date (datetime64), hour (int), minutes as a
    (n, 60) float array (NaN never occurs in the used files). Returns the frame and stats."""
    path = ESMI_DIR / vf.name
    types = {"Location name": pa.string(), "Date": pa.string(), "Hour": pa.string()} | {c: pa.string() for c in MINUTE_COLUMNS}
    t = pacsv.read_csv(path, convert_options=pacsv.ConvertOptions(column_types=types, include_columns=VOLTAGE_COLUMNS))
    raw_date = t["Date"].to_pandas()
    stats = check_date_format(raw_date, vf.date_format, vf.name)
    loc = norm(t["Location name"].to_pandas())
    sel = np.ones(len(loc), dtype=bool) if keep_locations is None else loc.isin(keep_locations).to_numpy()
    date = pd.to_datetime(raw_date, format=vf.date_format, errors="coerce")
    unparsed = int(date[sel].isna().sum())
    if unparsed and vf.use:
        raise ESMIError(f"{vf.name}: {unparsed} dates left unparsed with format {vf.date_format}")
    hour = pd.to_numeric(t["Hour"].to_pandas(), errors="coerce")
    bad_hour = (~hour.between(0, 23)) | hour.isna()
    stats["bad_hour_rows"] = int((bad_hour & pd.Series(sel)).sum())
    keep = sel & ~bad_hour.to_numpy() & date.notna().to_numpy()
    if with_values and vf.use and keep.any():
        # strict cast: a non-numeric voltage in a used file raises here
        sub = t.filter(pa.array(keep))
        mins = np.column_stack([pc.cast(sub[c], pa.float64()).to_numpy(zero_copy_only=False) for c in MINUTE_COLUMNS])
    else:
        mins = np.empty((int(keep.sum()), 0))
    out = pd.DataFrame({"loc": loc[keep].to_numpy(), "date": date[keep].to_numpy(), "hour": hour[keep].astype(int).to_numpy()})
    vals = mins
    stats["rows_kept"] = int(keep.sum())
    stats["blank_minutes"] = int(np.isnan(vals).sum()) if vals.size else None
    stats["duplicate_keys"] = int(out.duplicated(["loc", "date", "hour"]).sum())
    return out.assign(_row=np.arange(len(out))), {**stats, "_values": vals}


def active_hours() -> tuple[pd.DataFrame, list[dict]]:
    """Every (loc, date, hour) with a row in a used voltage file, plus per-file stats."""
    frames, stats = [], []
    for vf in VOLTAGE_FILES:
        df, st = read_voltage(vf, with_values=False)
        st.pop("_values", None)
        stats.append({"file": vf.name, "format": vf.date_format, "used": vf.use, "reason": vf.reason, **st})
        if vf.use:
            frames.append(df[["loc", "date", "hour"]])
    act = pd.concat(frames, ignore_index=True).drop_duplicates()
    act["t"] = act["date"] + pd.to_timedelta(act["hour"], unit="h")
    return act[["loc", "t"]], stats


def concurrency(act: pd.DataFrame, locations: pd.DataFrame) -> pd.DataFrame:
    """Active monitors per (state, district, hour)."""
    m = act.merge(locations[["Location name", "State", "District"]], left_on="loc", right_on="Location name", how="inner")
    return m.groupby(["State", "District", "t"])["loc"].nunique().rename("active").reset_index()


def eligibility(conc: pd.DataFrame, split: pd.Timestamp, cutoff: int, min_hours: int) -> pd.DataFrame:
    """A district passes at ``cutoff`` if it has at least ``min_hours`` hours with ``cutoff`` or
    more monitors reporting concurrently, in each half (before and after ``split``)."""
    c = conc.assign(ok=conc["active"] >= cutoff, first=conc["t"] < split)
    g = c.groupby(["State", "District"]).apply(
        lambda x: pd.Series({
            "hours_ok_first_half": int((x.ok & x["first"]).sum()),
            "hours_ok_second_half": int((x.ok & ~x["first"]).sum()),
            "median_active_when_reporting": float(x["active"].median()),
        }),
        include_groups=False,
    ).reset_index()
    g["passes"] = (g["hours_ok_first_half"] >= min_hours) & (g["hours_ok_second_half"] >= min_hours)
    return g.sort_values(["passes", "hours_ok_second_half"], ascending=False, ignore_index=True)


# Eligibility (S4). MIN_USABLE_HOURS: 30 days of hours per half. CHOSEN_CUTOFF is set after
# S4 reports the counts at 10, 8 and 5; it depends only on monitor availability, never on
# outage outcomes.
MIN_USABLE_HOURS = 720
CHOSEN_CUTOFF = 8
CUTOFF_REASON = (
    "S4 counts: 6 districts pass at 10, 8 at 8, 18 at 5. At 8 monitors one premises moves the index by at most "
    "12.5%; at 5 it moves it by 20% or more, too coarse to separate a lenient from a strong threshold. 8 keeps the "
    "index usable while adding two districts over a cut-off of 10"
)


def split_time(act: pd.DataFrame) -> pd.Timestamp:
    """Midpoint of the period with data, floored to midnight."""
    lo, hi = act["t"].min(), act["t"].max()
    return (lo + (hi - lo) / 2).floor("D")
