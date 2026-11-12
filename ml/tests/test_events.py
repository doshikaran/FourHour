"""Event reconstruction: D1 gap bridging, CONTRACTS 1.4 run duration, 1.5 hours clause."""

import numpy as np
import pandas as pd
import pytest

from fourhour_ml.events import interval_coverage, merge_runs, missing_runs, reconstruct_events, reconstruct_runs

T0 = pd.Timestamp("2022-03-01 00:00")
STEP = pd.Timedelta(minutes=15)
H = pd.Timedelta(hours=1)
N = 1000  # county customers


def series(values, start=T0):
    """values: list of customers_out per 15-minute step; None means the row is missing."""
    rows = [(start + i * STEP, v) for i, v in enumerate(values) if v is not None]
    return pd.DataFrame(rows, columns=["run_start_time", "customers_out"])


def test_duration_is_last_minus_first_reading():
    # 17 readings span exactly 4 h (CONTRACTS 1.4: qualifies at the reading 4 h after its first)
    ev = reconstruct_events(series([0] * 4 + [200] * 17 + [0] * 4), N, 0.1)
    assert len(ev) == 1
    e = ev.iloc[0]
    assert e["start"] == T0 + 4 * STEP and e["end"] == T0 + 20 * STEP
    assert e["longest_run_hours"] == 4.0 and e["qualifies_t4"] and not e["qualifies_t24"]
    assert e["first_t4_at"] == T0 + 20 * STEP
    assert e["peak_customers_out"] == 200 and e["peak_share"] == 0.2


def test_sixteen_readings_do_not_qualify():
    ev = reconstruct_events(series([200] * 16), N, 0.1)
    assert ev.iloc[0]["longest_run_hours"] == 3.75 and not ev.iloc[0]["qualifies_t4"]


def test_single_reading_is_zero_hours():
    ev = reconstruct_events(series([0, 200, 0]), N, 0.1)
    assert ev.iloc[0]["longest_run_hours"] == 0.0


def test_threshold_is_inclusive():
    assert len(reconstruct_events(series([100] * 4), N, 0.1)) == 1
    assert len(reconstruct_events(series([99] * 4), N, 0.1)) == 0


def test_observed_below_reading_ends_run():
    runs = reconstruct_runs(series([200] * 8 + [50] + [200] * 8), N, 0.1)  # 50: below, not a dropout
    assert len(runs) == 2


@pytest.mark.parametrize("n_missing,expected_runs", [(1, 1), (8, 1), (9, 2)])
def test_missing_gap_bridged_up_to_120_minutes(n_missing, expected_runs):
    # 8 missing intervals = 120 minutes between readings 135 min apart: bridged. 9 = 135: not.
    runs = reconstruct_runs(series([200] * 8 + [None] * n_missing + [200] * 8), N, 0.1)
    assert len(runs) == expected_runs
    if expected_runs == 1:
        r = runs.iloc[0]
        assert r["n_missing_bridged"] == n_missing
        assert r["duration_hours"] == (15 + n_missing) * 0.25  # bridged gap counts inside


def test_missing_gap_needs_readings_above_on_both_sides():
    # missing then below: the run ends at its last reading at or above
    runs = reconstruct_runs(series([200] * 8 + [None] * 2 + [50] + [200] * 4), N, 0.1)
    assert len(runs) == 2
    assert runs.iloc[0]["end"] == T0 + 7 * STEP


def test_null_value_is_missing_not_zero():
    s = series([200] * 8 + [None] * 2 + [200] * 8).astype({"customers_out": float})
    null_row = pd.DataFrame({"run_start_time": [T0 + 8 * STEP], "customers_out": [float("nan")]})
    s = pd.concat([s, null_row], ignore_index=True).sort_values("run_start_time")
    assert len(reconstruct_runs(s, N, 0.1)) == 1


def test_share_capped_at_one():
    ev = reconstruct_events(series([1500] * 3), N, 0.5)
    assert ev.iloc[0]["peak_share"] == 1.0 and ev.iloc[0]["peak_customers_out"] == 1500


def test_hours_clause_merges_qualifying_runs_within_72h():
    gap = [0] * (4 * 70)  # 70 hours below
    ev = reconstruct_events(series([200] * 20 + gap + [200] * 20), N, 0.1)
    assert len(ev) == 1
    e = ev.iloc[0]
    assert e["n_runs"] == 2 and e["longest_run_hours"] == 4.75  # never summed


