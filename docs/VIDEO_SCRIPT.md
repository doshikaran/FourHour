# FourHour: video script (under 3 minutes)

Three parts of about one minute each, about 400 spoken words. Every number comes from `artifacts/` (file named in the caption notes). Record at 1920x1080. Members and loss amounts are simulated; outage data and model results are real. Keep the "simulated" and "real data" labels in shot.

Before recording: open https://fourhour-api.onrender.com/api/health to wake the free host. Don't redeploy between enrolment and the replay. Check the sandbox business balance.

## Part 1 (0:00–1:00): the problem, enrolment, the first premium

| # | Spoken (≈130 words) | On screen | Caption |
|---|---|---|---|
| 1 | "The USDA says food in a fridge without power is safe for about four hours." | Home page hero with the USDA quote and its link | USDA FSIS: about 4 hours |
| 2 | "For a food truck or a home baker with no generator, a short outage means throwing out the stock, and business cover is built for long outages and pays slowly." | Home page, scroll to "how it works" | Small food sellers, no generator |
| 3 | "FourHour pays automatically after four hours. No claim form, no adjuster, no waiting." | Same | No claim form |
| 4 | "A member picks a county and a cover. The quote shows how the price is built from our models." | `/enrol`, Harris County, $50 cover, quote breakdown visible | Harris County, $50 cover: $1.36 a month (`pricing_table.json`) |
| 5 | "They save their PayPal wallet once, with PayPal's vault, and the first premium is charged right away, with no buyer present." | PayPal sandbox approval, then the policy page with the premium charge and its PayPal capture ID | PayPal Vault + Orders: premium charged |
| 6 | "Then they leave a phone or laptop on a charger at their premises. That's the witness. It signs every entry and chains them, so a log can't be edited." | `/witness/...` page, register, signed entries appearing | Witness: signed, hash-chained log |

## Part 2 (1:00–2:00): the replay, the payout, the spoof

| # | Spoken (≈140 words) | On screen | Caption |
|---|---|---|---|
| 7 | "This is the real Houston derecho of May 2024, replayed from the Department of Energy's EAGLE-I outage data." | `/live`, start `derecho-harris-2024` | Real data: EAGLE-I, Harris County, May 2024 |
| 8 | "The county index climbs past the lower line, twenty percent of customers out, and peaks near twenty-seven percent." | Index chart crossing the dashed 20% line | `theta_lo` 0.20 (`trigger_params.json`); peak 27.5% |
| 9 | "At the premises, the charger goes dark." | Camera or screen: charger unplugged / "Power is out" pressed; witness entry appears in the feed | Witness: power_lost |
| 10 | "Four simulated hours later both signals agree. Rule R3 decides the claim, and a PayPal Payout goes out." | 4-hour clock completes; claim R3 appears; payout "sending" | R3: index ≥ 20% for 4 h + witness confirms |
| 11 | "Sending. Confirmed. Thirty dollars, sixty percent of the cover, in the member's PayPal account." | Payout flips to "confirmed"; open the claim page with batch and item IDs | $30.00 payout confirmed (simulated member) |
| 12 | "Now the fraud case: a quiet week, and someone unplugs a charger. The witness alone can never create a payout. It's flagged for review, and nothing is paid." | Run `spoof-quiet`; R5 claim, review, $0 | R5: witness without index, no payout |

## Part 3 (2:00–2:50): does it work, and what it can't do

| # | Spoken (≈130 words) | On screen | Caption |
|---|---|---|---|
| 13 | "The hard question is basis risk: a county number doesn't prove your fridge was off. The county can be dark while your street has power, or the other way round." | `/evidence`, basis-risk table with caveats beside it | Basis risk (`basis_risk.json`) |
| 14 | "We tested it on real premises-level supply data from India. Paying on the index alone missed seventy percent of real four-hour outages, and thirty-five percent of its payments went to premises that had power." | Highlight the index-only row | Index only: 70.4% missed, 35.3% wrong |
| 15 | "Adding the witness cut misses to six percent with no wrong payments, and to twenty-three and a half percent with thirty percent of witness logs missing." | Highlight the rules rows | With witness: 5.9% / 0.0%; 30% logs missing: 23.5% / 6.0% |
| 16 | "That's an upper bound: Indian data, eight districts with one dominating, and a perfect witness. An independent audit reproduced every number." | Caveats panel | Upper bound: India, 8 districts · audit in AUDIT.md |
| 17 | "We're honest about the rest. Our frequency model did not beat its baselines; events are too rare. In 2024 the backtest pool lost money, a loss ratio of 1.32, from two years of test data." | Backtest panel | Frequency: no gain over baselines; 2024 loss ratio 1.32 (`backtest.json`) |
| 18 | "FourHour is the pricing, trigger and claims engine a licensed carrier would run: a sandbox prototype, not an insurance offer." | Limitations section, then repo URL | github.com/doshikaran/FourHour · MIT |

Word count of the spoken column: about 390. Read at a calm pace; trim line 2 first if over time.
