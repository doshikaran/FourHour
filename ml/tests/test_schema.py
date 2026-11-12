"""The schema check accepts contract-shaped artifacts and rejects malformed ones.

The numbers in these fixtures only exercise the validator. They are written to pytest's
tmp_path, never to artifacts/.
"""

import copy
import json
import math

import pytest

from fourhour_ml.schema import ArtifactSchemaError, validate_artifact, write_artifact


def prov(notes="test fixture"):
    return {
        "generated_at": "2026-11-11T18:00:00Z",
        "git_commit": "abc1234",
        "code": "ml/tests/test_schema.py",
        "data_sources": [{"name": "fixture", "url": "https://example.org", "sha256": "0" * 64, "rows_used": 10}],
        "train_years": [2014, 2022],
        "test_years": [2023, 2024],
        "seed": 20261111,
        "notes": notes,
    }


_V = {"exclude_low_coverage": None, "include_low_coverage": None}
MODELS = {"nb_glm": 0.8, "poisson_glm": 0.8, "historical_mean": 0.6}
VARIANT = {
    "county_years_fitted": 3,
    "per_county_year": [{"fips": "48201", "year": 2023, "realised": 1, **MODELS}],
    "deviance": dict(MODELS),
    "log_score": dict(MODELS),
    "calibration": {m: [{"bin": 1, "n": 4, "predicted_mean": 0.2, "realised_mean": 0.25}] for m in MODELS},
    "beats_baselines": False,
}
SCHEME = {"premises_events": 10, "paid": 8, "missed": 2, "wrong": 1, "missed_rate": 0.2, "wrong_rate": 0.125}

VALID = {
    "scenario": {
        "id": "beryl-harris-2024", "title": "t", "description": "d", "fips": "48201", "county_customers": 100,
        "start": "2024-07-08T00:00:00Z", "end": "2024-07-08T00:30:00Z",
        "series": [
            {"t": "2024-07-08T00:00:00Z", "customers_out": 0, "share": 0.0},
            {"t": "2024-07-08T00:15:00Z", "customers_out": None, "share": None},
            {"t": "2024-07-08T00:30:00Z", "customers_out": 5, "share": 0.05},
        ],
        "source": {"dataset": "EAGLE-I", "url": "https://doi.org/x", "file": "eaglei_outages_2024.csv", "sha256": "0" * 64},
        "provenance": prov(),
    },
    "trigger_params": {
        "theta_hi": 0.2, "theta_lo": 0.05, "gap_bridge_minutes": 120, "event_merge_hours": 72, "dropout_max_fraction": 0.1,
        "theta_hi_status": "grid_edge_no_interior_optimum",
        "sensitivity": [{"witness_share": 0.5, "false_confirmation_rate": 0.1, "theta_lo": 0.05, "theta_hi": 0.2,
                         "theta_hi_status": "grid_edge_no_interior_optimum", "objective": 0.3}],
        "grid": [
            {"theta_lo": 0.05, "theta_hi": 0.2, "expected_wrong_payouts": 0.1, "expected_missed_payouts": 0.2, "objective": 0.3, "chosen": True},
            {"theta_lo": 0.01, "theta_hi": 0.2, "expected_wrong_payouts": 0.2, "expected_missed_payouts": 0.2, "objective": 0.4, "chosen": False},
        ],
        "provenance": prov(),
    },
    "pricing_table": {
        "model_version": "v",
        "counties": {"48201": {"expected_events_per_year": 1.0, "prob_24h_given_event": 0.1,
                               "prob_premises_affected": 0.2, "credibility_weight": 0.5}},
        "rates": {"risk_margin_rate": 0.3, "expense_load_cents": 100},
        "payout_schedule": {"t4": 0.6, "t24": 1.0},
        "provenance": prov(),
    },
    "backtest": {
        "frequency": {"threshold": "theta_lo", "theta": 0.02, "unit": "qualifying events per county-month",
                      "primary_variant": "exclude_low_coverage",
                      "variants": {"exclude_low_coverage": VARIANT, "include_low_coverage": VARIANT}},
        "severity": {"threshold_hours": 4, "n_events_train": 10, "n_events_test": 3,
                     "gpd": {"shape": 0.1, "scale": 5.0, "location_hours": 4}, "ks_statistic": 0.05, "ks_pvalue": 0.3,
                     "prob_24h_given_event": {"fitted": 0.1, "empirical_train": 0.1, "empirical_test": 0.2},
                     "qq": [{"empirical_hours": 5.0, "model_hours": 5.2}]},
        "credibility": {"method": "buhlmann_straub", "k": 12.3, "counties": {"48201": {
            "state": "TX", "county_rate": 0.05, "state_rate": 0.04, "exposure_months": 96, "weight": 0.7, "blended_rate": 0.047}}},
        "pool": {"members": 1000, "years": [{"year": 2023, "premiums_cents": 100, "payouts_cents": 50, "loss_ratio": 0.5,
                                             "events": 3, "events_by_county": {"48201": 1}, "claims_paid": 10}]},
        "provenance": prov(),
    },
    "solvency": {"members": 1000, "simulations": 10, "ruin_probability": 0.01, "capital_1_in_200_cents": 5,
                 "stop_loss": {"attachment_cents": 100, "limit_cents": 200, "price_cents": 3},
                 "annual_loss_quantiles": [{"q": q, "loss_cents": 1} for q in (0.5, 0.9, 0.99, 0.995)], "provenance": prov()},
    "basis_risk": {
        "schemes": {s: dict(SCHEME) for s in ("index_only", "rules_perfect_witness", "rules_10pct_missing", "rules_30pct_missing")},
        "districts_used": [{"state": "Uttar Pradesh", "district": "Lucknow", "median_active_monitors": 9}],
        "min_active_monitors": 8, "theta_hi": 0.3, "theta_lo": 0.1,
        "fit_period": {"start": "2014-11-01", "end": "2017-02-28"}, "score_period": {"start": "2017-03-01", "end": "2019-12-31"},
        "provenance": prov(),
    },
    "data_quality": {
        "coverage_floor": 0.7,
        "rows": [{"fips": "48201", "county": "Harris", "state": "TX", "year": 2016, "county_customers": 1827686,
                  "expected_intervals": 35136, "intervals_present": 1447, "coverage_share": 0.041, "zero_rows": 0,
                  "max_customers_out": 830, "intervals_over_county_customers": 0, "in_model_universe": True,
                  "excluded_from_fitting": True, "exclusion_reason": "coverage_share below coverage_floor"}],
        "year_summary": [{"year": 2014, "rows": 1, "zero_rows": 0, "null_rows": 0, "off_grid_rows": 0,
                          "first_t": "2014-11-01T04:00:00Z", "last_t": "2014-12-31T00:00:00Z"}],
        "gap_bridge_evidence": {"events_examined": 1, "gaps": [{"minutes": 15, "count": 4}], "share_of_gaps_within_bridge": 0.9},
        "storm_windows": [{"id": "derecho-harris-2024", "fips": "48201", "start": "2024-05-16T00:00:00Z", "end": "2024-05-26T00:00:00Z",
                           "expected_intervals": 960, "intervals_present": 915, "zero_rows": 0, "longest_missing_minutes": 540, "notes": "n"}],
        "timestamp_check": {"assumed": "UTC", "storm": "s", "first_sharp_rise_utc": "2024-05-16T23:00:00Z", "consistent_with_utc": True, "evidence": "e"},
        "county_coverage": {"48201": {"data_coverage": 0.99}},
        "provenance": prov(),
    },
}


