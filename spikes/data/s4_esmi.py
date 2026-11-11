"""Spike S4: ESMI (Prayas, Harvard Dataverse doi:10.7910/DVN/CLLZZM) structure and premises
per district.

Run from the repo root:   ml/.venv/bin/python spikes/data/s4_esmi.py

The Dataverse files are behind a guestbook (a short form in the browser), so this script
does not download them. A person downloads them from
    https://dataverse.harvard.edu/dataset.xhtml?persistentId=doi:10.7910/DVN/CLLZZM
and puts every file, unrenamed, in ml/data/raw/esmi/. The script then prints each file's
structure, the time span, monitored premises per district, and which districts pass the
proposed cut-off.

Proposed cut-off (a proposal for the lead, not a result): a district can carry a district
index if its median number of monitors reporting per day is at least MIN_MEDIAN_ACTIVE in
BOTH halves of the period (the study fits on the first half and scores on the second,
SPEC.md section 6). With n monitors the index moves in steps of 1/n; at 10 one premises is
10% of the index, which is about the coarsest step that can still separate theta_lo from
theta_hi.
"""

from __future__ import annotations

import sys
from pathlib import Path

ML_DIR = Path(__file__).resolve().parents[2] / "ml"
sys.path.insert(0, str(ML_DIR))

import pandas as pd  # noqa: E402

from fourhour_ml.config import RAW_DIR  # noqa: E402
from fourhour_ml.esmi import (  # noqa: E402
    daily_active_per_district,
    district_eligibility,
    premises_per_district,
    read_header,
    read_location_days,
    read_locations,
)

ESMI_DIR = RAW_DIR / "esmi"
DATASET_URL = "https://dataverse.harvard.edu/dataset.xhtml?persistentId=doi:10.7910/DVN/CLLZZM"
MIN_MEDIAN_ACTIVE = 10  # PROPOSED cut-off, see module docstring


def main() -> int:
    files = sorted(p for p in ESMI_DIR.glob("*") if p.suffix.lower() in (".tab", ".tsv", ".csv"))
    loc_files = [p for p in files if "location" in p.name.lower()]
    volt_files = [p for p in files if "voltage" in p.name.lower()]
    if not loc_files or not volt_files:
        print("S4 NOT RUN: ESMI files are missing.")
        print(f"Download them in a browser from {DATASET_URL}")
        print("(the site asks for a guestbook form before downloading) and put them, unrenamed, in")
        print(f"  {ESMI_DIR}")
        return 2

    print("=" * 100)
    print("Files and structure")
    print("=" * 100)
    for p in files:
        h = read_header(p)
        print(f"  {p.name:50s} {p.stat().st_size:>12,d} bytes  {len(h)} columns: {h[:4]} ... {h[-1]}")

    locations = read_locations(loc_files[0])
    print(f"\nLocation file: {len(locations)} rows, {locations['Location name'].nunique()} distinct locations")
    for c in ("State", "Category", "Connection Type"):
        print(f"  {c}: {locations[c].value_counts(dropna=False).to_dict()}")

    days = []
    for p in volt_files:
        d = read_location_days(p)
        bad = d["Date"].isna().sum()
        print(f"  {p.name}: {len(d):,d} location-days, {d['Location name'].nunique()} locations, "
              f"{d['Date'].min()} -> {d['Date'].max()}, unparsed dates {bad} (sample raw: {d['Date_raw'].head(3).tolist()})")
        days.append(d)
    days_all = pd.concat(days, ignore_index=True)
    unparsed = int(days_all["Date"].isna().sum())
    days_all = days_all.dropna(subset=["Date"]).drop_duplicates(["Location name", "Date"])
    unknown = sorted(set(days_all["Location name"]) - set(locations["Location name"]))

    lo, hi = days_all["Date"].min(), days_all["Date"].max()
    split = lo + (hi - lo) / 2
    print(f"\nTime span with data: {lo.date()} -> {hi.date()}; first/second half split at {split.date()}")
    print(f"Unparsed dates dropped: {unparsed}. Locations in voltage data but not in the location file: {len(unknown)}")

    print("\n" + "=" * 100)
    print("Premises per district (location file)")
    print("=" * 100)
    print(premises_per_district(locations).to_string())

    daily = daily_active_per_district(days_all, locations)
    elig = district_eligibility(daily, split, MIN_MEDIAN_ACTIVE)
    print("\n" + "=" * 100)
    print(f"Median monitors reporting per day, by half. PROPOSED cut-off: >= {MIN_MEDIAN_ACTIVE} in both halves")
    print("=" * 100)
    print(elig.to_string())
    passing = elig[elig["passes"]]
    print(f"\n{len(passing)} district(s) pass the proposed cut-off: "
          + ", ".join(f"{r.District} ({r.State})" for r in passing.itertuples()))
    if passing.empty:
        print("S4 FAIL: no district has enough premises at this cut-off. Tell the lead; do not substitute data.")
        return 1
    print("S4 PASS")
    return 0


if __name__ == "__main__":
    sys.exit(main())
