package artifacts

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fourhour/backend/internal/rules"
)

// TestParityDerechoHarris2024 (B3) feeds the real scenario file through the
// rules detector with the real trigger_params.json and checks the answer the
// ML pipeline gives (ml/fourhour_ml/build.py build_scenarios): one theta_lo
// run from 2024-05-17T00:15:00Z to a last reading at or above at
// 2024-05-17T19:30:00Z (19.25 h) with 4 dropout readings treated as missing,
// and no theta_hi run. It skips only when the real artifacts are absent
// (for example a backend-only checkout).
func TestParityDerechoHarris2024(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "artifacts")
	b, err := os.ReadFile(filepath.Join(dir, "scenarios", "derecho-harris-2024.json"))
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("artifacts/scenarios/derecho-harris-2024.json not present")
	}
	if err != nil {
		t.Fatal(err)
	}
	sc, err := ParseScenario(b, "derecho-harris-2024")
	if err != nil {
		t.Fatal(err)
	}
	p, src, err := LoadTriggerParams(dir)
	if err != nil || src.Fixture {
		t.Fatalf("real trigger_params.json required: %+v %v", src, err)
	}
	if p.ThetaLo != 0.2 || p.ThetaHi != 0.9 || p.GapBridge != 120*time.Minute || p.MergeWindow != 72*time.Hour || p.DropoutMaxFraction != 0.1 {
		t.Fatalf("trigger params changed; update the expected answer with the ML pipeline: %+v", p)
	}

	n := 0
	det := rules.NewDetector(p, func() string { n++; return fmt.Sprintf("ev-%d", n) })
	// A second lo tracker, fed the same readings, counts dropouts: a reading
	// below theta_lo that leaves the open run open.
	lo := rules.NewRunTracker(p.ThetaLo, p.GapBridge, p.DropoutMaxFraction)
	dropouts := 0
	for i, pt := range sc.Series {
		_, wasOpen := lo.Current()
		ch := lo.Observe(sc.Times[i], pt.Share)
		_, isOpen := lo.Current()
		if pt.Share != nil && *pt.Share < p.ThetaLo && wasOpen && isOpen && ch.Closed == nil {
			dropouts++
		}
		det.Observe(sc.Times[i], pt.Share)
	}

	evs := det.Events()
	var loRuns, hiRuns []rules.Run
	for _, e := range evs {
		loRuns = append(loRuns, e.LoRuns...)
		hiRuns = append(hiRuns, e.HiRuns...)
	}
	if len(evs) != 1 || len(loRuns) != 1 {
		t.Fatalf("want exactly one event with one theta_lo run, got %d events, lo runs %+v", len(evs), loRuns)
	}
	r := loRuns[0]
	wantStart := time.Date(2024, 5, 17, 0, 15, 0, 0, time.UTC)
	wantLast := time.Date(2024, 5, 17, 19, 30, 0, 0, time.UTC)
	if !r.Start.Equal(wantStart) || !r.Last.Equal(wantLast) || r.Duration() != 19*time.Hour+15*time.Minute {
		t.Errorf("lo run: start %s last %s (%s), want %s .. %s (19.25 h)", r.Start, r.Last, r.Duration(), wantStart, wantLast)
	}
	if r.End == nil || !r.End.Equal(wantLast) {
		t.Errorf("lo run end %v, want closed at %s", r.End, wantLast)
	}
	if dropouts != 4 {
		t.Errorf("dropout readings treated as missing: %d, want 4", dropouts)
	}
	if len(hiRuns) != 0 {
		t.Errorf("want no theta_hi run, got %+v", hiRuns)
	}
}
