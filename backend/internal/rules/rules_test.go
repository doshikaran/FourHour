package rules

import (
	"fmt"
	"math/big"
	"testing"
	"time"
)

// Test parameters. These are test inputs, not trigger values: production
// values always come from trigger_params.json.
var tp = Params{ThetaHi: 0.05, ThetaLo: 0.01, GapBridge: 120 * time.Minute, MergeWindow: 72 * time.Hour, DropoutMaxFraction: 0.1}

var t0 = time.Date(2024, 5, 16, 20, 0, 0, 0, time.UTC)

func f(v float64) *float64 { return &v }

// series builds readings at 15-minute steps from t0; NaN-free: nil = missing.
func at(i int) time.Time { return t0.Add(time.Duration(i) * Step) }

type feed struct {
	d   *Detector
	i   int
	all []StepResult
}

func newFeed() *feed {
	n := 0
	return &feed{d: NewDetector(tp, func() string { n++; return fmt.Sprintf("evt_%d", n) })}
}

// push sends k readings of v (nil = missing).
func (fd *feed) push(k int, v *float64) {
	for j := 0; j < k; j++ {
		fd.all = append(fd.all, fd.d.Observe(at(fd.i), v))
		fd.i++
	}
}

func (fd *feed) signals() []Signal {
	var out []Signal
	for _, r := range fd.all {
		out = append(out, r.Signals...)
	}
	return out
}

func TestParamsValidate(t *testing.T) {
	if err := tp.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []Params{{ThetaHi: 0.01, ThetaLo: 0.05}, {ThetaHi: 0.05, ThetaLo: 0}, {ThetaHi: 1.5, ThetaLo: 0.1}, {ThetaHi: 0.05, ThetaLo: 0.01, GapBridge: -1}} {
		if p.Validate() == nil {
			t.Errorf("%+v: want error", p)
		}
	}
}

func TestRunQualifiesAtFourHoursAfterFirstReading(t *testing.T) {
	fd := newFeed()
	fd.push(16, f(0.1)) // readings 0..15: last - first = 3h45
	evs := fd.d.Events()
	if len(evs) != 1 || evs[0].LoRuns[0].Duration() != 3*time.Hour+45*time.Minute {
		t.Fatalf("%+v", evs)
	}
	fd.push(1, f(0.1)) // reading 16: exactly 4h
	ev, _ := fd.d.Event("evt_1")
	if ev.LoRuns[0].Duration() != Qualify4h || ev.HiRuns[0].Duration() != Qualify4h {
		t.Fatalf("not 4h: %+v", ev)
	}
	if ev.LoEnd() != nil {
		t.Fatal("run should be open")
	}
}

func TestGapBridgeUpTo120Minutes(t *testing.T) {
	fd := newFeed()
	fd.push(4, f(0.1))
	fd.push(8, nil) // 8 missing intervals = 120 minutes: bridged
	fd.push(5, f(0.1))
	evs := fd.d.Events()
	if len(evs) != 1 || len(evs[0].LoRuns) != 1 || !evs[0].LoRuns[0].Open() {
		t.Fatalf("gap of 120 min must not end the event: %+v", evs)
	}
	// first reading 0, last reading 16 -> 4h with the bridged gap counted.
	if d := evs[0].LoRuns[0].Duration(); d != 4*time.Hour {
		t.Fatalf("duration %v", d)
	}
}

func TestGapLongerThanBridgeEndsRunAtLastReading(t *testing.T) {
	fd := newFeed()
	fd.push(4, f(0.1)) // 0..3
	fd.push(9, nil)    // 135 minutes missing: ends
	fd.push(2, f(0.1))
	evs := fd.d.Events()
	if len(evs) != 2 {
		t.Fatalf("want 2 events (first not qualifying, so not merged), got %d", len(evs))
	}
	if e := evs[0].LoEnd(); e == nil || !e.Equal(at(3)) {
		t.Fatalf("end should be last reading at or above: %v", e)
	}
}

