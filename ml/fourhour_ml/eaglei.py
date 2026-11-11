"""Reading EAGLE-I county outage files.

Source: ORNL EAGLE-I, "Recorded Electricity Outages 2014-2025", figshare article 24237376
(doi:10.6084/m9.figshare.24237376, version 4). Column names differ by year; see
ml/README.md. This module maps them onto one schema:

    fips_code (str, 5 digits), county (str), state (str),
    customers_out (float, nullable), run_start_time (datetime64, as published),
    total_customers (float, nullable; only the 2024 file publishes it)
"""

from __future__ import annotations

import hashlib
from dataclasses import dataclass
from pathlib import Path

import pandas as pd
import pyarrow as pa
import pyarrow.compute as pc
import pyarrow.csv as pacsv
import pyarrow.parquet as pq

from .config import KEEP_STATES

FIGSHARE_ARTICLE_ID = 24237376
FIGSHARE_ARTICLE_URL = (
    "https://figshare.com/articles/dataset/"
    "The_Environment_for_Analysis_of_Geo-Located_Energy_Information_s_Recorded_"
    "Electricity_Outages_2014-2022/24237376"
)
FIGSHARE_API_URL = f"https://api.figshare.com/v2/articles/{FIGSHARE_ARTICLE_ID}"
DOWNLOAD_URL = "https://ndownloader.figshare.com/files/{file_id}"


@dataclass(frozen=True)
class RemoteFile:
    name: str
    file_id: int
    size: int
    md5: str

    @property
    def url(self) -> str:
        return DOWNLOAD_URL.format(file_id=self.file_id)


# From the figshare API on 2026-11-11 (article version 4, modified 2026-02-25).
YEAR_FILES: dict[int, RemoteFile] = {
    2014: RemoteFile("eaglei_outages_2014.csv", 42547717, 78056250, "c7bfcf4b7fd26292f8d8801a26e8c52f"),
    2015: RemoteFile("eaglei_outages_2015.csv", 42547822, 598963737, "774d837e6a0d4f20aab5c82448538d37"),
    2016: RemoteFile("eaglei_outages_2016.csv", 42547825, 619779314, "8e4e85e81db474a708278aabef1198ec"),
    2017: RemoteFile("eaglei_outages_2017.csv", 42547828, 698796929, "4faaeeb3b3e8e2d13d0e9238617ce3ce"),
    2018: RemoteFile("eaglei_outages_2018.csv", 42547879, 999210542, "726543e4a392e220108b035ba4c52e43"),
    2019: RemoteFile("eaglei_outages_2019.csv", 42547885, 1102778792, "0ff77af0106486eb7bdc6c5051bd916a"),
    2020: RemoteFile("eaglei_outages_2020.csv", 42547894, 1170699724, "3b1e76e7ff0445da2b9a2fe4819c0e88"),
    2021: RemoteFile("eaglei_outages_2021.csv", 42547891, 1141058232, "b5a5912be793ca22dddcc73ca2c3379d"),
    2022: RemoteFile("eaglei_outages_2022.csv", 42547897, 1187596176, "0cd04f23ac90ec4ec20ce3a63dcfa25a"),
    2023: RemoteFile("eaglei_outages_2023.csv", 44574907, 1199752423, "4ef3f9290884b4151a43333d28f67369"),
    2024: RemoteFile("eaglei_outages_2024.csv", 53581661, 1444846424, "13183facf56c196c061a238303c87973"),
    2025: RemoteFile("eaglei_outages_2025.csv", 62164877, 1402347695, "cd2feb1282a42fb048cb6885398bc1cc"),
}

AUX_FILES: dict[str, RemoteFile] = {
    "MCC": RemoteFile("MCC.csv", 42547708, 40584, "7e30d47d44cae46b3342cdabf7c84b7f"),
    "DQI": RemoteFile("DQI.csv", 42547705, 4338, "fca42544e0c3ee4a191985d1c6c19bc4"),
    "coverage_history": RemoteFile("coverage_history.csv", 42547714, 11965, "33ef0c8549a95bcc1ef52b61c2463271"),
}

# Header variants seen in the published files. Anything else is an error, not a guess.
_OUT_COLUMN_ALIASES = ("customers_out", "sum")  # 2023 uses "sum"
_REQUIRED = ("fips_code", "county", "state", "run_start_time")
_KNOWN = set(_REQUIRED) | set(_OUT_COLUMN_ALIASES) | {"total_customers"}