@pytest.mark.parametrize("kind", sorted(VALID))
def test_valid_artifacts_pass(kind):
    validate_artifact(kind, VALID[kind])


def _broken(kind, mutate):
    d = copy.deepcopy(VALID[kind])
    mutate(d)
    return d


MALFORMED = [
    ("scenario", "renamed key", lambda d: d.__setitem__("county_customer", d.pop("county_customers"))),
    ("scenario", "float customers_out", lambda d: d["series"][0].__setitem__("customers_out", 1.5)),
    ("scenario", "string share", lambda d: d["series"][0].__setitem__("share", "0.1")),
    ("scenario", "only one value null", lambda d: d["series"][0].__setitem__("share", None)),
    ("scenario", "share above 1", lambda d: d["series"][2].__setitem__("share", 1.2)),
    ("scenario", "time without Z", lambda d: d.__setitem__("start", "2024-07-08T00:00:00")),
    ("scenario", "series not 15-minute", lambda d: d["series"][1].__setitem__("t", "2024-07-08T00:20:00Z")),
    ("scenario", "series skips a row", lambda d: d["series"].pop(1)),
    ("scenario", "fips as int", lambda d: d.__setitem__("fips", 48201)),
    ("scenario", "extra field", lambda d: d.__setitem__("confidence", 0.9)),
    ("scenario", "source as string", lambda d: d.__setitem__("source", "EAGLE-I")),
    ("scenario", "source missing sha256", lambda d: d["source"].pop("sha256")),
    ("scenario", "no provenance", lambda d: d.pop("provenance")),
    ("scenario", "provenance missing seed", lambda d: d["provenance"].pop("seed")),
    ("scenario", "test years in train", lambda d: d["provenance"].__setitem__("train_years", [2014, 2024])),
    ("scenario", "bad sha256", lambda d: d["provenance"]["data_sources"][0].__setitem__("sha256", "xyz")),
    ("scenario", "rows_used float", lambda d: d["provenance"]["data_sources"][0].__setitem__("rows_used", 1.0)),
    ("scenario", "placeholder text", lambda d: d.__setitem__("description", "placeholder threshold")),
    ("trigger_params", "theta_lo above theta_hi", lambda d: d.__setitem__("theta_lo", 0.5)),
    ("trigger_params", "theta as string", lambda d: d.__setitem__("theta_hi", "0.2")),
    ("trigger_params", "null without notes", lambda d: (d.__setitem__("theta_hi", None), d["provenance"].__setitem__("notes", ""))),
    ("trigger_params", "missing dropout_max_fraction", lambda d: d.pop("dropout_max_fraction")),
    ("trigger_params", "missing event_merge_hours", lambda d: d.pop("event_merge_hours")),
    ("trigger_params", "grid item extra key", lambda d: d["grid"][0].__setitem__("n", 1)),
    ("trigger_params", "grid two chosen", lambda d: d["grid"][1].__setitem__("chosen", True)),
    ("trigger_params", "chosen differs from theta", lambda d: d.__setitem__("theta_lo", 0.01)),
    ("trigger_params", "chosen not bool", lambda d: d["grid"][0].__setitem__("chosen", 1)),
    ("pricing_table", "bad fips key", lambda d: d["counties"].__setitem__("4820", d["counties"]["48201"])),
    ("pricing_table", "cents as float", lambda d: d["rates"].__setitem__("expense_load_cents", 1.5)),
    ("pricing_table", "missing t24", lambda d: d["payout_schedule"].pop("t24")),
    ("pricing_table", "probability above 1", lambda d: d["counties"]["48201"].__setitem__("prob_24h_given_event", 1.2)),
    ("solvency", "NaN ruin probability", lambda d: d.__setitem__("ruin_probability", math.nan)),
    ("solvency", "members bool", lambda d: d.__setitem__("members", True)),
    ("solvency", "quantiles as object", lambda d: d.__setitem__("annual_loss_quantiles", {})),
    ("solvency", "quantile missing", lambda d: d["annual_loss_quantiles"].pop()),
    ("solvency", "quantile float cents", lambda d: d["annual_loss_quantiles"][0].__setitem__("loss_cents", 1.5)),
    ("basis_risk", "missing scheme", lambda d: d["schemes"].pop("rules_30pct_missing")),
    ("basis_risk", "schemes at top level", lambda d: d.update(d.pop("schemes"))),
    ("basis_risk", "scheme missing count", lambda d: d["schemes"]["index_only"].pop("paid")),
    ("basis_risk", "period as string", lambda d: d.__setitem__("fit_period", "2014-11-01/2017-02-28")),
    ("basis_risk", "district as string", lambda d: d.__setitem__("districts_used", ["Lucknow"])),
    ("backtest", "missing pool", lambda d: d.pop("pool")),
    ("backtest", "frequency threshold hi", lambda d: d["frequency"].__setitem__("threshold", "theta_hi")),
    ("trigger_params", "missing theta_hi_status", lambda d: d.pop("theta_hi_status")),
    ("trigger_params", "bad theta_hi_status", lambda d: d.__setitem__("theta_hi_status", "optimum")),
    ("trigger_params", "missing sensitivity", lambda d: d.pop("sensitivity")),
    ("trigger_params", "sensitivity row missing key", lambda d: d["sensitivity"][0].pop("objective")),
    ("backtest", "missing variant", lambda d: d["frequency"]["variants"].pop("include_low_coverage")),
    ("backtest", "variant missing model", lambda d: d["frequency"]["variants"]["exclude_low_coverage"]["deviance"].pop("nb_glm")),
    ("backtest", "realised float", lambda d: d["frequency"]["variants"]["exclude_low_coverage"]["per_county_year"][0].__setitem__("realised", 1.0)),
    ("backtest", "credibility list", lambda d: d["credibility"].__setitem__("counties", [])),
    ("backtest", "pool cents float", lambda d: d["pool"]["years"][0].__setitem__("payouts_cents", 1.5)),
    ("backtest", "severity extra", lambda d: d["severity"].__setitem__("aic", 1.0)),
    ("data_quality", "only provenance", lambda d: d.pop("rows")),
    ("data_quality", "row missing key", lambda d: d["rows"][0].pop("coverage_share")),
    ("data_quality", "row bool as int", lambda d: d["rows"][0].__setitem__("excluded_from_fitting", 1)),
    ("data_quality", "coverage above 1", lambda d: d["rows"][0].__setitem__("coverage_share", 1.5)),
    ("data_quality", "county_coverage bad key", lambda d: d["county_coverage"].__setitem__("482", {"data_coverage": 0.5})),
    ("data_quality", "storm window missing notes", lambda d: d["storm_windows"][0].pop("notes")),
]


