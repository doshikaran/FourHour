"""Regenerate artifacts/ from the raw EAGLE-I and ESMI files.

    cd ml && .venv/bin/python build_artifacts.py            # every artifact
    cd ml && .venv/bin/python build_artifacts.py data_quality trigger_params

Each artifact is validated against CONTRACTS.md 8.1 before it is written; an artifact that
fails is not written and the command exits 1.
"""

from __future__ import annotations

import sys
import time

from fourhour_ml import build
from fourhour_ml.schema import write_artifact

STAGES = {
    "data_quality": ("data_quality", build.build_data_quality),
    "trigger_params": ("trigger_params", build.build_trigger_params),
    "pricing_table": ("pricing_table", build.build_pricing_table),
    "solvency": ("solvency", build.build_solvency),
    "backtest": ("backtest", build.build_backtest),
    "scenarios": ("scenario", build.build_scenarios),
    "basis_risk": ("basis_risk", build.build_basis_risk),
}


def main(argv: list[str]) -> int:
    names = argv or list(STAGES)
    unknown = [n for n in names if n not in STAGES]
    if unknown:
        print(f"unknown stage(s) {unknown}; choose from {list(STAGES)}")
        return 2
    for n in names:
        t0 = time.time()
        kind, fn = STAGES[n]
        out = fn()
        docs = out if isinstance(out, list) else [out]
        for d in docs:
            p = write_artifact(kind, d)
            print(f"wrote {p} ({time.time() - t0:.0f}s)")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
