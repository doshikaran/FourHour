package artifacts

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fourhour/backend/internal/money"
	"fourhour/backend/internal/rules"
)

// enginePaid drives rules.Evaluate the way the replay does (the same
// schedule, repeated evaluations, a long outage confirmed by the witness,
// then the replay end) and returns, per tier the engine decided, the share
// of cover it pays at the full factor. A tier decided with two different
// shares, or decided but never paid in full, fails the test.
func enginePaid(t *testing.T, sched rules.Schedule) map[string]*big.Rat {
	t.Helper()
	const cover = int64(1_000_000_000_000) // large, so the floor to a cent loses nothing
	t0 := time.Date(2024, 5, 16, 0, 0, 0, 0, time.UTC)
	at := func(h float64) time.Time { return t0.Add(time.Duration(h * float64(time.Hour))) }
	closed := func(a, b float64) rules.Run { e := at(b); return rules.Run{Start: at(a), Last: at(b), End: &e} }
	open := func(a, b float64) rules.Run { return rules.Run{Start: at(a), Last: at(b)} }
	lost := func(id string, seq int64, h float64) rules.WitnessEntry {
		return rules.WitnessEntry{ID: id, Seq: seq, Kind: rules.KindPowerLost, At: at(h)}
	}
	restored := func(id string, seq int64, h float64) rules.WitnessEntry {
		return rules.WitnessEntry{ID: id, Seq: seq, Kind: rules.KindPowerRestored, At: at(h)}
	}

	shares := map[string]*big.Rat{}
	tiers := map[string]bool{}
	record := func(d rules.Decision, factor *big.Rat) {
		tiers[d.Tier] = true
		if d.Decision != rules.Paid {
			return
		}
		// share = amount / (cover × factor)
		s := new(big.Rat).SetFrac64(d.AmountCents, cover)
		s.Quo(s, factor)
		if prev, ok := shares[d.Tier]; ok && prev.Cmp(s) != 0 {
			t.Fatalf("tier %s paid at two shares: %s and %s", d.Tier, prev.RatString(), s.RatString())
		}
		shares[d.Tier] = s
	}

	// A 48-hour outage at both thresholds (R1), one at theta_lo only (R3),
	// each confirmed by a 48-hour witness interval, evaluated every 15
	// minutes while the runs grow, then after they close and the replay ends.
	type scen struct {
		name   string
		events func(now float64) []rules.Event
	}
	for _, sc := range []scen{
		{"hi", func(now float64) []rules.Event {
			end := min(now, 48)
			if now >= 48 {
				return []rules.Event{{ID: "evt_hi", LoRuns: []rules.Run{closed(0, 48)}, HiRuns: []rules.Run{closed(0.5, 47)}}}
			}
			return []rules.Event{{ID: "evt_hi", LoRuns: []rules.Run{open(0, end)}, HiRuns: []rules.Run{open(0.5, max(end, 0.5))}}}
		}},
		{"lo", func(now float64) []rules.Event {
			if now >= 48 {
				return []rules.Event{{ID: "evt_lo", LoRuns: []rules.Run{closed(0, 48)}}}
			}
			return []rules.Event{{ID: "evt_lo", LoRuns: []rules.Run{open(0, now)}}}
		}},
	} {
		decided := map[rules.Key]bool{}
		for q := 0; q <= 4*72; q++ {
			now := float64(q) / 4
			var entries []rules.WitnessEntry
			if now >= 0.25 {
				entries = append(entries, lost("w1", 1, 0.25))
			}
			if now >= 48 {
				entries = append(entries, restored("w2", 2, 48))
			}
			ds, err := rules.Evaluate(rules.Input{
				Now: at(now), ReplayEnded: q == 4*72, Events: sc.events(now), Schedule: sched,
				Policy:  rules.Policy{ID: "pol", CoverCents: cover, HasDevice: true, Entries: entries},
				Decided: func(k rules.Key) bool { return decided[k] },
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range ds {
				decided[d.Key()] = true
				factor := money.FactorFull
				if d.Rule == rules.R2 {
					factor = money.FactorHalf
				}
				record(d, factor)
			}
		}
		if len(decided) == 0 {
			t.Fatalf("scenario %s: the engine decided nothing; the parity probe is broken", sc.name)
		}
	}
	for tier := range tiers {
		if _, ok := shares[tier]; !ok {
			t.Fatalf("tier %s is decided but never paid in this probe", tier)
		}
	}
	return shares
}

// pricedShares reads pricing_table.payout_schedule exactly as written,
// keeping keys the typed loader ignores.
func pricedShares(t *testing.T, doc []byte) map[string]*big.Rat {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(doc, &top); err != nil {
		t.Fatal(err)
	}
	s, err := ScheduleShares(top["payout_schedule"])
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func pricingDoc(schedule string) string {
	return `{"model_version":"m","counties":{"48201":{"expected_events_per_year":0.25,"prob_premises_affected":0.4,"credibility_weight":0.1,"offered":true,"not_offered_reason":null}},` +
		`"rates":{"risk_margin_rate":0.3,"expense_load_cents":100},"payout_schedule":` + schedule + `,` + provJSON + `}`
}

// checkDir is the parity check the backend relies on: the schedule the
// replay pays from (LoadSchedule) against the schedule the premium is
// priced from (the file's payout_schedule, every key).
func checkDir(t *testing.T, dir string) error {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "pricing_table.json"))
	if err != nil {
		t.Fatal(err)
	}
	sched, src := LoadSchedule(dir)
	if !src.FromArtifact {
		return errors.New("the replay would not pay from pricing_table.json: " + src.Reason)
	}
	return CheckScheduleParity(pricedShares(t, b), enginePaid(t, sched))
}

// TestPricedScheduleEqualsPaidSchedule fails if the premium prices a
// schedule the rule engine does not pay: a t24 key, a different t4, or a
// paid tier missing from the schedule.
func TestPricedScheduleEqualsPaidSchedule(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pricing_table.json", pricingDoc(`{"t4":0.6}`))
	if err := checkDir(t, dir); err != nil {
		t.Fatalf("t4-only schedule: %v", err)
	}

	paid := enginePaid(t, rules.Schedule{T4: big.NewRat(6, 10)})
	if len(paid) != 1 || paid[rules.TierT4] == nil || paid[rules.TierT4].Cmp(big.NewRat(6, 10)) != 0 {
		t.Fatalf("engine pays %v, want exactly t4 = 3/5", paid)
	}
	cases := []struct {
		name, schedule, want string
	}{
		{"t24 priced", `{"t4":0.6,"t24":1.0}`, "tier t24 is priced but never paid"},
		{"different t4", `{"t4":0.5}`, "tier t4 is priced at 1/2 but paid at 3/5"},
		{"paid tier missing", `{}`, "tier t4 is paid but not priced"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckScheduleParity(pricedShares(t, []byte(pricingDoc(c.schedule))), paid)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}

	// The loader refuses a schedule that is not exactly the paid tiers, so
	// the replay falls back to SPEC.md and the quote cannot price it.
	for _, bad := range []string{`{"t4":0.6,"t24":1.0}`, `{}`} {
		write(t, dir, "pricing_table.json", pricingDoc(bad))
		if err := checkDir(t, dir); err == nil {
			t.Errorf("%s: parity check passed", bad)
		}
		if _, err := LoadPricingTable(dir); err == nil {
			t.Errorf("%s: pricing_table loaded", bad)
		}
	}
}

// TestRealPricingTableScheduleParity runs the same check on the real
// artifacts/pricing_table.json. It skips only when the file is absent.
func TestRealPricingTableScheduleParity(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "artifacts")
	if _, err := os.Stat(filepath.Join(dir, "pricing_table.json")); errors.Is(err, fs.ErrNotExist) {
		t.Skip("artifacts/pricing_table.json not present")
	}
	if err := checkDir(t, dir); err != nil {
		t.Fatalf("artifacts/pricing_table.json: %v", err)
	}
}
