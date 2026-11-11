import importlib
import inspect
import pkgutil

import pandas as pd
import pytest

import fourhour_ml
from fourhour_ml.config import TEST_YEARS, TRAIN_YEARS
from fourhour_ml.split import LeakageError, assert_train_only, split_by_year, train_only


def _frame(years):
    return pd.DataFrame({"t": pd.to_datetime([f"{y}-06-01 12:00" for y in years]), "x": range(len(years))})


def test_split_years_are_the_contract_years():
    assert TRAIN_YEARS == (2014, 2022)
    assert TEST_YEARS == (2023, 2024)


def test_split_by_year_partitions_and_drops_out_of_range():
    df = _frame([2013, 2014, 2018, 2022, 2023, 2024, 2025])
    train, test = split_by_year(df, "t")
    assert sorted(train["t"].dt.year) == [2014, 2018, 2022]
    assert sorted(test["t"].dt.year) == [2023, 2024]
    assert set(train.index).isdisjoint(test.index)


def test_split_boundary_is_new_year_midnight():
    df = pd.DataFrame({"t": pd.to_datetime(["2022-12-31 23:45", "2023-01-01 00:00"])})
    train, test = split_by_year(df, "t")
    assert list(train["t"].dt.year) == [2022] and list(test["t"].dt.year) == [2023]


@train_only
def _fake_fit(df, other=None):
    return len(df)


def test_train_rows_reach_fitting():
    train, _ = split_by_year(_frame(range(2014, 2025)), "t")
    assert _fake_fit(train) == 9


@pytest.mark.parametrize("year", [2023, 2024, 2025, 2013])
def test_a_single_non_train_row_is_refused_by_a_fitting_function(year):
    df = _frame([2015, 2016, year])
    with pytest.raises(LeakageError):
        _fake_fit(df)
    with pytest.raises(LeakageError):
        _fake_fit(_frame([2015]), other=df)  # keyword arguments are checked too


def test_test_split_never_reaches_fitting():
    _, test = split_by_year(_frame(range(2014, 2025)), "t")
    with pytest.raises(LeakageError):
        _fake_fit(test)


def test_event_spanning_into_test_year_is_refused():
    ev = pd.DataFrame({"start": pd.to_datetime(["2022-12-31 20:00"]), "end": pd.to_datetime(["2023-01-01 06:00"])})
    with pytest.raises(LeakageError):
        assert_train_only(ev)


def test_year_column_is_used():
    assert_train_only(pd.DataFrame({"year": [2014, 2022], "n": [1, 2]}))
    with pytest.raises(LeakageError):
        assert_train_only(pd.DataFrame({"year": [2022, 2023]}))


def test_frame_without_time_information_is_refused():
    with pytest.raises(LeakageError):
        _fake_fit(pd.DataFrame({"n": [1, 2, 3]}))


FIT_PREFIXES = ("fit_", "tune_", "choose_", "select_threshold", "grid_search")


def test_every_fitting_function_in_the_package_is_guarded():
    """Any function named fit_*/tune_*/choose_*/... in fourhour_ml must carry @train_only.
    A new fitting function without the guard fails this test."""
    unguarded = []
    for mod_info in pkgutil.walk_packages(fourhour_ml.__path__, "fourhour_ml."):
        mod = importlib.import_module(mod_info.name)
        for name, fn in inspect.getmembers(mod, inspect.isfunction):
            if fn.__module__ == mod.__name__ and name.startswith(FIT_PREFIXES):
                if not getattr(fn, "__train_only__", False):
                    unguarded.append(f"{mod.__name__}.{name}")
    assert not unguarded, f"fitting functions without @train_only: {unguarded}"
