# Deploying FourHour on Render

Sandbox only. The backend refuses to start unless `PAYPAL_BASE_URL` is `https://api-m.sandbox.paypal.com`.

`render.yaml` at the repo root defines two services:

| Service | Type | Build | Serves |
|---|---|---|---|
| `fourhour-api` | Go web service | `cd backend && go build -o ../bin/fourhour ./cmd/fourhour` | `/api/*`, health check `/api/health` |
| `fourhour-web` | Static site | `cd frontend && npm ci && npm run build` | `frontend/dist`, every path rewritten to `/index.html` |

The backend reads `artifacts/` from the repo at `./artifacts`, so the committed artifacts ship with it.

## 1. Create the services

1. Render dashboard → New → Blueprint → pick this GitHub repo. Render reads `render.yaml` and lists both services.
2. Render asks for every variable marked `sync: false`. For the first deploy you don't know the two URLs yet: put `https://example.invalid` in `APP_BASE_URL`, `FRONTEND_URL` and `VITE_API_BASE_URL`, and leave `PAYPAL_WEBHOOK_ID` empty. Fill in the rest from your local `.env`.
3. Apply. Wait for both services to go live and note their URLs, e.g. `https://fourhour-api.onrender.com` and `https://fourhour-web.onrender.com`.

## 2. Set the real URLs

| Service | Variable | Value |
|---|---|---|
| `fourhour-api` | `APP_BASE_URL` | the API URL, no trailing slash (PayPal's vault `return_url`/`cancel_url` are built from it) |
| `fourhour-api` | `FRONTEND_URL` | the static site URL, no trailing slash (vault redirect target and CORS origin) |
| `fourhour-web` | `VITE_API_BASE_URL` | the API URL **plus `/api`** |

Save. The API restarts. The static site needs a manual redeploy (Manual Deploy → Clear build cache & deploy), because Vite reads `VITE_*` values at build time.

Check: open `<API URL>/api/health`. It should return `"status":"ok"`, `"paypal_env":"sandbox"` and all six names in `artifacts_loaded`.

## 3. Register the webhook

1. developer.paypal.com → Apps & Credentials → **Sandbox** → your app → Webhooks → Add Webhook.
2. URL: `<API URL>/api/paypal/webhook`
3. Events (`CONTRACTS.md` section 7): `PAYMENT.CAPTURE.COMPLETED`, `PAYMENT.CAPTURE.DENIED`, `PAYMENT.CAPTURE.REFUNDED`, `PAYMENT.PAYOUTSBATCH.SUCCESS`, `PAYMENT.PAYOUTSBATCH.DENIED`, `PAYMENT.PAYOUTS-ITEM.SUCCEEDED`, `PAYMENT.PAYOUTS-ITEM.FAILED`, `PAYMENT.PAYOUTS-ITEM.UNCLAIMED`.
4. Save, copy the **Webhook ID** into `PAYPAL_WEBHOOK_ID` on `fourhour-api`, and save again (the service restarts).

Without the webhook, payouts are still confirmed by polling (`confirmed_via` = `poll`). With it, a confirmed payout should show `confirmed_via` = `webhook` when the webhook arrives first. Events sent from PayPal's webhook simulator can't be verified and are stored with `verified = false`; that is expected.

## 4. Things to know

- **The database is a SQLite file on the free plan's temporary disk.** It is wiped on every deploy and restart, and free services sleep after about 15 minutes idle (the first request then takes up to a minute). Before recording, open `/api/health` to wake the service, and don't redeploy between enrolment and the replay. For a database that survives restarts, attach a Render persistent disk (paid plan) and point `DATABASE_URL` at a file on it.
- **Demo policy at startup.** With `DEMO_VAULT_TOKEN`, `SANDBOX_MEMBER_EMAIL` and `SANDBOX_PAYOUT_EMAIL` set, the backend creates a demo member and an active $50 Harris County policy whenever its database is new, and charges the first premium for real in the sandbox. On the free plan the database is wiped on every restart and wake-up, so each wake-up makes one new sandbox premium charge from the demo buyer account. The `seed` command (the $1.00 test cover) is for local runs only.
- **Funding.** Payouts come out of the sandbox business account's balance. Check it in the sandbox dashboard before a demo.
- **CORS.** If the browser shows CORS errors, `FRONTEND_URL` doesn't exactly match the static site's origin (scheme, host, no trailing slash).
