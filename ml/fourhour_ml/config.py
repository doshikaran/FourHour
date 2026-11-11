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

# SPEC.md section 5.1.
GAP_BRIDGE_MINUTES = 30
INTERVAL_MINUTES = 15
