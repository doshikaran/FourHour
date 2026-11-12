package rules

import (
	"fmt"
	"math/big"
	"sort"
	"time"

	"fourhour/backend/internal/money"
)

// Rule, tier and decision values (CONTRACTS.md section 3).
const (
	R1 = "R1"
	R2 = "R2"
	R3 = "R3"
	R4 = "R4"
	R5 = "R5"

	TierT4  = "t4"
	TierT24 = "t24"

	Paid     = "paid"
	Declined = "declined"
	Review   = "review"
)

// Witness entry kinds that open and close an outage interval. The attest_*
// kinds count the same (CONTRACTS.md section 6). Heartbeats never do (D8).
const (
	KindHeartbeat      = "heartbeat"
	KindPowerLost      = "power_lost"
	KindPowerRestored  = "power_restored"
	KindAttestLost     = "attest_lost"
	KindAttestRestored = "attest_restored"
)

// IsLost reports whether kind starts a witness outage interval.
func IsLost(kind string) bool { return kind == KindPowerLost || kind == KindAttestLost }

// IsRestored reports whether kind ends a witness outage interval.
func IsRestored(kind string) bool { return kind == KindPowerRestored || kind == KindAttestRestored }

// WitnessEntry is a valid entry as the rules see it: its kind and the time
// it counts at (sim_time during a replay).
type WitnessEntry struct {
	ID   string
	Seq  int64
	Kind string
	At   time.Time
}

// Interval is a witness outage interval: from a lost entry to the next
// restored entry. End is nil while power is still out.
type Interval struct {
	StartEntryID string
	Start        time.Time
	End          *time.Time
}

// Duration until End, or until now while open.
func (i Interval) Duration(now time.Time) time.Duration {
	if i.End != nil {
		return i.End.Sub(i.Start)
	}
	return now.Sub(i.Start)
}

func (i Interval) end(now time.Time) time.Time {
	if i.End != nil {
		return *i.End
	}
	return now
}