func TestSkippedRowsLongerThanBridgeEndRun(t *testing.T) {
	n := 0
	d := NewDetector(tp, func() string { n++; return fmt.Sprintf("e%d", n) })
	d.Observe(at(0), f(0.1))
	d.Observe(at(1), f(0.1))
	d.Observe(at(1).Add(3*time.Hour), f(0.1)) // rows absent, not null
	if evs := d.Events(); len(evs) != 2 || !evs[0].LoEnd().Equal(at(1)) {
		t.Fatalf("%+v", evs)
	}
}

func TestBelowReadingClosesRun(t *testing.T) {
	fd := newFeed()
	fd.push(3, f(0.1))
	fd.push(1, f(0.03)) // below hi, above lo: hi closes, lo stays
	ev, _ := fd.d.Event("evt_1")
	if ev.HiRuns[0].Open() || !ev.LoRuns[0].Open() {
		t.Fatalf("%+v", ev)
	}
	fd.push(1, f(0.005)) // below lo and above 10% of 0.03: not a dropout
	ev, _ = fd.d.Event("evt_1")
	if ev.LoEnd() == nil || !ev.LoEnd().Equal(at(3)) {
		t.Fatalf("lo end %v", ev.LoEnd())
	}
	sig := fd.signals()
	kinds := ""
	for _, s := range sig {
		kinds += s.Kind + ":" + s.Threshold + " "
	}
	if kinds != "opened:lo opened:hi closed:hi closed:lo " {
		t.Fatalf("signals %q", kinds)
	}
}

func TestHoursClauseMergesOnlyQualifyingRuns(t *testing.T) {
	fd := newFeed()
	fd.push(17, f(0.1)) // run A qualifies (4h)
	fd.push(12, f(0))   // dropouts until the 2h bridge passes, then closed
	fd.push(4, f(0.1))  // run B: provisional, 45 min, not qualifying
	fd.push(12, f(0))
	if evs := fd.d.Events(); len(evs) != 2 {
		t.Fatalf("a non-qualifying run is its own event, got %d events", len(evs))
	}
	fd.push(17, f(0.1)) // run C qualifies and is within 72h of A's end
	evs := fd.d.Events()
	if len(evs) != 2 || evs[0].ID != "evt_1" || len(evs[0].LoRuns) != 2 || evs[1].ID != "evt_2" {
		t.Fatalf("run C must join evt_1, B stays alone: %+v", evs)
	}
	if evs[0].LoEnd() != nil {
		t.Fatal("merged event end must be null while the new run is open")
	}
	// Signals at C's qualification: merged evt_3 -> evt_1, then opened lo and hi for evt_1.
	last := fd.all[len(fd.all)-1]
	if len(last.Signals) != 3 || last.Signals[0].Kind != "merged" || last.Signals[0].EventID != "evt_3" || last.Signals[0].IntoEventID != "evt_1" ||
		last.Signals[1].Kind != "opened" || last.Signals[1].EventID != "evt_1" || last.Signals[1].Threshold != Lo || last.Signals[2].Threshold != Hi {
		t.Fatalf("signals %+v", last.Signals)
	}
	if len(last.Deleted) != 1 || last.Deleted[0] != "evt_3" {
		t.Fatalf("deleted %v", last.Deleted)
	}
	for _, id := range last.Changed {
		if id == "evt_3" {
			t.Fatal("deleted event must not be written")
		}
	}
	// Durations are per run, never summed.
	if d := evs[0].LoRuns[1].Duration(); d != 4*time.Hour {
		t.Fatalf("run C duration %v", d)
	}
}

func TestHoursClauseDoesNotMergeAfterWindow(t *testing.T) {
	p := tp
	p.MergeWindow = time.Hour
	n := 0
	d := NewDetector(p, func() string { n++; return fmt.Sprintf("e%d", n) })
	i := 0
	push := func(k int, v *float64) {
		for j := 0; j < k; j++ {
			d.Observe(at(i), v)
			i++
		}
	}
	push(17, f(0.1))
	push(12, f(0)) // closes after the bridge; quiet longer than the 1h window
	push(17, f(0.1))
	if evs := d.Events(); len(evs) != 2 {
		t.Fatalf("want 2 events, got %d", len(evs))
	}
}