def test_hours_clause_splits_beyond_72h():
    gap = [0] * (4 * 73)
    ev = reconstruct_events(series([200] * 20 + gap + [200] * 20), N, 0.1)
    assert len(ev) == 2


def test_non_qualifying_run_never_merges_or_chains():
    # qualifying, 50h, short run, 50h, qualifying: the short run is its own event and does
    # not chain the two qualifying runs (they are 100 h apart).
    gap = [0] * (4 * 50)
    ev = reconstruct_events(series([200] * 20 + gap + [200] * 4 + gap + [200] * 20), N, 0.1)
    assert len(ev) == 3
    assert list(ev["qualifies_t4"]) == [True, False, True]


def test_merge_runs_qualifying_mask():
    hr = 3600 * 10**9
    start = np.array([0, 10, 20, 200]) * hr
    end = start + 5 * hr
    q = np.array([True, False, True, True])
    assert list(merge_runs(start, end, 72, q)) == [0, 1, 0, 2]


def test_t24_tier():
    ev = reconstruct_events(series([200] * 97), N, 0.1)
    assert ev.iloc[0]["longest_run_hours"] == 24.0 and ev.iloc[0]["qualifies_t24"]


def test_min_hours_filter():
    s = series([200] * 16 + [0] * 4 * 80 + [200] * 17)  # 3.75 h, then 4 h
    ev = reconstruct_events(s, N, 0.1, min_hours=4.0)
    assert list(ev["longest_run_hours"]) == [4.0]


def test_series_edge_flags():
    s = series([200] * 20 + [0] * 4 * 80 + [200] * 20)
    end = T0 + (40 + 320) * STEP
    ev = reconstruct_events(s, N, 0.1, series_start=T0, series_end=end)
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


# ---- Dropouts (CONTRACTS 1.6 section 3)


def test_derecho_style_dropouts_are_bridged():
    # 400k out, one reading of 47, back; later three readings of ~22, back: one run.
    N2 = 1_000_000
    vals = [400_000] * 20 + [47] + [400_000] * 20 + [23, 22, 22] + [390_000] * 60
    runs = reconstruct_runs(series(vals), N2, 0.01)
    assert len(runs) == 1
    r = runs.iloc[0]
    assert r["n_missing_bridged"] == 4
    assert r["duration_hours"] == (len(vals) - 1) * 0.25
    ev = reconstruct_events(series(vals), N2, 0.01)
    assert ev.iloc[0]["qualifies_t24"] and ev.iloc[0]["peak_share"] == 0.4


def test_dropout_rule_can_be_switched_off():
    vals = [400] * 20 + [4] + [400] * 20
    assert len(reconstruct_runs(series(vals), N, 0.1, dropout_max_fraction=None)) == 2
    assert len(reconstruct_runs(series(vals), N, 0.1)) == 1


def test_fall_of_less_than_90_percent_ends_run():
    vals = [400] * 20 + [41] + [400] * 20  # 41 > 10% of 400
    assert len(reconstruct_runs(series(vals), N, 0.1)) == 2


def test_dropout_without_recovery_in_window_ends_run():
    # 9 dropout readings: the next reading at or above is 150 min after the last one above
    vals = [400] * 20 + [4] * 9 + [400] * 20
    runs = reconstruct_runs(series(vals), N, 0.1)
    assert len(runs) == 2 and runs.iloc[0]["end"] == T0 + 19 * STEP
    # 8 dropout readings: 135 min, bridged like 120 minutes of missing intervals
    assert len(reconstruct_runs(series([400] * 20 + [4] * 8 + [400] * 20), N, 0.1)) == 1


def test_dropout_then_ordinary_below_ends_run():
    vals = [400] * 20 + [4, 60] + [400] * 20  # 60 is below 0.1 share but not a dropout
    assert len(reconstruct_runs(series(vals), N, 0.1)) == 2


def test_dropout_mixed_with_missing_rows():
    vals = [400] * 20 + [4, None, None, 3] + [400] * 20
    runs = reconstruct_runs(series(vals), N, 0.1)
    assert len(runs) == 1 and runs.iloc[0]["n_missing_bridged"] == 4


def test_dropout_at_series_end_ends_run():
    runs = reconstruct_runs(series([400] * 20 + [4, 4]), N, 0.1)
    assert len(runs) == 1 and runs.iloc[0]["end"] == T0 + 19 * STEP