@pytest.mark.parametrize("kind,why,mutate", MALFORMED, ids=[f"{k}:{w}" for k, w, _ in MALFORMED])
def test_malformed_artifacts_are_rejected(kind, why, mutate):
    with pytest.raises(ArtifactSchemaError):
        validate_artifact(kind, _broken(kind, mutate))


def test_null_with_reason_is_allowed():
    d = _broken("trigger_params", lambda d: d["grid"][0].__setitem__("objective", None))
    d["provenance"]["notes"] = "theta_lo not computed: reason"
    validate_artifact("trigger_params", d)


def test_malformed_artifact_is_not_written(tmp_path):
    bad = _broken("scenario", lambda d: d.pop("series"))
    with pytest.raises(ArtifactSchemaError):
        write_artifact("scenario", bad, out_dir=tmp_path)
    assert not any(tmp_path.rglob("*.json*"))


def test_valid_artifact_is_written_and_round_trips(tmp_path):
    p = write_artifact("scenario", VALID["scenario"], out_dir=tmp_path)
    assert p == tmp_path / "scenarios" / "beryl-harris-2024.json"
    assert json.loads(p.read_text()) == VALID["scenario"]
    p2 = write_artifact("trigger_params", VALID["trigger_params"], out_dir=tmp_path)
    assert p2.name == "trigger_params.json"