// --- Evaluate -------------------------------------------------------------

var sched = Schedule{T4: big.NewRat(6, 10), T24: big.NewRat(1, 1)}

func runAt(i, j int, open bool) Run {
	r := Run{Start: at(i), Last: at(j)}
	if !open {
		e := at(j)
		r.End = &e
	}
	return r
}

func noneDecided(Key) bool { return false }

func eval(t *testing.T, in Input) []Decision {
	t.Helper()
	if in.Decided == nil {
		in.Decided = noneDecided
	}
	if in.Schedule.T4 == nil {
		in.Schedule = sched
	}
	ds, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func hiEvent(open bool) Event {
	return Event{ID: "evt_a", LoRuns: []Run{runAt(0, 20, open)}, HiRuns: []Run{runAt(1, 17, open)}}
}

func lost(id string, seq int64, i int) WitnessEntry {
	return WitnessEntry{ID: id, Seq: seq, Kind: KindPowerLost, At: at(i)}
}
func restored(id string, seq int64, i int) WitnessEntry {
	return WitnessEntry{ID: id, Seq: seq, Kind: KindPowerRestored, At: at(i)}
}

func TestR2NoDevice(t *testing.T) {
	ds := eval(t, Input{Now: at(17), Events: []Event{hiEvent(true)}, Policy: Policy{ID: "p", CoverCents: 100}})
	if len(ds) != 1 || ds[0].Rule != R2 || ds[0].AmountCents != 30 || ds[0].Decision != Paid || ds[0].Tier != TierT4 {
		t.Fatalf("%+v", ds)
	}
	// Under 4h: nothing.
	short := Event{ID: "e", LoRuns: []Run{runAt(0, 15, true)}, HiRuns: []Run{runAt(0, 15, true)}}
	if ds := eval(t, Input{Now: at(15), Events: []Event{short}, Policy: Policy{ID: "p", CoverCents: 100}}); len(ds) != 0 {
		t.Fatalf("%+v", ds)
	}
}

func TestR1WitnessConfirms(t *testing.T) {
	pol := Policy{ID: "p", CoverCents: 15000, HasDevice: true, Entries: []WitnessEntry{lost("w1", 1, 2), restored("w2", 2, 19)}}
	ds := eval(t, Input{Now: at(20), Events: []Event{hiEvent(true)}, Policy: pol})
	if len(ds) != 1 || ds[0].Rule != R1 || ds[0].AmountCents != 9000 {
		t.Fatalf("%+v", ds)
	}
	// Open witness interval reaching 4h also confirms (attest kinds count).
	pol.Entries = []WitnessEntry{{ID: "w1", Seq: 1, Kind: KindAttestLost, At: at(2)}}
	ds = eval(t, Input{Now: at(18), Events: []Event{hiEvent(true)}, Policy: pol})
	if len(ds) != 1 || ds[0].Rule != R1 {
		t.Fatalf("%+v", ds)
	}
}

func TestDeviceWaitsThenR4OrR2(t *testing.T) {
	// Witness out for only 1h: no decision while the hi run is open.
	pol := Policy{ID: "p", CoverCents: 15000, HasDevice: true, Entries: []WitnessEntry{lost("w1", 1, 2), restored("w2", 2, 6)}}
	if ds := eval(t, Input{Now: at(18), Events: []Event{hiEvent(true)}, Policy: pol}); len(ds) != 0 {
		t.Fatalf("must wait: %+v", ds)
	}
	ds := eval(t, Input{Now: at(21), Events: []Event{hiEvent(false)}, Policy: pol})
	if len(ds) != 1 || ds[0].Rule != R4 || ds[0].AmountCents != 0 || ds[0].Decision != Declined {
		t.Fatalf("R4: %+v", ds)
	}
	// Device registered but no entries inside the event window: R2.
	pol.Entries = []WitnessEntry{{ID: "h", Seq: 1, Kind: KindHeartbeat, At: at(-40)}}
	ds = eval(t, Input{Now: at(21), Events: []Event{hiEvent(false)}, Policy: pol})
	if len(ds) != 1 || ds[0].Rule != R2 || ds[0].AmountCents != 4500 {
		t.Fatalf("R2 (no log): %+v", ds)
	}
	// Replay end also decides.
	pol.Entries = []WitnessEntry{{ID: "h", Seq: 1, Kind: KindHeartbeat, At: at(5)}}
	ds = eval(t, Input{Now: at(18), ReplayEnded: true, Events: []Event{hiEvent(true)}, Policy: pol})
	if len(ds) != 1 || ds[0].Rule != R4 {
		t.Fatalf("R4 at replay end: %+v", ds)
	}
}

func TestR3LenientIndexWithWitness(t *testing.T) {
	ev := Event{ID: "evt_l", LoRuns: []Run{runAt(0, 18, true)}} // never at theta_hi
	pol := Policy{ID: "p", CoverCents: 5000, HasDevice: true, Entries: []WitnessEntry{lost("w1", 1, 1)}}
	ds := eval(t, Input{Now: at(18), Events: []Event{ev}, Policy: pol})
	if len(ds) != 1 || ds[0].Rule != R3 || ds[0].AmountCents != 3000 {
		t.Fatalf("%+v", ds)
	}
	// Without a device, a theta_lo event pays nothing.
	if ds := eval(t, Input{Now: at(18), Events: []Event{ev}, Policy: Policy{ID: "p", CoverCents: 5000}}); len(ds) != 0 {
		t.Fatalf("%+v", ds)
	}
}

func TestR5WitnessWithoutIndex(t *testing.T) {
	pol := Policy{ID: "p", CoverCents: 5000, HasDevice: true, Entries: []WitnessEntry{{ID: "w9", Seq: 3, Kind: KindAttestLost, At: at(0)}}}
	if ds := eval(t, Input{Now: at(15), Policy: pol}); len(ds) != 0 {
		t.Fatalf("under 4h: %+v", ds)
	}
	ds := eval(t, Input{Now: at(16), Policy: pol})
	if len(ds) != 1 || ds[0].Rule != R5 || ds[0].Decision != Review || ds[0].AmountCents != 0 || ds[0].WitnessEntryID != "w9" || ds[0].EventID != "" {
		t.Fatalf("%+v", ds)
	}
	// An overlapping theta_lo run (even one that never qualifies) blocks R5.
	ev := Event{ID: "e", LoRuns: []Run{runAt(10, 12, false)}}
	if ds := eval(t, Input{Now: at(16), Events: []Event{ev}, Policy: pol}); len(ds) != 0 {
		t.Fatalf("%+v", ds)
	}
	// Decided already: nothing.
	ds = eval(t, Input{Now: at(16), Policy: pol, Decided: func(k Key) bool { return k.WitnessEntryID == "w9" }})
	if len(ds) != 0 {
		t.Fatalf("%+v", ds)
	}
}

func TestHeartbeatsNeverMakeAnInterval(t *testing.T) {
	es := []WitnessEntry{{ID: "a", Seq: 1, Kind: KindHeartbeat, At: at(0)}, {ID: "b", Seq: 2, Kind: KindHeartbeat, At: at(40)}}
	if ivs := Intervals(es); len(ivs) != 0 {
		t.Fatalf("%+v", ivs)
	}
	es = []WitnessEntry{lost("a", 1, 0), lost("b", 2, 4), restored("c", 3, 8), restored("d", 4, 9), lost("e", 5, 10)}
	ivs := Intervals(es)
	if len(ivs) != 2 || ivs[0].StartEntryID != "a" || !ivs[0].End.Equal(at(8)) || ivs[1].End != nil {
		t.Fatalf("%+v", ivs)
	}
}

// One outage event can never produce two payouts for the same tier: once a
// claim exists for (event, t4), Evaluate returns nothing for it, and a
// second qualifying run merged into the same event is the same event.
func TestOneEventOnePayoutPerTier(t *testing.T) {
	fd := newFeed()
	decided := map[Key]bool{}
	var paid []Decision
	run := func() {
		for _, d := range eval(t, Input{Now: at(fd.i - 1), Events: fd.d.Events(), Policy: Policy{ID: "p", CoverCents: 100}, Decided: func(k Key) bool { return decided[k] }}) {
			if decided[d.Key()] {
				t.Fatalf("duplicate %+v", d)
			}
			decided[d.Key()] = true
			paid = append(paid, d)
		}
	}
	for k := 0; k < 20; k++ {
		fd.push(1, f(0.1))
		run()
	}
	for k := 0; k < 12; k++ {
		fd.push(1, f(0))
		run()
	}
	for k := 0; k < 20; k++ { // second qualifying run within 72h: merged
		fd.push(1, f(0.1))
		run()
	}
	if len(paid) != 1 || paid[0].Rule != R2 || paid[0].EventID != "evt_1" {
		t.Fatalf("want exactly one t4 claim, got %+v", paid)
	}
}

func TestDropoutBridgedWhenRunResumes(t *testing.T) {
	fd := newFeed()
	fd.push(6, f(0.3))
	fd.push(3, f(0.02)) // <= 10% of 0.3 and below hi: hi dropout; >= lo: lo continues
	fd.push(3, f(0.0))  // dropout for both
	fd.push(8, f(0.3))  // resumes within 120 min of the last reading at or above
	evs := fd.d.Events()
	if len(evs) != 1 || len(evs[0].HiRuns) != 1 || !evs[0].HiRuns[0].Open() {
		t.Fatalf("dropout must not end the run: %+v", evs)
	}
	if d := evs[0].HiRuns[0].Duration(); d != 19*Step {
		t.Fatalf("duration %v", d)
	}
}

func TestDropoutEndsRunWhenBridgePasses(t *testing.T) {
	fd := newFeed()
	fd.push(6, f(0.3))
	fd.push(8, f(0.0)) // 120 min of dropouts: still open (decided later)
	if ev, _ := fd.d.Event("evt_1"); ev.LoEnd() != nil {
		t.Fatal("must stay open while the window has not passed")
	}
	fd.push(1, f(0.0)) // 135 min: window passed
	ev, _ := fd.d.Event("evt_1")
	if ev.LoEnd() == nil || !ev.LoEnd().Equal(at(5)) || ev.HiRuns[0].End == nil || !ev.HiRuns[0].End.Equal(at(5)) {
		t.Fatalf("end must be the last reading at or above: %+v", ev)
	}
}

func TestBelowButNotDropoutEndsRun(t *testing.T) {
	fd := newFeed()
	fd.push(6, f(0.06))
	fd.push(1, f(0.008)) // below lo 0.01, above 10% of 0.06
	ev, _ := fd.d.Event("evt_1")
	if ev.LoEnd() == nil || !ev.LoEnd().Equal(at(5)) {
		t.Fatalf("%+v", ev)
	}
	// A dropout followed by a non-dropout below reading also ends it.
	fd2 := newFeed()
	fd2.push(6, f(0.3))
	fd2.push(2, f(0.0))
	fd2.push(1, f(0.04)) // below hi 0.05, above 10% of 0.3: hi ends; lo continues
	ev, _ = fd2.d.Event("evt_1")
	if ev.HiRuns[0].End == nil || !ev.HiRuns[0].End.Equal(at(5)) || !ev.LoRuns[0].Open() {
		t.Fatalf("%+v", ev)
	}
}

func TestDropoutFractionZeroIsOff(t *testing.T) {
	p := tp
	p.DropoutMaxFraction = 0
	d := NewDetector(p, func() string { return "e" })
	d.Observe(at(0), f(0.3))
	d.Observe(at(1), f(0))
	if ev, _ := d.Event("e"); ev.LoEnd() == nil {
		t.Fatal("with fraction 0 a zero reading ends the run")
	}
}
