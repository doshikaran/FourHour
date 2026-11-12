"""Constants shared by the ML pipeline. Change a value here, not at a call site."""

from __future__ import annotations

from pathlib import Path

ML_DIR = Path(__file__).resolve().parent.parent
REPO_DIR = ML_DIR.parent
RAW_DIR = ML_DIR / "data" / "raw"
DERIVED_DIR = ML_DIR / "data" / "derived"
ARTIFACTS_DIR = REPO_DIR / "artifacts"

# CONTRACTS.md section 8 / CLAUDE.md rule 3. The only place the split years are defined.
TRAIN_YEARS: tuple[int, int] = (2014, 2022)  # inclusive
TEST_YEARS: tuple[int, int] = (2023, 2024)  # inclusive

SEED = 20261111

# States kept from the national EAGLE-I files (the three demo counties are in these).
KEEP_STATES: tuple[str, ...] = ("Texas", "Florida", "Illinois")
STATE_ABBR: dict[str, str] = {"Texas": "TX", "Florida": "FL", "Illinois": "IL"}

DEMO_COUNTIES: dict[str, str] = {
    "48201": "Harris County, TX",
    "12086": "Miami-Dade County, FL",
    "17031": "Cook County, IL",
}

INTERVAL_MINUTES = 15

# ---- Product parameters (SPEC.md section 4, decisions D1, D2). Set by the humans, not fitted.
GAP_BRIDGE_MINUTES = 120  # D1: missing runs up to 2 h between readings at or above are bridged
EVENT_MERGE_HOURS = 72  # D2: hours clause
DROPOUT_MAX_FRACTION = 0.1  # CONTRACTS 1.6 section 3, set by the humans on 2026-11-12
QUALIFY_HOURS = 4  # USDA 4-hour line
T24_HOURS = 24
# SPEC section 4, CONTRACTS 1.8: only the 4-hour tier is paid and priced (60% is a product
# assumption). The 24-hour top-up was cut on 2026-11-12; T24_HOURS stays for the severity report.
PAYOUT_SCHEDULE = {"t4": 0.6}
R2_FACTOR = 0.5  # SPEC section 4, rule R2
POOL_MEMBERS = 1000  # SPEC 5.7 default pool size
COVERS_CENTS = (5000, 15000, 30000)

# Set by the humans on 2026-11-12 (lead's questions Q4 and Q5). Product parameters, not fitted.
EXPENSE_LOAD_CENTS: int | None = 100  # per member per month
COST_OF_CAPITAL_RATE: float | None = 0.06  # turns the 1-in-200 capital into risk_margin_rate
WITNESS_SHARE: float | None = 0.5  # share of members with a registered witness (trigger grid)
FALSE_CONFIRMATION_RATE: float | None = 0.10  # rate a witness wrongly confirms an R3 payment
PRODUCT_PARAMS_SET_ON = "2026-11-12"
SENSITIVITY_WITNESS_SHARE = (0.25, 0.5, 0.75)
SENSITIVITY_FALSE_CONFIRMATION = (0.05, 0.10, 0.20)
NOT_SET = "product parameter not yet set by the humans"

# ---- Data rules (stated before looking at test years; see ml/README.md).
# A county is in the model universe if its median row coverage over training years
# 2018-2022 is at least this; a county-year in the universe is excluded from fitting if its
# own coverage is below COVERAGE_FLOOR. Both read only training-year coverage, never a model
# result. COVERAGE_FLOOR was first 0.80 (equal to the universe rule); before any model was
# fitted, the training-year coverage table showed that 0.80 cuts into ordinary year-to-year
# variation (0.72-0.80 in counties whose median is 0.80-0.87), while feed-missing years sit
# at 0.69 or lower. 0.70 is the gap between those two clusters.
UNIVERSE_MIN_MEDIAN_COVERAGE = 0.80
UNIVERSE_YEARS = (2018, 2022)
COVERAGE_FLOOR = 0.70
