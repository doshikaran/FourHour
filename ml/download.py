"""Download EAGLE-I yearly files, verify them against the published MD5, and filter each to
Texas, Florida and Illinois as Parquet in ml/data/derived/eaglei/.

Resumable: re-run the same command after an interruption. Finished years are skipped
(their manifest records the source MD5), and a partial download resumes from its
``.part`` file. Usage (from ml/):

    .venv/bin/python download.py --years 2014-2023
    .venv/bin/python download.py --years 2024 --delete-raw

Raw national CSVs go to ml/data/raw/eaglei/ and are never committed.
"""

from __future__ import annotations

import argparse
import sys
import traceback

from fourhour_ml.eaglei import YEAR_FILES
from fourhour_ml.fetch import fetch_and_filter_year, fetch_aux, log


def parse_years(s: str) -> list[int]:
    years: list[int] = []
    for part in s.split(","):
        if "-" in part:
            a, b = part.split("-")
            years.extend(range(int(a), int(b) + 1))
        else:
            years.append(int(part))
    bad = [y for y in years if y not in YEAR_FILES]
    if bad:
        raise SystemExit(f"no EAGLE-I file registered for {bad}; known: {sorted(YEAR_FILES)}")
    return years


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--years", default="2014-2024", help="e.g. 2014-2023 or 2018,2024 (default 2014-2024)")
    ap.add_argument("--delete-raw", action="store_true", help="delete each national CSV after filtering")
    args = ap.parse_args(argv)
    years = parse_years(args.years)
    log(f"fetching companion files and years {years}")
    fetch_aux()
    failed = []
    for y in years:
        try:
            fetch_and_filter_year(y, delete_raw=args.delete_raw)
        except Exception:  # keep going; report every failure at the end
            traceback.print_exc()
            failed.append(y)
    if failed:
        log(f"FAILED years: {failed}. Re-run the same command to resume.")
        return 1
    log("all requested years done")
    return 0


if __name__ == "__main__":
    sys.exit(main())
