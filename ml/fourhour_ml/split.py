"""The train/test split, and the guard that keeps test years out of fitting.

CLAUDE.md rule 3: models are fitted on 2014-2022 and evaluated on 2023-2024. This module
is the only place that split is applied. Every function that fits, tunes or chooses a
threshold is decorated with ``@train_only``; the decorator refuses any pandas argument
that holds a row outside the training years, or whose year cannot be determined.
"""

from __future__ import annotations

import functools
from typing import Any, Callable, TypeVar

import pandas as pd

from .config import TEST_YEARS, TRAIN_YEARS

F = TypeVar("F", bound=Callable[..., Any])


class LeakageError(RuntimeError):
    """Raised when data outside the training years reaches fitting or tuning code."""


def _years_in(obj: pd.DataFrame | pd.Series) -> set[int]:
    """Every calendar year any row of ``obj`` touches.

    Uses a ``year`` column if present, plus every datetime column and a DatetimeIndex.
    An event that starts in 2022 and ends in 2023 therefore touches 2023. If no time
    information is found the guard cannot prove the data is clean, so it raises.
    """
    years: set[int] = set()
    found = False
    frame = obj.to_frame() if isinstance(obj, pd.Series) else obj
    if isinstance(frame.index, pd.DatetimeIndex):
        found = True
        years.update(int(y) for y in frame.index.year.dropna().unique())
    for col in frame.columns:
        s = frame[col]
        if col == "year":
            found = True
            years.update(int(y) for y in pd.unique(s.dropna()))
        elif pd.api.types.is_datetime64_any_dtype(s):
            found = True
            years.update(int(y) for y in s.dt.year.dropna().unique())
    if not found:
        raise LeakageError(
            "cannot determine the year of the rows passed to a fitting function: "
            "add a 'year' column or a datetime column"
        )
    return years


def assert_train_only(obj: pd.DataFrame | pd.Series, what: str = "data") -> None:
    """Raise LeakageError unless every row of ``obj`` lies in the training years."""
    lo, hi = TRAIN_YEARS
    bad = sorted(y for y in _years_in(obj) if not lo <= y <= hi)
    if bad:
        raise LeakageError(
            f"{what} contains rows from years {bad}; fitting and tuning may only see "
            f"{lo}-{hi}"
        )


def train_only(fn: F) -> F:
    """Decorator for fitting, tuning and threshold-selection functions."""

    @functools.wraps(fn)
    def wrapper(*args: Any, **kwargs: Any) -> Any:
        for i, a in enumerate(args):
            if isinstance(a, (pd.DataFrame, pd.Series)):
                assert_train_only(a, f"{fn.__name__} argument {i}")
        for k, a in kwargs.items():
            if isinstance(a, (pd.DataFrame, pd.Series)):
                assert_train_only(a, f"{fn.__name__} argument {k!r}")
        return fn(*args, **kwargs)

    wrapper.__train_only__ = True  # type: ignore[attr-defined]
    return wrapper  # type: ignore[return-value]


def split_by_year(df: pd.DataFrame, time_col: str) -> tuple[pd.DataFrame, pd.DataFrame]:
    """Split rows into (train, test) by the calendar year of ``time_col``.

    Train is TRAIN_YEARS, test is TEST_YEARS, both inclusive. Rows in any other year
    (for example 2025, which EAGLE-I now publishes) are in neither and are dropped.
    """
    years = pd.to_datetime(df[time_col]).dt.year
    train = df[years.between(*TRAIN_YEARS)].copy()
    test = df[years.between(*TEST_YEARS)].copy()
    return train, test
