# FourHour backend (Go)

Run from `backend/`: `PAYPAL_BASE_URL=https://api-m.sandbox.paypal.com GOTOOLCHAIN=local go run -ldflags=-linkmode=external ./cmd/fourhour` (listens on `:$PORT`, default 8080; flags: `-addr` to override, `-env-file ../.env`; the process environment wins over the file). It refuses to start unless `PAYPAL_BASE_URL` is exactly the sandbox URL, applies `internal/db/migrations/*.sql` to the SQLite file at `DATABASE_URL` (default `./fourhour.db`), checks `ARTIFACTS_DIR` (default `../artifacts`), and serves `GET /api/health`.

Tests: `GOTOOLCHAIN=local go test -race -ldflags=-linkmode=external ./...`. The `-ldflags=-linkmode=external` flag is only needed for Go 1.22.2 on macOS 26 (internal-linker binaries abort with `dyld: missing LC_UUID load command`); drop it on a current Go release.

Fixture artifacts for tests are in `internal/artifacts/testdata/fixture-artifacts/`. Every value in them is null; they are not data.
