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


VALID = {
    "scenario": {
        "id": "beryl-harris-2024", "title": "t", "description": "d", "fips": "48201", "county_customers": 100,
        "start": "2024-07-08T00:00:00Z", "end": "2024-07-08T00:30:00Z",
        "series": [
            {"t": "2024-07-08T00:00:00Z", "customers_out": 0, "share": 0.0},
            {"t": "2024-07-08T00:15:00Z", "customers_out": 5, "share": 0.05},
        ],
        "source": "EAGLE-I", "provenance": prov(),
    },
    "trigger_params": {"theta_hi": 0.2, "theta_lo": 0.05, "gap_bridge_minutes": 30, "grid": [{}], "provenance": prov()},
    "pricing_table": {
        "model_version": "v",
        "counties": {"48201": {"expected_events_per_year": 1.0, "prob_24h_given_event": 0.1,
                               "prob_premises_affected": 0.2, "credibility_weight": 0.5}},
        "rates": {"risk_margin_rate": 0.3, "expense_load_cents": 100},
        "payout_schedule": {"t4": 0.6, "t24": 1.0},
        "provenance": prov(),
    },
    "backtest": {"frequency": {}, "severity": {}, "credibility": {}, "pool": {}, "provenance": prov()},
    "solvency": {"members": 1000, "simulations": 10, "ruin_probability": 0.01, "capital_1_in_200_cents": 5,
                 "stop_loss": {"attachment_cents": 100, "limit_cents": 200, "price_cents": 3}, "annual_loss_quantiles": {}, "provenance": prov()},
    "basis_risk": {
        **{s: {"missed_rate": 0.1, "wrong_rate": 0.0} for s in
           ("index_only", "rules_perfect_witness", "rules_10pct_missing", "rules_30pct_missing")},
        "districts_used": [], "fit_period": {}, "score_period": {}, "provenance": prov(),
    },
    "data_quality": {"rows": [], "provenance": prov()},
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
    ("scenario", "time without Z", lambda d: d.__setitem__("start", "2024-07-08T00:00:00")),
    ("scenario", "series not 15-minute", lambda d: d["series"][1].__setitem__("t", "2024-07-08T00:20:00Z")),
    ("scenario", "fips as int", lambda d: d.__setitem__("fips", 48201)),
    ("scenario", "extra field", lambda d: d.__setitem__("confidence", 0.9)),
    ("scenario", "no provenance", lambda d: d.pop("provenance")),
    ("scenario", "provenance missing seed", lambda d: d["provenance"].pop("seed")),
    ("scenario", "test years in train", lambda d: d["provenance"].__setitem__("train_years", [2014, 2024])),
    ("scenario", "bad sha256", lambda d: d["provenance"]["data_sources"][0].__setitem__("sha256", "xyz")),
    ("scenario", "rows_used float", lambda d: d["provenance"]["data_sources"][0].__setitem__("rows_used", 1.0)),
    ("scenario", "placeholder text", lambda d: d.__setitem__("description", "placeholder threshold")),
    ("trigger_params", "theta_lo above theta_hi", lambda d: d.__setitem__("theta_lo", 0.5)),
    ("trigger_params", "theta as string", lambda d: d.__setitem__("theta_hi", "0.2")),
    ("trigger_params", "null without notes", lambda d: (d.__setitem__("theta_hi", None), d["provenance"].__setitem__("notes", ""))),
    ("pricing_table", "bad fips key", lambda d: d["counties"].__setitem__("4820", d["counties"]["48201"])),
    ("pricing_table", "cents as float", lambda d: d["rates"].__setitem__("expense_load_cents", 1.5)),
    ("pricing_table", "missing t24", lambda d: d["payout_schedule"].pop("t24")),
    ("pricing_table", "probability above 1", lambda d: d["counties"]["48201"].__setitem__("prob_24h_given_event", 1.2)),
    ("solvency", "NaN ruin probability", lambda d: d.__setitem__("ruin_probability", math.nan)),
    ("solvency", "members bool", lambda d: d.__setitem__("members", True)),
    ("basis_risk", "missing scheme", lambda d: d.pop("rules_30pct_missing")),
    ("backtest", "missing pool", lambda d: d.pop("pool")),
    ("data_quality", "only provenance", lambda d: d.pop("rows")),
]


@pytest.mark.parametrize("kind,why,mutate", MALFORMED, ids=[f"{k}:{w}" for k, w, _ in MALFORMED])
def test_malformed_artifacts_are_rejected(kind, why, mutate):
    with pytest.raises(ArtifactSchemaError):
        validate_artifact(kind, _broken(kind, mutate))


def test_null_with_reason_is_allowed():
    d = _broken("trigger_params", lambda d: d.__setitem__("theta_lo", None))
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


def test_basis_risk_caveats_go_in_provenance_notes_not_a_top_level_field():
    with pytest.raises(ArtifactSchemaError):
        validate_artifact("basis_risk", _broken("basis_risk", lambda d: d.__setitem__("notes", "upper bound")))
