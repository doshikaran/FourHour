"""EAGLE-I header normalisation and ESMI helpers, on tiny files written in tmp_path."""

import pandas as pd
import pytest

from fourhour_ml.eaglei import SchemaError, filter_year_to_parquet, load_mcc, read_parquet
from fourhour_ml.esmi import district_eligibility, premises_per_district, read_location_days


@pytest.mark.parametrize(
    "header,row_extra",
    [
        ("fips_code,county,state,customers_out,run_start_time", ""),  # 2014-2022, 2025
        ("fips_code,county,state,sum,run_start_time", ""),  # 2023
        ("fips_code,county,state,customers_out,run_start_time,total_customers", ",500"),  # 2024
    ],
)
def test_eaglei_headers_normalise(tmp_path, header, row_extra):
    out_pos = header.split(",").index("sum" if "sum" in header else "customers_out")
    def row(fips, county, state, out, t):
        vals = [fips, county, state, None, t]
        vals.insert(out_pos, out)
        vals = [v for v in vals if v is not None]
        return ",".join(vals) + row_extra
    lines = [header,
             row("48201", "Harris", "Texas", "7", "2024-01-01 00:15:00"),
             row("01001", "Autauga", "Alabama", "3", "2024-01-01 00:15:00"),
             row("17031", "Cook", "Illinois", "", "2024-01-01 00:30:00")]
    csv = tmp_path / "y.csv"
    csv.write_text("\n".join(lines) + "\n")
    stats = filter_year_to_parquet(csv, tmp_path / "y.parquet")
    df = read_parquet(tmp_path / "y.parquet")
    assert stats["rows"] == 2
    assert list(df.columns) == ["fips_code", "county", "state", "customers_out", "run_start_time", "total_customers"]
    assert df["fips_code"].tolist() == ["48201", "17031"]  # leading zeros kept as strings; Alabama dropped
    assert df["customers_out"].iloc[0] == 7 and pd.isna(df["customers_out"].iloc[1])
    assert (df["total_customers"].iloc[0] == 500) == bool(row_extra)


def test_eaglei_unknown_header_is_an_error(tmp_path):
    csv = tmp_path / "y.csv"
    csv.write_text("fips,county,state,out,time\n48201,Harris,Texas,1,2024-01-01 00:00:00\n")
    with pytest.raises(SchemaError):
        filter_year_to_parquet(csv, tmp_path / "y.parquet")


def test_mcc_bom_and_zero_padding(tmp_path):
    p = tmp_path / "MCC.csv"
    p.write_bytes("﻿County_FIPS,Customers\n1001,24619\n48201,1827686\n".encode("utf-8"))
    m = load_mcc(p)
    assert m["fips"].tolist() == ["01001", "48201"]


def test_esmi_district_helpers(tmp_path):
    loc = pd.DataFrame({"Location name": ["a", "b", "c"], "District": ["D1", "D1", "D2"], "State": ["S", "S", "S"]})
    assert premises_per_district(loc).to_dict("records") == [
        {"State": "S", "District": "D1", "premises": 2}, {"State": "S", "District": "D2", "premises": 1}]
    dates = ["2015-01-01", "2015-01-02", "2015-01-03", "2015-01-04"]
    days = pd.DataFrame({"Location name": ["a"] * 4 + ["b"] * 4 + ["c", "c"],
                         "Date": pd.to_datetime(dates + dates + ["2015-01-03", "2015-01-04"])})
    from fourhour_ml.esmi import daily_active_per_district
    daily = daily_active_per_district(days, loc)
    elig = district_eligibility(daily, pd.Timestamp("2015-01-03"), min_median_active=1).set_index("District")
    assert elig.loc["D1", "median_active_first_half"] == 2 and elig.loc["D1", "passes"]
    # D2 has no monitor in the first half: days without data count as zero, so it fails.
    assert elig.loc["D2", "median_active_first_half"] == 0 and not elig.loc["D2", "passes"]


def test_esmi_voltage_dates_are_not_guessed(tmp_path):
    p = tmp_path / "ESMI minute-wise voltage data x.tab"
    p.write_text("Location name\tDate\tHour\tMin 0\na\t2015-01-02\t0\t230\na\t02/01/2015\t1\t0\n")
    d = read_location_days(p)
    assert d["Date"].notna().sum() == 1 and d["Date"].isna().sum() == 1
