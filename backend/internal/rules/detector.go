// Package rules holds the trigger logic of SPEC.md section 4 and
// CONTRACTS.md section 3, as pure code with no database, clock or network:
//
//   - RunTracker follows one threshold through a 15-minute series and opens,
//     qualifies and closes runs, bridging missing intervals of up to
//     gap_bridge_minutes (decision D1).
//   - Detector combines a theta_lo and a theta_hi tracker into outage events,
//     merging a qualifying run into the previous qualifying run's event under
//     the hours clause (event_merge_hours, CONTRACTS.md 1.5).
//   - Evaluate decides claims for one policy (rules R1 to R5).
//
// Thresholds and window lengths always come from trigger_params.json; this
// package has no default values for them.
package rules

import (
	"errors"
	"fmt"
	"time"
)

// Qualify4h is the 4-hour tier line (USDA guidance, SPEC.md section 1).
const Qualify4h = 4 * time.Hour

// Qualify24h is the 24-hour tier line.
const Qualify24h = 24 * time.Hour

// Step is the EAGLE-I interval length.
const Step = 15 * time.Minute

// Params are the trigger parameters from trigger_params.json.
type Params struct {
	ThetaHi     float64
	ThetaLo     float64
	GapBridge   time.Duration // gap_bridge_minutes
	MergeWindow time.Duration // event_merge_hours
	// DropoutMaxFraction is dropout_max_fraction: a reading below the
	// threshold that is at most this fraction of the run's last reading at
	// or above it is a partial-feed dropout, treated like a missing interval.
	DropoutMaxFraction float64
}

// Validate checks 0 < theta_lo < theta_hi <= 1 and non-negative windows.
func (p Params) Validate() error {
	if !(p.ThetaLo > 0 && p.ThetaLo < p.ThetaHi && p.ThetaHi <= 1) {
		return fmt.Errorf("rules: need 0 < theta_lo < theta_hi <= 1, got lo=%v hi=%v", p.ThetaLo, p.ThetaHi)
	}
	if p.GapBridge < 0 || p.MergeWindow < 0 {
		return errors.New("rules: gap_bridge_minutes and event_merge_hours must not be negative")
	}
	if p.DropoutMaxFraction < 0 || p.DropoutMaxFraction >= 1 {
		return fmt.Errorf("rules: dropout_max_fraction must be in [0, 1), got %v", p.DropoutMaxFraction)
	}
	return nil
}

// Run is one run at or above a threshold. Last is its last observed reading
// at or above the threshold; End is set when it closes and then equals Last
// (CONTRACTS.md section 3, run duration).
type Run struct {
	Start time.Time
	Last  time.Time
	End   *time.Time
}

// Duration is Last − Start, with bridged gaps counted inside.
func (r Run) Duration() time.Duration { return r.Last.Sub(r.Start) }

// Open reports whether the run has not closed.
func (r Run) Open() bool { return r.End == nil }

// Overlaps reports whether [start, end] overlaps the run, where an open run
// extends to now.
func (r Run) Overlaps(start, end, now time.Time) bool {
	rEnd := now
	if r.End != nil {
		rEnd = *r.End
	}
	return !start.After(rEnd) && !r.Start.After(end)
}

// RunTracker follows one threshold.
type RunTracker struct {
	theta   float64
	bridge  time.Duration
	dropout float64 // dropout_max_fraction
	cur     *Run
	lastVal float64 // share at cur.Last
}

// NewRunTracker returns a tracker for theta with the given gap bridge and
// dropout fraction.
func NewRunTracker(theta float64, bridge time.Duration, dropout float64) *RunTracker {
	return &RunTracker{theta: theta, bridge: bridge, dropout: dropout}
}

// RunChange is what one reading did to a tracker.
type RunChange struct {
	Opened bool
	Closed *Run // the run that closed, with End set
	// Extended is true when an open run observed a new reading at or above.
	Extended bool
}

// Current returns a copy of the open run, if any.
func (t *RunTracker) Current() (Run, bool) {
	if t.cur == nil {
		return Run{}, false
	}
	return *t.cur, true
}

// Observe feeds one reading. share is nil for a missing interval.
//   - A reading at or above theta opens a run or extends the open one.
//   - A reading below theta closes the open run; its end is its last
//     reading at or above.
//   - A missing interval never extends a run. It closes the run when the
//     time since the run's last reading at or above exceeds the bridge, so
//     up to bridge/15min consecutive missing intervals are bridged.
//   - A dropout (a reading below theta and at most dropout × the run's last
//     reading at or above) counts as missing (CONTRACTS.md 1.6). The run
//     stays open until a reading at or above arrives within the bridge, or
//     the bridge passes, or a reading below theta that is not a dropout
//     arrives; in the last two cases it ends at its last reading at or above.
func (t *RunTracker) Observe(at time.Time, share *float64) RunChange {
	var ch RunChange
	if share != nil && *share < t.theta && t.cur != nil && t.dropout > 0 && *share <= t.dropout*t.lastVal {
		share = nil // partial-feed dropout: treat as missing
	}
	if share == nil {
		if t.cur != nil && at.Sub(t.cur.Last) > t.bridge {
			ch.Closed = t.close()
		}
		return ch
	}
	if *share >= t.theta {
		t.lastVal = *share
		if t.cur == nil {
			t.cur = &Run{Start: at, Last: at}
			ch.Opened = true
			return ch
		}
		if at.Sub(t.cur.Last) > t.bridge+Step {
			// The series skipped rows instead of sending null points: the
			// unseen gap is longer than the bridge, so the run ended.
			ch.Closed = t.close()
			t.cur = &Run{Start: at, Last: at}
			ch.Opened = true
			return ch
		}
		t.cur.Last = at
		ch.Extended = true
		return ch
	}
	if t.cur != nil {
		ch.Closed = t.close()
	}
	return ch
}

func (t *RunTracker) close() *Run {
	r := *t.cur
	end := r.Last
	r.End = &end
	t.cur = nil
	return &r
}
