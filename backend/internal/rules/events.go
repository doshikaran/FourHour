package rules

import (
	"time"
)

// Threshold names used on the event stream.
const (
	Lo = "lo"
	Hi = "hi"
)

// Event is one outage event: lo runs merged by the hours clause, and the hi
// runs inside them.
type Event struct {
	ID        string
	LoRuns    []Run
	HiRuns    []Run
	PeakShare float64
}

func (e *Event) clone() Event {
	c := *e
	c.LoRuns = append([]Run(nil), e.LoRuns...)
	c.HiRuns = append([]Run(nil), e.HiRuns...)
	return c
}

// LoStart is the first lo run's start.
func (e Event) LoStart() time.Time { return e.LoRuns[0].Start }

// LoEnd is the last lo run's end, or nil while it is open.
func (e Event) LoEnd() *time.Time { return e.LoRuns[len(e.LoRuns)-1].End }

// LongestHi returns the longest hi run (the first of equals), if any.
func (e Event) LongestHi() (Run, bool) {
	var best Run
	found := false
	for _, r := range e.HiRuns {
		if !found || r.Duration() > best.Duration() {
			best, found = r, true
		}
	}
	return best, found
}

// Window is [first lo start, last lo end], with an open end at now.
func (e Event) Window(now time.Time) (time.Time, time.Time) {
	end := now
	if le := e.LoEnd(); le != nil {
		end = *le
	}
	return e.LoStart(), end
}

// Signal is something the stream reports (CONTRACTS.md section 5).
type Signal struct {
	Kind          string // "opened", "closed" or "merged"
	EventID       string
	IntoEventID   string // "merged" only: the event the run joined
	Threshold     string // Lo or Hi
	DurationHours float64
}

// StepResult is what one reading changed.
type StepResult struct {
	Signals []Signal
	// Changed lists events whose stored row must be written.
	Changed []string
	// Deleted lists provisional events abandoned by an hours-clause merge.
	Deleted []string
}

type lastQualifying struct {
	event *Event
	end   time.Time
}

// Detector builds outage events for one county from a 15-minute series.
// It is not safe for concurrent use; the replay engine owns one per replay.
type Detector struct {
	p     Params
	lo    *RunTracker
	hi    *RunTracker
	newID func() string

	events  []*Event
	current *Event // the event holding the open lo run
	curQual bool   // the open lo run has reached 4 hours
	lastQ   *lastQualifying
}

// NewDetector returns a Detector. newID makes outage event IDs.
func NewDetector(p Params, newID func() string) *Detector {
	return &Detector{p: p, lo: NewRunTracker(p.ThetaLo, p.GapBridge, p.DropoutMaxFraction), hi: NewRunTracker(p.ThetaHi, p.GapBridge, p.DropoutMaxFraction), newID: newID}
}

// Events returns copies of every live event, oldest first.
func (d *Detector) Events() []Event {
	out := make([]Event, 0, len(d.events))
	for _, e := range d.events {
		out = append(out, e.clone())
	}
	return out
}

// Event returns a copy of one event.
func (d *Detector) Event(id string) (Event, bool) {
	for _, e := range d.events {
		if e.ID == id {
			return e.clone(), true
		}
	}
	return Event{}, false
}

func hours(dur time.Duration) float64 { return dur.Hours() }

// Observe feeds one reading; share is nil for a missing interval.
func (d *Detector) Observe(at time.Time, share *float64) StepResult {
	var res StepResult
	changed := map[string]bool{}
	mark := func(e *Event) {
		if e != nil && !changed[e.ID] {
			changed[e.ID] = true
			res.Changed = append(res.Changed, e.ID)
		}
	}
	hc := d.hi.Observe(at, share)
	lc := d.lo.Observe(at, share)

	// Closes first: hi before lo, so a hi run is attributed to its event.
	if hc.Closed != nil && d.current != nil {
		e := d.current
		e.HiRuns[len(e.HiRuns)-1] = *hc.Closed
		res.Signals = append(res.Signals, Signal{Kind: "closed", EventID: e.ID, Threshold: Hi, DurationHours: hours(hc.Closed.Duration())})
		mark(e)
	}
	if lc.Closed != nil && d.current != nil {
		e := d.current
		e.LoRuns[len(e.LoRuns)-1] = *lc.Closed
		if d.curQual {
			d.lastQ = &lastQualifying{event: e, end: *lc.Closed.End}
		}
		res.Signals = append(res.Signals, Signal{Kind: "closed", EventID: e.ID, Threshold: Lo, DurationHours: hours(lc.Closed.Duration())})
		mark(e)
		d.current, d.curQual = nil, false
	}
	// Opens.
	if lc.Opened {
		r, _ := d.lo.Current()
		e := &Event{ID: d.newID(), LoRuns: []Run{r}}
		d.events = append(d.events, e)
		d.current, d.curQual = e, false
		res.Signals = append(res.Signals, Signal{Kind: "opened", EventID: e.ID, Threshold: Lo})
		mark(e)
	} else if lc.Extended && d.current != nil {
		r, _ := d.lo.Current()
		d.current.LoRuns[len(d.current.LoRuns)-1] = r
		mark(d.current)
	}
	if d.current != nil {
		if hc.Opened {
			r, _ := d.hi.Current()
			d.current.HiRuns = append(d.current.HiRuns, r)
			res.Signals = append(res.Signals, Signal{Kind: "opened", EventID: d.current.ID, Threshold: Hi})
			mark(d.current)
		} else if hc.Extended {
			r, _ := d.hi.Current()
			d.current.HiRuns[len(d.current.HiRuns)-1] = r
			mark(d.current)
		}
		if share != nil && *share > d.current.PeakShare {
			d.current.PeakShare = *share
			mark(d.current)
		}
	}

	// Qualification and the hours clause (CONTRACTS.md 1.5, section 3).
	if d.current != nil && !d.curQual {
		run := d.current.LoRuns[len(d.current.LoRuns)-1]
		if run.Duration() >= Qualify4h {
			d.curQual = true
			if d.lastQ != nil && d.lastQ.event != d.current && run.Start.Sub(d.lastQ.end) <= d.p.MergeWindow {
				d.merge(d.current, d.lastQ.event, &res)
				mark(d.current)
			}
		}
	}
	return res
}

// merge moves the provisional event's runs into the earlier event and drops
// the provisional one.
func (d *Detector) merge(prov, into *Event, res *StepResult) {
	into.LoRuns = append(into.LoRuns, prov.LoRuns...)
	hiOpen := len(prov.HiRuns) > 0 && prov.HiRuns[len(prov.HiRuns)-1].Open()
	into.HiRuns = append(into.HiRuns, prov.HiRuns...)
	if prov.PeakShare > into.PeakShare {
		into.PeakShare = prov.PeakShare
	}
	for i, e := range d.events {
		if e == prov {
			d.events = append(d.events[:i], d.events[i+1:]...)
			break
		}
	}
	// Drop the provisional ID from Changed: its row is deleted, not written.
	kept := res.Changed[:0]
	for _, id := range res.Changed {
		if id != prov.ID {
			kept = append(kept, id)
		}
	}
	res.Changed = kept
	res.Deleted = append(res.Deleted, prov.ID)
	d.current = into
	res.Signals = append(res.Signals, Signal{Kind: "merged", EventID: prov.ID, IntoEventID: into.ID})
	res.Signals = append(res.Signals, Signal{Kind: "opened", EventID: into.ID, Threshold: Lo})
	if hiOpen {
		res.Signals = append(res.Signals, Signal{Kind: "opened", EventID: into.ID, Threshold: Hi})
	}
}
