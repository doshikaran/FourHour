# FourHour

FourHour is an automatic power-outage cover prototype for food trucks, home bakers, and small grocers. A seller gets a quote, approves saving a PayPal wallet, and registers a phone or laptop at the business as a **witness**. The device signs a log of charger power changes. When a qualifying county outage overlaps a four-hour witness outage, FourHour creates a claim and sends a PayPal Sandbox payout of **60% of the selected cover**. [USDA guidance on keeping food cold during a power outage](https://ask.fsis.usda.gov/article/How-do-you-keep-food-safe-during-a-power-failure)

**[Open the live demo](https://fourhour-web.onrender.com/live)** · [Get a quote](https://fourhour-web.onrender.com/enrol) · [Evidence](https://fourhour-web.onrender.com/evidence) · [Operator dashboard](https://fourhour-web.onrender.com/operator) · [API health](https://fourhour-api.onrender.com/api/health)

### Try the hosted replay

1. Open **Live Replay** and register a witness from the demo panel. The seeded $50 Harris County policy lets you try the replay without a PayPal login.
2. Start **Houston derecho, Harris County, May 2024** (`derecho-harris-2024`). When the index crosses **20%**, select **Power is out** on the witness page.
3. After four simulated hours of qualifying evidence, open the R3 claim to see the rule, witness log, and **$30 Sandbox payout** status.
4. Try **Quiet week, Harris County, 2024** (`spoof-quiet`) with the same witness action. The claim is flagged for review and pays **$0**.

The Render API can take up to a minute to wake. Each qualifying replay draws from the demo account’s finite PayPal Sandbox balance.

## PayPal integration

All payment activity uses PayPal Sandbox. The backend records charges and payouts before making the API call and uses stable request or batch IDs to handle retries.

| PayPal tool | Endpoint | Purpose | Code |
| --- | --- | --- | --- |
| OAuth 2.0 | `POST /v1/oauth2/token` | Authenticate backend requests | [Client](backend/internal/paypal/client.go) |
| Vault | `POST /v3/vault/setup-tokens` | Start wallet approval | [Client](backend/internal/paypal/client.go) |
| Vault | `POST /v3/vault/payment-tokens` | Exchange the approved setup token for a payment token | [Client](backend/internal/paypal/client.go) |
| Orders v2 | `POST /v2/checkout/orders` | Charge the premium against the saved wallet | [Policy service](backend/internal/service/policies.go) |
| Payouts v1 | `POST /v1/payments/payouts` | Send a qualifying claim payment | [Payout service](backend/internal/service/payouts.go) |
| Payouts v1 | `GET /v1/payments/payouts/{batch_id}` | Check for a final payout status | [Payout service](backend/internal/service/payouts.go) |
| Webhooks | `POST /v1/notifications/verify-webhook-signature` | Verify incoming payment events | [Verification client](backend/internal/paypal/p7p8.go) |

## Models and evidence

The Python pipeline fits statistical models on **2014–2022** outage data and evaluates them on **2023–2024**. The Go service reads the resulting [versioned artifacts](artifacts/) and applies explicit claim rules. Hurricane Beryl influenced event reconstruction, and the Houston derecho informed the timestamp check. [Modeling guide](ml/README.md)

| Component | Method and result |
| --- | --- |
| Outage reconstruction | Builds events from 15-minute readings and handles short gaps and feed dropouts. Excluded 54 of 198 training county-years for low coverage. [Data quality](artifacts/data_quality.json) |
| Trigger thresholds | A grid search selected **20%** and **90%** county outage thresholds. The upper threshold sits at the grid boundary. [Trigger parameters](artifacts/trigger_params.json) |
| Frequency and duration | The negative-binomial frequency model performed similarly to the Poisson baseline. The generalized Pareto model estimated an 81% chance that a qualifying event lasts 24 hours. The evaluation data showed 68%. [Backtest](artifacts/backtest.json) |
| Credibility and pricing | Bühlmann–Straub county weights were zero, so quotes use state-level rates. Harris and Miami-Dade quote **$1.36**, **$2.09**, and **$3.19** per month for **$50**, **$150**, and **$300** of cover. Cook County’s pricing group had no qualifying training event and receives no quote. [Pricing table](artifacts/pricing_table.json) |
| Portfolio backtest | The simulated loss ratio was **0.00 in 2023** and **1.32 in 2024**. Two Harris County events drove 351 simulated claims in 2024. [Backtest](artifacts/backtest.json) |
| Solvency | Across 20,000 simulated years, payouts exceeded net premium income in **12.3%** of years under a zero-starting-capital assumption. One training storm year drove simulated losses. [Solvency model](artifacts/solvency.json) |

### Basis risk

The Prayas ESMI study evaluated **5,010 premises outages** lasting at least four hours across eight Indian districts.

| Evidence used | Eligible outages missed | Payments to premises that had power |
| --- | ---: | ---: |
| District index alone | 70.4% | 35.3% |
| Index plus a perfect witness | 5.9% | 0.0% |
| Index plus a witness with 10% of logs missing | 10.9% | 1.7% |
| Index plus a witness with 30% of logs missing | 23.5% | 6.0% |

The perfect witness was the premises’ own supply monitor. Removing that monitor from the district index raised its miss rate to **17.1%** in the audit. One district supplied 71% of the evaluated outages, and each missing-log scenario uses one simulated draw. [Study results](artifacts/basis_risk.json)

## What is real and what is simulated

| Component | Demo behavior |
| --- | --- |
| Outage readings and model inputs | Historical county and premises data |
| Witness | Browser-signed events mapped onto the simulated replay clock |
| Policies and portfolio | Demo sellers, simulated cover, and backtested claims |
| Payment integration | Actual Vault, Orders, Payouts, and Webhook calls within PayPal Sandbox |

## Data sources

- **EAGLE-I**, Brelsford, Tennille, Myers, Chinthavali, Tansakul, Denman et al. (2023), Oak Ridge National Laboratory / U.S. Department of Energy. FourHour derives county aggregates and replay slices from the 2014–2024 files. Coverage varies by county and year, and storms can interrupt the feed. [Dataset](https://doi.org/10.6084/m9.figshare.24237376.v4) · [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/) · [Coverage report](artifacts/data_quality.json)
- **Electricity Supply Monitoring Initiative**, Prayas (Energy Group). Premises-level supply readings from India used to measure basis risk. [Dataset](https://doi.org/10.7910/DVN/CLLZZM) · [CC0 1.0](https://creativecommons.org/publicdomain/zero/1.0/)

## Limitations

- **County resolution.** Local outages below 20% of county customers fall outside the payout rules. The county index is an imperfect measure of power at a specific business.
- **Witness reliability.** Unplugging a charger can mimic an outage. The engine flags an uncorroborated witness outage for review. Manual reports use the same decision rules as sensed events.
- **Witness incentives.** At the 90% threshold, R2 can pay 30% of cover when a log is absent, while R4 declines a claim when a log is present without a qualifying witness outage.
- **Thin pricing evidence.** The portfolio backtest spans two years. The $1 expense load and assumptions about witness availability and false reports need field validation.
- **Demo infrastructure.** The hosted API uses temporary SQLite storage. A restart can clear demo state.

FourHour is a sandbox prototype of a pricing, trigger, and claims engine for a licensed carrier.

## Run locally

The React and TypeScript frontend uses Vite and AG Grid Community. The Go backend serves the API and live replay stream, and SQLite stores policy, witness, claim, and ledger records.

**Frontend preview** — Node 20.19+ (20.x) or 22.12+.

```sh
cd frontend
npm ci
npm run dev:mock
```

Open [localhost:5173](http://localhost:5173). Preview mode uses fixtures.

**Full Sandbox stack** — Go 1.27+ and a PayPal Sandbox app with Vault and Payouts enabled.

1. Set up a funded Sandbox business account and Sandbox personal accounts for premium payment and payout receipt.
2. Copy [`.env.example`](.env.example) to `.env` and fill in the app credentials. Set `PAYPAL_BASE_URL` to `https://api-m.sandbox.paypal.com`, `APP_BASE_URL` to `http://localhost:8080`, `FRONTEND_URL` to `http://localhost:5173`, `VITE_API_BASE_URL` to `http://localhost:8080/api`, and `VITE_USE_MOCK` to `false`. For the seeded replay policy, also set `DEMO_VAULT_TOKEN`, `SANDBOX_MEMBER_EMAIL`, and `SANDBOX_PAYOUT_EMAIL`.
3. From the repository root, run the backend in one terminal and the frontend in another.

```sh
(cd backend && go run ./cmd/fourhour -env-file ../.env)
```

```sh
(cd frontend && npm ci && npm run dev)
```

Check [local API health](http://localhost:8080/api/health) for the six loaded artifacts. See the [backend guide](backend/README.md) for tests and Sandbox setup, the [modeling guide](ml/README.md) to regenerate artifacts with Python 3.12+, and [`render.yaml`](render.yaml) for the Render deployment.

## License

FourHour is licensed under the [MIT License](LICENSE). The datasets retain the licenses listed above.
