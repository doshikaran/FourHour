"""Spike S4: ESMI (Prayas, Harvard Dataverse doi:10.7910/DVN/CLLZZM) structure, date formats,
monitored premises per district, and district eligibility for a district index.

Run from the repo root:   ml/.venv/bin/python spikes/data/s4_esmi.py

The Dataverse files sit behind a guestbook form, so a person downloads them and puts them,
unrenamed, in ml/data/raw/esmi/. Exit codes: 0 pass, 1 no district passes the chosen
cut-off, 2 files missing, 3 the data contradicts an expected format or no rows survive.

Eligibility: a district passes at cut-off c if, in each half of the study period, it has at
least MIN_HOURS hours in which c or more of its monitors report concurrently (a monitor
reports in an hour when its file has a row for that hour). The study fits thresholds on the
first half and scores on the second (SPEC.md section 6), so both halves need data.
"""

from __future__ import annotations

import sys
from pathlib import Path

ML_DIR = Path(__file__).resolve().parents[2] / "ml"
sys.path.insert(0, str(ML_DIR))

import pandas as pd  # noqa: E402

from fourhour_ml import esmi  # noqa: E402

CUTOFFS = (10, 8, 5)
CHOSEN_CUTOFF = esmi.CHOSEN_CUTOFF
MIN_HOURS = esmi.MIN_USABLE_HOURS


def main() -> int:
    missing = [vf.name for vf in esmi.VOLTAGE_FILES if not (esmi.ESMI_DIR / vf.name).exists()]
    if not (esmi.ESMI_DIR / esmi.LOCATION_FILE).exists() or missing:
        print(f"S4 NOT RUN: ESMI files are missing from {esmi.ESMI_DIR}: {missing or [esmi.LOCATION_FILE]}")
        print(f"Download them in a browser from {esmi.DATASET_URL} (guestbook form) and put them there, unrenamed.")
        return 2
    try:
        locations = esmi.read_locations()
        act, stats = esmi.active_hours()
    except esmi.ESMIError as e:
        print(f"S4 FAIL: {e}")
        return 3

    print("=" * 100)
    print("Voltage files: date format confirmed from rows where one number is above 12")
    print("=" * 100)
    for s in stats:
        print(
            f"  {s['file']:48s} {s['format']:9s} rows {s['rows']:>9,d}  first>12 {s['first_above_12']:>8,d}  "
            f"second>12 {s['second_above_12']:>8,d}  unparseable {s['unparseable_cells']} {s['unparseable_values']}  "
            f"bad hour rows {s['bad_hour_rows']}  duplicate keys {s['duplicate_keys']}  "
            + ("USED" if s["used"] else f"NOT USED: {s['reason']}")
        )
    if act.empty:
        print("S4 FAIL: no rows survived parsing in the used files. Check the date formats above.")
        return 3

    print("\nNo-supply rule (About the initiative.pdf): a 0 V reading is a power outage; missing data is unavailable, not an interruption.")
    print(f"\nLocation file: {len(locations)} locations, {locations['District'].nunique()} districts after normalising names.")
    unknown = sorted(set(act["loc"]) - set(locations["Location name"]))
    print(f"Locations in used voltage files: {act['loc'].nunique()}; not in the location file: {len(unknown)} {unknown[:5]}")
    lo, hi = act["t"].min(), act["t"].max()
    split = esmi.split_time(act)
    print(f"Study period with data: {lo} -> {hi}; halves split at {split}")

    per = locations.groupby(["State", "District"]).size().rename("premises").sort_values(ascending=False)
    print(f"\nDistricts with 10 or more premises in the location file: {(per >= 10).sum()}")
    conc = esmi.concurrency(act, locations)
    peak = conc.groupby(["State", "District"])["active"].max()
    print(f"Districts by most monitors ever reporting in one hour: >=10: {(peak >= 10).sum()}, >=8: {(peak >= 8).sum()}, >=5: {(peak >= 5).sum()}")

    results = {}
    for c in CUTOFFS:
        el = esmi.eligibility(conc, split, c, MIN_HOURS)
        results[c] = el
        p = el[el["passes"]]
        print(f"\nCut-off {c} concurrently reporting monitors, at least {MIN_HOURS} such hours in each half: {len(p)} district(s) pass")
        print(el.head(15).to_string())
    chosen = results[CHOSEN_CUTOFF]
    passing = chosen[chosen["passes"]]
    print(f"\nChosen cut-off: {CHOSEN_CUTOFF} ({esmi.CUTOFF_REASON})")
    if passing.empty:
        print("S4 FAIL: no district passes the chosen cut-off. Tell the lead; do not substitute data.")
        return 1
    print(f"{len(passing)} district(s) pass: " + ", ".join(f"{r.District} ({r.State})" for r in passing.itertuples()))
    print("S4 PASS")
    return 0


if __name__ == "__main__":
    sys.exit(main())
