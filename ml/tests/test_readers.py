"""EAGLE-I header normalisation and ESMI helpers, on tiny files written in tmp_path."""

import pandas as pd
import pytest

from fourhour_ml.eaglei import SchemaError, filter_year_to_parquet, load_mcc, read_parquet
from fourhour_ml.esmi import ESMIError, check_date_format, concurrency, eligibility, norm


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


def test_esmi_day_first_confirmed():
    st = check_date_format(pd.Series(["31-10-2014", "01-11-2014"]), "%d-%m-%Y", "f")
    assert st["first_above_12"] == 1 and st["second_above_12"] == 0


def test_esmi_month_first_confirmed_and_value_errors_counted():
    st = check_date_format(pd.Series(["7/1/2019", "7/31/2019", "#VALUE!"]), "%m/%d/%Y", "f")
    assert st["second_above_12"] == 1 and st["unparseable_cells"] == 1


def test_esmi_wrong_format_fails_loudly():
    with pytest.raises(ESMIError):
        check_date_format(pd.Series(["7/1/2019", "7/31/2019"]), "%d-%m-%Y", "f")
    with pytest.raises(ESMIError):
        check_date_format(pd.Series(["31-10-2014"]), "%m-%d-%Y", "f")
    with pytest.raises(ESMIError):  # no row can tell day from month: not confirmed
        check_date_format(pd.Series(["01-02-2015"]), "%d-%m-%Y", "f")


def test_esmi_names_normalised():
    assert norm(pd.Series(["Chandauli ", " Chandauli", "Bengaluru  Urban"])).tolist() == ["Chandauli", "Chandauli", "Bengaluru Urban"]


def test_esmi_eligibility_counts_concurrent_hours():
    loc = pd.DataFrame({"Location name": ["a", "b", "c"], "District": ["D1", "D1", "D2"], "State": ["S", "S", "S"]})
    t = pd.to_datetime(["2015-01-01 00:00", "2015-01-01 01:00", "2015-01-03 00:00"])
    act = pd.DataFrame({"loc": ["a", "a", "a", "b", "b", "b", "c"], "t": list(t) + list(t) + [t[2]]})
    conc = concurrency(act, loc)
    el = eligibility(conc, pd.Timestamp("2015-01-02"), cutoff=2, min_hours=1).set_index("District")
    assert el.loc["D1", "hours_ok_first_half"] == 2 and el.loc["D1", "hours_ok_second_half"] == 1 and el.loc["D1", "passes"]
    assert not el.loc["D2", "passes"]