// Intervals builds outage intervals from valid entries in seq order. A lost
// entry while power is already out, and a restored entry while it is on,
// change nothing.
func Intervals(entries []WitnessEntry) []Interval {
	es := append([]WitnessEntry(nil), entries...)
	sort.SliceStable(es, func(a, b int) bool { return es[a].Seq < es[b].Seq })
	var out []Interval
	var cur *Interval
	for _, e := range es {
		switch {
		case IsLost(e.Kind) && cur == nil:
			cur = &Interval{StartEntryID: e.ID, Start: e.At}
		case IsRestored(e.Kind) && cur != nil:
			end := e.At
			cur.End = &end
			out = append(out, *cur)
			cur = nil
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

// Policy is what the rules need about one policy.
type Policy struct {
	ID         string
	CoverCents int64
	HasDevice  bool
	// Entries are the device's valid entries that count for this replay.
	Entries []WitnessEntry
}

// Schedule is the payout schedule from pricing_table.json.
type Schedule struct {
	T4  *big.Rat
	T24 *big.Rat
}

// Key identifies a claim for the uniqueness rules of CONTRACTS.md section 3.
type Key struct {
	EventID        string
	Tier           string
	WitnessEntryID string // R5 only
}

// Input is one evaluation of one policy.
type Input struct {
	Now         time.Time
	ReplayEnded bool
	Events      []Event
	Policy      Policy
	Schedule    Schedule
	// Decided reports whether a claim with this key already exists.
	Decided func(Key) bool
}

// Decision is a claim to record.
type Decision struct {
	Rule           string
	Tier           string
	Decision       string
	EventID        string // empty for R5
	WitnessEntryID string // R5 only
	AmountCents    int64
	Reason         string
}

// Key returns the decision's uniqueness key.
func (d Decision) Key() Key {
	return Key{EventID: d.EventID, Tier: d.Tier, WitnessEntryID: d.WitnessEntryID}
}

func qualifying(runs []Run, min time.Duration) []Run {
	var out []Run
	for _, r := range runs {
		if r.Duration() >= min {
			out = append(out, r)
		}
	}
	return out
}

// confirms reports whether some interval of 4h or more overlaps some run.
func confirms(ivs []Interval, runs []Run, now time.Time) bool {
	for _, iv := range ivs {
		if iv.Duration(now) < Qualify4h {
			continue
		}
		for _, r := range runs {
			if r.Overlaps(iv.Start, iv.end(now), now) {
				return true
			}
		}
	}
	return false
}

func anyClosed(runs []Run) bool {
	for _, r := range runs {
		if !r.Open() {
			return true
		}
	}
	return false
}

func entriesIn(entries []WitnessEntry, start, end time.Time) bool {
	for _, e := range entries {
		if !e.At.Before(start) && !e.At.After(end) {
			return true
		}
	}
	return false
}

// Evaluate returns the claims to record now for one policy, 4-hour tier and
// R5 (CONTRACTS.md section 3, "When rules are decided"):
//   - No device: R2 when a theta_hi run reaches 4 hours.
//   - Device: R1 (theta_hi run) or R3 (theta_lo run only) as soon as a
//     witness interval of 4 hours or more overlaps a qualifying run.
//     Otherwise, once a qualifying theta_hi run closes or the replay ends:
//     R4 if the device has valid entries inside the event window, else R2.
//   - R5 when a witness interval reaches 4 hours with no theta_lo run
//     overlapping it.
//
// The witness can confirm or contradict an index event; it never creates a
// payment on its own.
func Evaluate(in Input) ([]Decision, error) {
	var out []Decision
	ivs := Intervals(in.Policy.Entries)
	full := func(rule, eventID, reason string, factor *big.Rat) (Decision, error) {
		amt, err := money.PayoutCents(in.Policy.CoverCents, in.Schedule.T4, factor)
		if err != nil {
			return Decision{}, fmt.Errorf("rules: payout for %s: %w", in.Policy.ID, err)
		}
		return Decision{Rule: rule, Tier: TierT4, Decision: Paid, EventID: eventID, AmountCents: amt, Reason: reason}, nil
	}
	for _, ev := range in.Events {
		if in.Decided(Key{EventID: ev.ID, Tier: TierT4}) {
			continue
		}
		hiQ := qualifying(ev.HiRuns, Qualify4h)
		loQ := qualifying(ev.LoRuns, Qualify4h)
		var d *Decision
		switch {
		case len(hiQ) > 0 && !in.Policy.HasDevice:
			x, err := full(R2, ev.ID, "The county outage index stayed at or above the strong threshold for 4 hours or more and no witness device is registered, so 50% of the schedule is paid.", money.FactorHalf)
			if err != nil {
				return nil, err
			}
			d = &x
		case len(hiQ) > 0 && confirms(ivs, hiQ, in.Now):
			x, err := full(R1, ev.ID, "The county outage index stayed at or above the strong threshold for 4 hours or more and the member's witness confirmed 4 hours or more without power.", money.FactorFull)
			if err != nil {
				return nil, err
			}
			d = &x
		case len(hiQ) > 0 && (anyClosed(hiQ) || in.ReplayEnded):
			start, end := ev.Window(in.Now)
			if entriesIn(in.Policy.Entries, start, end) {
				d = &Decision{Rule: R4, Tier: TierT4, Decision: Declined, EventID: ev.ID, Reason: "The county outage index stayed at or above the strong threshold for 4 hours or more, but the member's witness log does not show 4 hours without power, so no payout is made."}
			} else {
				x, err := full(R2, ev.ID, "The county outage index stayed at or above the strong threshold for 4 hours or more and no witness log was received for the event, so 50% of the schedule is paid.", money.FactorHalf)
				if err != nil {
					return nil, err
				}
				d = &x
			}
		case len(hiQ) == 0 && len(loQ) > 0 && in.Policy.HasDevice && confirms(ivs, loQ, in.Now):
			x, err := full(R3, ev.ID, "The county outage index stayed at or above the lenient threshold for 4 hours or more and the member's witness confirmed 4 hours or more without power.", money.FactorFull)
			if err != nil {
				return nil, err
			}
			d = &x
		}
		if d != nil {
			out = append(out, *d)
		}
	}
	// R5: a 4-hour witness interval with no theta_lo run overlapping it.
	for _, iv := range ivs {
		if iv.Duration(in.Now) < Qualify4h || in.Decided(Key{Tier: TierT4, WitnessEntryID: iv.StartEntryID}) {
			continue
		}
		overlapped := false
		for _, ev := range in.Events {
			for _, r := range ev.LoRuns {
				if r.Overlaps(iv.Start, iv.end(in.Now), in.Now) {
					overlapped = true
				}
			}
		}
		if !overlapped {
			out = append(out, Decision{Rule: R5, Tier: TierT4, Decision: Review, WitnessEntryID: iv.StartEntryID, Reason: "The member's witness reported 4 hours or more without power while the county outage index stayed below the lenient threshold, so the claim is flagged for review and nothing is paid."})
		}
	}
	return out, nil
}