STANDARD_COLUMNS = ["fips_code", "county", "state", "customers_out", "run_start_time", "total_customers"]


class SchemaError(ValueError):
    pass


def file_md5(path: Path, chunk: int = 1 << 22) -> str:
    h = hashlib.md5()
    with open(path, "rb") as f:
        while b := f.read(chunk):
            h.update(b)
    return h.hexdigest()


def file_sha256(path: Path, chunk: int = 1 << 22) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        while b := f.read(chunk):
            h.update(b)
    return h.hexdigest()


def read_header(path: Path) -> list[str]:
    with open(path, encoding="utf-8-sig") as f:
        return f.readline().strip().split(",")


def _normalise_table(tbl: pa.Table) -> pa.Table:
    cols = tbl.column_names
    unknown = set(cols) - _KNOWN
    missing = [c for c in _REQUIRED if c not in cols]
    out_cols = [c for c in _OUT_COLUMN_ALIASES if c in cols]
    if unknown or missing or len(out_cols) != 1:
        raise SchemaError(f"unexpected EAGLE-I columns {cols}")
    n = tbl.num_rows
    arrays = {
        "fips_code": tbl["fips_code"],
        "county": tbl["county"],
        "state": tbl["state"],
        "customers_out": tbl[out_cols[0]].cast(pa.float64()),
        "run_start_time": tbl["run_start_time"],
        "total_customers": (
            tbl["total_customers"].cast(pa.float64())
            if "total_customers" in cols
            else pa.nulls(n, pa.float64())
        ),
    }
    return pa.table(arrays)


def iter_filtered(path: Path, states: tuple[str, ...] = KEEP_STATES, block_size: int = 64 << 20):
    """Stream a national yearly CSV and yield normalised record batches for ``states``."""
    header = read_header(path)
    types = {
        "fips_code": pa.string(),
        "county": pa.string(),
        "state": pa.string(),
        "run_start_time": pa.timestamp("s"),
    }
    for c in header:
        if c in _OUT_COLUMN_ALIASES or c == "total_customers":
            types[c] = pa.float64()
    reader = pacsv.open_csv(
        path,
        read_options=pacsv.ReadOptions(block_size=block_size, encoding="utf8"),
        convert_options=pacsv.ConvertOptions(column_types=types, strings_can_be_null=False),
    )
    value_set = pa.array(list(states))
    for batch in reader:
        tbl = pa.Table.from_batches([batch])
        tbl = tbl.filter(pc.is_in(tbl["state"], value_set=value_set))
        if tbl.num_rows:
            yield _normalise_table(tbl)


def filter_year_to_parquet(csv_path: Path, out_path: Path, states: tuple[str, ...] = KEEP_STATES) -> dict:
    """Filter one national yearly CSV to ``states`` and write Parquet. Returns stats."""
    out_path.parent.mkdir(parents=True, exist_ok=True)
    tmp = out_path.with_suffix(".parquet.part")
    rows = 0
    writer = None
    try:
        for tbl in iter_filtered(csv_path, states):
            if writer is None:
                writer = pq.ParquetWriter(tmp, tbl.schema, compression="zstd")
            writer.write_table(tbl)
            rows += tbl.num_rows
    finally:
        if writer is not None:
            writer.close()
    if writer is None:
        raise SchemaError(f"no rows for {states} in {csv_path}")
    tmp.replace(out_path)
    return {"rows": rows, "bytes": out_path.stat().st_size, "header": read_header(csv_path)}


def read_parquet(path: Path, fips: list[str] | None = None) -> pd.DataFrame:
    filters = [("fips_code", "in", fips)] if fips else None
    df = pq.read_table(path, filters=filters).to_pandas()
    df["run_start_time"] = pd.to_datetime(df["run_start_time"])
    return df


def load_mcc(path: Path) -> pd.DataFrame:
    """Modeled county customers (2022). Returns fips (5-digit str) and customers (int)."""
    mcc = pd.read_csv(path, encoding="utf-8-sig", dtype={"County_FIPS": str})
    if list(mcc.columns) != ["County_FIPS", "Customers"]:
        raise SchemaError(f"unexpected MCC columns {list(mcc.columns)}")
    mcc["fips"] = mcc["County_FIPS"].str.zfill(5)
    return mcc[["fips", "Customers"]].rename(columns={"Customers": "customers"})
