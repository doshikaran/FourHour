# FourHour build status

Updated: 2026-11-12 19:20 IST by the lead (ship run). Earlier: Final development pass in progress (humans' items 1–6); freeze 15:30 IST. Submission: 01:30 IST 13 Nov.

## Final pass (items 1–6): DONE 13:20 IST

- Contract 1.8; SPEC 4 and 5.5 updated.
- Items 1, 3, 4, 5 built (backend, frontend, ML); item 2 Harris finding: no bug (Uri peak 0.2495, longest run at 0.20 = 2.75 h), recorded in `data_quality.json` findings and README.
- Artifacts regenerated from clean `0938669`; committed in `e564b91`.
- Item 6 clean clone (local clone of e564b91, copy of .env, README steps): backend starts, 6 artifacts loaded, startup demo policy `pol_57dozqd52j6g` charged 136 cents (order `7SS37135JB9144335`, capture `70968229F6247641B`, COMPLETED); restart on same DB: `created:false`, no charge; Cook quote 422 `county_not_offered`; 100-cent cover refused; frontend `npm ci` + `npm run dev` serves. Not checked: pages in a browser (headless Chrome hung on the live stream). README fix: ml test setup step.
- Commits `0938669`, `e564b91` (+ final) are LOCAL: humans must `git push origin main`.

Resume from this file and `TASKS.md` alone.

## Ship run (started 19:05 IST, Mac)

| Item | State | Evidence |
|---|---|---|
| Track A restyle | running | worktree `../fourhour-ui-restyle`, branch `ui-restyle`, not merged |
| B1 repo check | **fail** | `GET api.github.com/repos/doshikaran/FourHour` without auth returns 404: repo is private, so the licence can't be detected publicly. README has all 9 SPEC 10 sections in order; `LICENSE` is MIT |
| B2 deploy | **blocked** | `RENDER_API_KEY` is not in `.env` |
| B3 webhook | blocked on B2 | |
| B4 hosted run | blocked on B2 | |
| B5 audit | running | independent reviewer |
| B6 licences | done | `ml/README.md`: figshare API + DataCite agree CC BY 4.0; Dataverse API + DataCite agree CC0 1.0; landing pages bot-blocked (202, empty). README attribution now names the creators and links the licence |
| B7 screens | blocked on B2 | |
| B8 submission pack | pending | |


## Sandbox money calls: 4 runs of 5, plus 1 startup demo premium charge (136 cents) in item 6

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
