import pandas as pd
import pytest

from fourhour_ml.events import interval_coverage, missing_runs, reconstruct_events

T0 = pd.Timestamp("2022-03-01 00:00")
STEP = pd.Timedelta(minutes=15)
N = 1000  # county customers


def series(values, start=T0):
    """values: list of customers_out per 15-minute step; None means the row is missing."""
    rows = [(start + i * STEP, v) for i, v in enumerate(values) if v is not None]
    return pd.DataFrame(rows, columns=["run_start_time", "customers_out"])


def test_single_run_duration_and_peak():
    s = series([0] * 4 + [200] * 16 + [0] * 4)  # 16 intervals = 4 h at 20%
    ev = reconstruct_events(s, N, 0.1)
    assert len(ev) == 1
    e = ev.iloc[0]
    assert e["start"] == T0 + 4 * STEP and e["end"] == T0 + 20 * STEP
    assert e["duration_hours"] == 4.0
    assert e["peak_customers_out"] == 200 and e["peak_share"] == 0.2
    assert e["n_above"] == 16 and e["n_bridged_below"] == 0 and e["n_bridged_missing"] == 0


def test_threshold_is_inclusive():
    assert len(reconstruct_events(series([100] * 4), N, 0.1)) == 1
    assert len(reconstruct_events(series([99] * 4), N, 0.1)) == 0


@pytest.mark.parametrize("gap_kind", ["below", "missing"])
def test_gap_of_30_minutes_is_bridged(gap_kind):
    fill = 0 if gap_kind == "below" else None
    s = series([200] * 8 + [fill] * 2 + [200] * 8)
    ev = reconstruct_events(s, N, 0.1)
    assert len(ev) == 1
    assert ev.iloc[0]["duration_hours"] == 4.5  # bridged time counts inside the event
    key = "n_bridged_below" if gap_kind == "below" else "n_bridged_missing"
    assert ev.iloc[0][key] == 2


@pytest.mark.parametrize("gap_kind", ["below", "missing"])
def test_gap_of_45_minutes_splits(gap_kind):
    fill = 0 if gap_kind == "below" else None
    s = series([200] * 8 + [fill] * 3 + [200] * 8)
    ev = reconstruct_events(s, N, 0.1)
    assert len(ev) == 2
    assert list(ev["duration_hours"]) == [2.0, 2.0]


def test_custom_bridge():
    s = series([200] * 8 + [0] * 3 + [200] * 8)
    assert len(reconstruct_events(s, N, 0.1, gap_bridge_minutes=45)) == 1
    assert len(reconstruct_events(s, N, 0.1, gap_bridge_minutes=0)) == 2


def test_min_hours_filter():
    s = series([200] * 15 + [0] * 10 + [200] * 16)  # 3.75 h then 4 h
    ev = reconstruct_events(s, N, 0.1, min_hours=4.0)
    assert list(ev["duration_hours"]) == [4.0]


def test_null_values_are_not_above():
    s = series([200] * 8 + [None] * 3 + [200] * 8).astype({"customers_out": float})
    null_row = pd.DataFrame({"run_start_time": [T0 + 9 * STEP], "customers_out": [float("nan")]})
    s = pd.concat([s, null_row], ignore_index=True).sort_values("run_start_time")
    assert len(reconstruct_events(s, N, 0.1)) == 2


def test_series_edge_flags():
    s = series([200] * 8 + [0] * 8 + [200] * 8)
    ev = reconstruct_events(s, N, 0.1, series_start=T0, series_end=T0 + 24 * STEP)
    assert list(ev["touches_series_start"]) == [True, False]
    assert list(ev["touches_series_end"]) == [False, True]


def test_unsorted_input_and_empty_result():
    s = series([200] * 8).sample(frac=1, random_state=0)
    assert len(reconstruct_events(s, N, 0.1)) == 1
    assert reconstruct_events(series([0] * 8), N, 0.1).empty


def test_rejects_duplicates_and_off_grid():
    s = series([200] * 4)
    with pytest.raises(ValueError):
        reconstruct_events(pd.concat([s, s.iloc[:1]]), N, 0.1)
    s2 = s.copy()
    s2.loc[0, "run_start_time"] += pd.Timedelta(minutes=5)
    with pytest.raises(ValueError):
        reconstruct_events(s2, N, 0.1)


def test_rejects_bad_denominator_and_threshold():
    with pytest.raises(ValueError):
        reconstruct_events(series([1]), 0, 0.1)
    with pytest.raises(ValueError):
        reconstruct_events(series([1]), N, 0)


def test_coverage_and_missing_runs():
    s = series([1, None, None, 1, None, 1, 1, 1])
    cov = interval_coverage(s, T0, T0 + 8 * STEP)
    assert cov == {"expected_intervals": 8, "intervals_present": 5, "coverage_share": 5 / 8, "longest_missing_run_intervals": 2}
    runs = missing_runs(s, T0, T0 + 8 * STEP)
    assert list(runs["intervals"]) == [2, 1]
    assert runs.iloc[0]["gap_start"] == T0 + STEP and runs.iloc[0]["gap_end"] == T0 + 3 * STEP
