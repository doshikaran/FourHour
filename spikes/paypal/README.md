# PayPal spikes (S1, S2)

Two standalone Go programs. Both read only the `.env` at the repo root (found by walking up to `CONTRACTS.md`; override with `-env-file`), refuse to run unless `PAYPAL_BASE_URL` is exactly `https://api-m.sandbox.paypal.com`, print the `paypal-debug-id` of every response, print the full body of any error, and never print the client secret or access token. The shared request and response types live in `paypal/` (standard library only) so they can move into `backend/` unchanged.

Run from this directory (`spikes/paypal/`) with Go 1.27 or later. Since Gate 2 the request and response types used by the service live in `backend/internal/paypal/` (copied from `paypal/` here and extended); this directory is kept unchanged as the record of spikes S1 and S2.

- **S1, vault and merchant-initiated charge:** `go run ./cmd/s1-vault` (flags: `-port 8799`, `-wait 10m`, `-env-file PATH`). Open the printed approval URL in a browser, log in as a **sandbox personal** account and approve. The program listens on `localhost:8799` for `/return` or `/cancel`, then exchanges the token and charges $1.00 with no buyer present, and repeats the charge with the same `PayPal-Request-Id`. **Pass:** the last line is `RESULT: PASS order_id=... capture_id=...` (order and capture both `COMPLETED`), exit code 0. Record the order ID and the `IDEMPOTENCY:` line in `TASKS.md`.
- **S2, payout:** `go run ./cmd/s2-payout <sandbox-personal-email>` (flags: `-sender-batch-id ID`, `-poll-interval 5s`, `-poll-timeout 2m`, `-env-file PATH`). Sends $1.00, polls until the batch leaves `PENDING`/`PROCESSING` or 2 minutes pass, then resends the same `sender_batch_id` and prints PayPal's full response. **Pass:** last line `RESULT: PASS payout_batch_id=... batch_status=SUCCESS`, exit code 0, and the recipient's sandbox balance went up by $1.00 (check by hand). Record the `[P5 repeat]` block in `TASKS.md`.

Exit codes for both: 0 pass, 1 fail (with `ERROR at <step>`, HTTP status, `paypal-debug-id` and full body), 2 configuration or usage error (nothing was sent to PayPal).

Tests: `go test -race ./...`. They run against `paypaltest`, a fake server whose responses are **hand-written** from the PayPal API reference, not recorded from the sandbox.