# CONTRACTS.md 1.2: pricing_table.counties is an object keyed by FIPS with exactly four fields.
PRICING_COUNTY_CASES = [
    ("missing credibility_weight", lambda d: d["counties"]["48201"].pop("credibility_weight")),
    ("extra county field", lambda d: d["counties"]["48201"].__setitem__("expected_annual_payout_cents", 10)),
    ("county value not an object", lambda d: d["counties"].__setitem__("48201", 0.5)),
    ("counties as a list", lambda d: d.__setitem__("counties", [d["counties"]["48201"]])),
    ("counties empty", lambda d: d.__setitem__("counties", {})),
    ("fips key with letters", lambda d: d["counties"].__setitem__("4820A", d["counties"]["48201"])),
    ("negative expected events", lambda d: d["counties"]["48201"].__setitem__("expected_events_per_year", -1.0)),
]


@pytest.mark.parametrize("why,mutate", PRICING_COUNTY_CASES, ids=[w for w, _ in PRICING_COUNTY_CASES])
def test_pricing_counties_shape_is_enforced(why, mutate):
    with pytest.raises(ArtifactSchemaError):
        validate_artifact("pricing_table", _broken("pricing_table", mutate))


def test_pricing_counties_several_fips_and_null_with_reason():
    d = copy.deepcopy(VALID["pricing_table"])
    d["counties"]["12086"] = dict(d["counties"]["48201"], credibility_weight=None)
    d["provenance"]["notes"] = "credibility_weight for 12086 not computed: reason"
    validate_artifact("pricing_table", d)


# CONTRACTS.md 1.2: solvency.stop_loss keys are attachment_cents, limit_cents, price_cents (int or null).
STOP_LOSS_CASES = [
    ("old unit-less keys", lambda d: d.__setitem__("stop_loss", {"attachment": 1, "limit": 2, "price": 3})),
    ("missing price_cents", lambda d: d["stop_loss"].pop("price_cents")),
    ("extra key", lambda d: d["stop_loss"].__setitem__("rate_on_line", 0.1)),
    ("float cents", lambda d: d["stop_loss"].__setitem__("limit_cents", 200.0)),
    ("string cents", lambda d: d["stop_loss"].__setitem__("attachment_cents", "100")),
    ("negative cents", lambda d: d["stop_loss"].__setitem__("price_cents", -1)),
    ("bool cents", lambda d: d["stop_loss"].__setitem__("price_cents", True)),
]


@pytest.mark.parametrize("why,mutate", STOP_LOSS_CASES, ids=[w for w, _ in STOP_LOSS_CASES])
def test_stop_loss_shape_is_enforced(why, mutate):
    with pytest.raises(ArtifactSchemaError):
        validate_artifact("solvency", _broken("solvency", mutate))


def test_stop_loss_null_values_need_a_reason():
    d = _broken("solvency", lambda d: d["stop_loss"].__setitem__("price_cents", None))
    validate_artifact("solvency", d)  # fixture notes are non-empty
    d["provenance"]["notes"] = ""
    with pytest.raises(ArtifactSchemaError):
        validate_artifact("solvency", d)


def test_contract_version_is_current():
    from fourhour_ml.schema import CONTRACT_VERSION, UNSPECIFIED

    assert CONTRACT_VERSION == "1.7" and UNSPECIFIED == {}


def test_basis_risk_caveats_go_in_provenance_notes_not_a_top_level_field():
    with pytest.raises(ArtifactSchemaError):
        validate_artifact("basis_risk", _broken("basis_risk", lambda d: d.__setitem__("notes", "upper bound")))
