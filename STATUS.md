# FourHour build status

Updated: 2026-11-12 12:50 IST by the lead. Final development pass in progress (humans' items 1–6); freeze 15:30 IST. Submission: 01:30 IST 13 Nov.

## Final pass (items 1–6), 13:25 IST

- Contract 1.8 (t4-only pricing, `offered`, `capital_assumption`, `findings`, startup demo policy + `demo_policy` table, `GET /api/demo`); SPEC 4 and 5.5 updated.
- Backend DONE (items 1, 3, 5): t4-only quote, `TestPricedScheduleEqualsPaidSchedule`, 422 `county_not_offered`, startup demo policy (one row, first premium only), `GET /api/demo`, unknown device 404 `device_not_found`.
- Frontend DONE (items 1, 3, 4, 5) against mock: 112 tests pass.
- ML DONE (items 1, 2, 3, 4): Harris finding = case (2), share peaked 0.2495 in Uri, longest run 2.75 h, no bug; t4-only pricing (Harris $50/$150/$300 = 136/209/319 cents), Cook not offered, capital_assumption 0 starting capital. pytest 163.
- Checkpoint commit `0938669` (local; push pending human sign-in). All suites passed on it (backend -race all ok, ml 163, frontend 112 + build).
- In progress: ML regenerating all artifacts from clean `0938669`. Then: lead commits artifacts + README, human pushes, lead runs item 6 clean clone.

Resume from this file and `TASKS.md` alone.

## Sandbox runs used: 4 of 5

| Run | When | What | IDs |
|---|---|---|---|
| 1–2 | 2026-11-11 | Previous machine | see TASKS.md S1/S2 |
| 3 | 12:17 IST | `FOURHOUR_SANDBOX_IT=1 go test -v ./internal/integration/` PASS | charge order `5UG31672BS8343058`, capture `1E8763885K549590F` (100 cents); claim `clm_ytbevero7t55` R2 30 cents; payout batch `G6NW9GX8C8K98` item `2CUWU899JMFXG` SUCCESS via poll |
| 4 | ~13:10 IST | Paid derecho replay from /live with the real witness page (headless Chrome, self-reported) | claim `clm_tr7vun2ixysq` R3 60 cents; payout `pay_xn2i6f3yhzzi`, batch `Q7NNEG8GBSPC4`, item `697FJHGSXLYC4` SUCCESS via poll |

## State

- All suites pass on the final tree: backend `CGO_ENABLED=1 go test -race ./...` all ok; ml `pytest` 148 passed; frontend typecheck clean, vitest 102 passed, build OK.
- Artifacts: all 8 regenerated from a clean worktree, provenance `e9fbacd`; scenario files now committed (`artifacts/.gitignore` removed per the humans' licence decision).
- Backend parity test proves the backend and ML agree on the derecho runs.
- Witness: a real page's entries are accepted; a tampered entry is rejected `bad_signature`.
- README (H1) written from artifacts; `render.yaml` + `DEPLOY.md` (H2 prep) written.
- Cuts recorded in TASKS.md.

## Machine (Windows 11, x86_64, i7-14700, no battery)

- Go 1.27.4 at `%USERPROFILE%\sdk\go\bin` (user PATH). gcc for -race: WinLibs at `%LOCALAPPDATA%\Microsoft\WinGet\Packages\BrechtSanders.WinLibs.POSIX.UCRT_*\mingw64\bin` (not on PATH; prepend it and set `CGO_ENABLED=1`).
- `ml/.venv/Scripts/python`; frontend `npm ci` done.
- `.env` has local non-secret defaults appended; `PAYPAL_WEBHOOK_ID` is empty.
- `git config core.fileMode false` set locally.

## Blocked / open

- **Git push**: GitHub returns 401 on this machine until a human signs in. Everything since e9fbacd is uncommitted until the humans run `scripts/push.sh`.
- Seeded policy `pol_bz7eouein5ad` has device `dev_mpozsu6y54wg` whose key lives only in a headless Chrome profile; register a new device from a real browser for the video.
- `/evidence` still has a small layout shift (CLS ~0.21) as panels load; left as is.
- npm `dev:mock`/`build:mock` scripts use POSIX env syntax; they fail in cmd on Windows (not needed for the demo).

## Next three actions (humans)

1. Sign in to git, then commit and push with `scripts/push.sh`.
2. M7: review the artifacts and README numbers; confirm the S6 licence pages.
3. H2 account steps per `DEPLOY.md`, then the video.
