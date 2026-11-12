"""M3 done-when: a 2023-2024 row reaching any fitting function fails. Also the ESMI half split."""

from decimal import Decimal

import numpy as np
import pandas as pd
import pytest

from fourhour_ml import basis, build, models, trigger
from fourhour_ml.split import LeakageError


def _cm(years):
    st = {"48": "TX", "12": "FL", "17": "IL"}
    rows = [(f, st[f[:2]], y, m, int((y + m + int(f[-1])) % 4 == 0)) for f in ("48201", "48113", "12086", "12011", "17031") for y in years for m in range(1, 13)]
    return pd.DataFrame(rows, columns=["fips", "state", "year", "month", "events"])


def _events(years):
    return pd.DataFrame({"start": pd.to_datetime([f"{y}-06-01" for y in years]), "longest_run_hours": [10.0 + i for i in range(len(years))]})


def test_fitting_functions_accept_training_years():
    models.fit_frequency(_cm(range(2015, 2023)))
    models.fit_severity(_events(range(2015, 2023)))
    models.fit_credibility(_cm(range(2015, 2023)))


@pytest.mark.parametrize("bad_year", [2023, 2024])
def test_frequency_and_credibility_refuse_test_rows(bad_year):
    cm = _cm([2020, 2021, bad_year])
    with pytest.raises(LeakageError):
        models.fit_frequency(cm)
    with pytest.raises(LeakageError):
        models.fit_credibility(cm)


def test_severity_refuses_test_events():
    with pytest.raises(LeakageError):
        models.fit_severity(_events([2019, 2020, 2023]))


def test_trigger_grid_refuses_test_rows():
    t = pd.date_range("2022-12-31 20:00", "2023-01-01 04:00", freq="15min")
    df = pd.DataFrame({"fips": "48201", "t": t, "customers_out": 500000.0})
    with pytest.raises(LeakageError):
        trigger.grid_search_components(df, 1)


def test_pipeline_training_readings_are_training_years_only():
    years = set(build.train_readings()["t"].dt.year.unique())
    assert years and years <= set(range(2014, 2023))


def _dist(post_split_outage: bool):
    """One synthetic district: 8 monitors, 2 days of minutes; split at minute 1440."""
    n, T = 8, 2880
    rep = np.ones((n, T), dtype=bool)
    index = np.zeros(T)
    index[100:500] = 0.5  # first-half index event (400 min)
    prem = [(np.array([100]), np.array([500])) if k < 4 else (np.array([], dtype=np.int64), np.array([], dtype=np.int64)) for k in range(n)]
    if post_split_outage:
        index[1600:2200] = 0.9
        prem = [(np.r_[p[0], 1600], np.r_[p[1], 2200]) for p in prem]
    return {"state": "S", "district": "D", "index": index, "defined": np.ones(T, bool), "reporting": rep, "premises": prem, "n": n}


def test_esmi_thresholds_use_first_half_only():
    g1, lo1, hi1 = basis.choose_thresholds([_dist(False)], 0, 1440)
    g2, lo2, hi2 = basis.choose_thresholds([_dist(True)], 0, 1440)
    assert (lo1, hi1) == (lo2, hi2)
    pd.testing.assert_frame_equal(g1, g2)


def test_premium_formula_rounds_half_up_once():
    c = {"expected_events_per_year": 0.25, "prob_24h_given_event": 0.5, "prob_premises_affected": 0.4}
    # 0.25 * (0.6 + 0.4*0.5) * 0.4 * 15000 = 1200 per year -> 100/month * 1.5 + 100 = 250
    assert build.expected_annual_payout_cents(c, 15000) == Decimal("1200.000")
    assert build.premium_cents(c, 15000, 0.5, 100) == 250


def test_simulated_payouts_are_integer_cents_and_round_down():
    rng = np.random.default_rng(0)
    ev = pd.DataFrame({"mean_share": [1.0], "qualifies_t24": [True]})
    total, claims = models.simulate_year_payouts(rng, ev, np.array([5001]))
    # t4: floor(5001*0.6) = 3000; top-up: 5001 - 3000 = 2001
    assert total == 5001 and claims == 2
