"""Spike S3: EAGLE-I download, columns, demo-county row counts, Harris County 2024 events.

Run from the repo root:   ml/.venv/bin/python spikes/data/s3_eaglei.py
Downloads what is missing (the 2024 national file is 1.44 GB), so it runs from a clean
checkout once ml/.venv exists (see ml/README.md).

The event threshold used here is a PLACEHOLDER chosen only to exercise the code. It is
not a result. 2024 is a test year: nothing printed here may feed any fitting, tuning or
threshold choice. This script writes nothing to artifacts/.
"""

from __future__ import annotations

import sys
from pathlib import Path

ML_DIR = Path(__file__).resolve().parents[2] / "ml"
sys.path.insert(0, str(ML_DIR))

import pandas as pd  # noqa: E402

from fourhour_ml.config import DEMO_COUNTIES, GAP_BRIDGE_MINUTES  # noqa: E402
from fourhour_ml.eaglei import AUX_FILES, YEAR_FILES, load_mcc, read_header, read_parquet  # noqa: E402
from fourhour_ml.events import interval_coverage, missing_runs, reconstruct_events  # noqa: E402
from fourhour_ml.fetch import EAGLEI_RAW, fetch_and_filter_year, fetch_aux, parquet_path  # noqa: E402

YEAR = 2024
HARRIS = "48201"
PLACEHOLDER_THRESHOLD = 0.01  # PLACEHOLDER, NOT A RESULT: 1% of county customers out
SENSITIVITY_PLACEHOLDERS = (0.005, 0.01, 0.02, 0.05)  # also placeholders


def banner(text: str) -> None:
    print("\n" + "=" * 100 + "\n" + text + "\n" + "=" * 100)


def main() -> int:
    banner(
        "SPIKE S3 (EAGLE-I). DATA YEAR 2024 IS A TEST YEAR.\n"
        "Nothing below may feed fitting, tuning or threshold selection. Nothing is written to artifacts/."
    )
    aux = fetch_aux()
    manifest = fetch_and_filter_year(YEAR)

    banner("Files (figshare article 24237376, v4)")
    for y, r in YEAR_FILES.items():
        print(f"  {r.name:28s} {r.size:>14,d} bytes  md5 {r.md5}  {r.url}")
    for r in AUX_FILES.values():
        print(f"  {r.name:28s} {r.size:>14,d} bytes  md5 {r.md5}  {r.url}")

    banner("Columns as published")
    print(f"  {manifest['source_name']}: {manifest['source_header']}")
    for k, p in aux.items():
        print(f"  {p.name}: {read_header(p)}")
    print(f"  source sha256 {manifest['source_sha256']}; TX/FL/IL rows {manifest['rows']:,}")

    mcc = load_mcc(aux["MCC"]).set_index("fips")["customers"]
    df = read_parquet(parquet_path(YEAR), fips=list(DEMO_COUNTIES))
    y0, y1 = pd.Timestamp(f"{YEAR}-01-01"), pd.Timestamp(f"{YEAR + 1}-01-01")

    banner(f"Demo counties, {YEAR}")
    print(f"  {'fips':6s} {'county':24s} {'rows':>7s} {'expected':>8s} {'coverage':>8s} {'longest gap':>11s} "
          f"{'zero rows':>9s} {'MCC customers':>13s} {'file total_customers':>20s} {'max out':>10s}")
    for fips, name in DEMO_COUNTIES.items():
        d = df[df["fips_code"] == fips]
        cov = interval_coverage(d, y0, y1)
        tc = d["total_customers"].dropna().unique()
        print(f"  {fips:6s} {name:24s} {len(d):>7,d} {cov['expected_intervals']:>8,d} {cov['coverage_share']:>8.4f} "
              f"{cov['longest_missing_run_intervals'] * 15 / 60:>9.2f} h {int((d['customers_out'] == 0).sum()):>9,d} "
              f"{int(mcc[fips]):>13,d} {', '.join(f'{int(x):,d}' for x in tc):>20s} {d['customers_out'].max():>10,.0f}")
    print("  Note: the 2024 file publishes explicit zero rows, so a missing row here is a gap, not a zero.")
    print("  Note: run_start_time timezone is not stated on the figshare page; times below are as published.")

    h = df[df["fips_code"] == HARRIS].sort_values("run_start_time")
    n_cust = int(mcc[HARRIS])
    over = (h["customers_out"] > h["total_customers"]).sum()
    print(f"\n  Harris: denominator used = MCC.csv ({n_cust:,d}). Rows where customers_out exceeds the file's own "
          f"total_customers ({int(h['total_customers'].iloc[0]):,d}): {over}")

    banner(
        f"Harris County (48201) {YEAR}: events of 4 h or more\n"
        f"THRESHOLD = {PLACEHOLDER_THRESHOLD} share of customers out. THIS IS A PLACEHOLDER, NOT A RESULT.\n"
        f"Gap bridging {GAP_BRIDGE_MINUTES} min (SPEC.md 5.1). Test-year data: not for fitting or threshold choice."
    )
    ev = reconstruct_events(h, n_cust, PLACEHOLDER_THRESHOLD, min_hours=4.0, series_start=y0, series_end=y1)
    gaps = missing_runs(h, y0, y1)
    with pd.option_context("display.width", 250, "display.max_columns", 20):
        out = ev.assign(
            peak_customers_out=ev["peak_customers_out"].map("{:,.0f}".format),
            peak_share=ev["peak_share"].round(4),
            mean_share=ev["mean_share"].round(4),
            duration_hours=ev["duration_hours"].round(2),
        )
        print(out.to_string() if len(out) else "  (no events)")
    print(f"\n  {len(ev)} events of 4 h or more at the PLACEHOLDER threshold {PLACEHOLDER_THRESHOLD}.")

    banner("Missing-data runs of 1 h or more near each event (within 24 h before start to end)")
    for _, e in ev.iterrows():
        near = gaps[(gaps["gap_end"] >= e["start"] - pd.Timedelta(hours=24)) & (gaps["gap_start"] <= e["end"])]
        near = near[near["intervals"] >= 4]
        print(f"  event {e['start']} -> {e['end']}: " + (
            "; ".join(f"{r.gap_start} -> {r.gap_end} ({r.intervals} intervals)" for r in near.itertuples()) or "none"))

    banner("Sensitivity: number of 4 h+ events at several PLACEHOLDER thresholds (none of these is a result)")
    for th in SENSITIVITY_PLACEHOLDERS:
        n = len(reconstruct_events(h, n_cust, th, min_hours=4.0, series_start=y0, series_end=y1))
        n24 = len(reconstruct_events(h, n_cust, th, min_hours=24.0, series_start=y0, series_end=y1))
        print(f"  placeholder threshold {th:<6}: {n:3d} events >= 4 h, {n24:3d} events >= 24 h")

    print("\nS3 PASS: the 2024 file and MCC.csv downloaded with matching MD5, columns printed, event list printed.")
    print("Reminder: the threshold above is a PLACEHOLDER and 2024 is a TEST YEAR.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
