// Package replay runs a scenario on a simulated clock (SPEC.md section 7):
// each 15-minute reading is fed to the event detector, outage events are
// written, the rule engine is run for every active policy in the county,
// claims are recorded and paid, and everything is published on the stream
// (CONTRACTS.md section 5).
//
// Concurrency: one Engine owns at most one running replay. All replay state
// is guarded by Engine.mu, and stream messages about that state are
// published while holding it, so a subscriber's snapshot and the messages
// after it never overlap or leave a gap. Rule rule: Engine.mu is never
// acquired while holding a database connection (the service calls the
// engine only through SimNow, before it opens a transaction).
package replay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"fourhour/backend/internal/artifacts"
	"fourhour/backend/internal/ids"
	"fourhour/backend/internal/model"
	"fourhour/backend/internal/rules"
	"fourhour/backend/internal/service"
	"fourhour/backend/internal/stream"
)

// MaxSpeed bounds simulated seconds per real second.
const MaxSpeed = 10_000_000

// Options configure an Engine.
type Options struct {
	Service      *service.Service
	Log          *slog.Logger
	ArtifactsDir string
	// Params, if set, overrides trigger_params.json (tests only).
	Params *rules.Params
	// Now is the real clock; default time.Now.
	Now func() time.Time
	// Manual makes every reading wait for Advance instead of a timer
	// (tests only; speed is then ignored).
	Manual bool
}

// Engine runs replays.
type Engine struct {
	svc    *service.Service
	hub    *stream.Hub
	log    *slog.Logger
	artDir string
	params *rules.Params
	now    func() time.Time
	manual chan chan struct{} // non-nil in Manual mode

	mu  sync.Mutex
	cur *run
}

type run struct {
	id        string
	scenario  artifacts.Scenario
	params    rules.Params
	schedule  rules.Schedule
	speed     float64
	startedAt time.Time // real time; entries received since count
	det       *rules.Detector
	simTime   time.Time
	stepped   bool
	ended     bool
	series    []model.Tick
	claimIDs  []string
	decided   map[string]map[rules.Key]bool // policy -> keys
	cancel    context.CancelFunc
	done      chan struct{}
}

// New returns an Engine and installs its clock in the service.
func New(o Options) *Engine {
	e := &Engine{svc: o.Service, hub: o.Service.Hub(), log: o.Log, artDir: o.ArtifactsDir, params: o.Params, now: o.Now}
	if e.now == nil {
		e.now = time.Now
	}
	if o.Manual {
		e.manual = make(chan chan struct{})
	}
	o.Service.SetSimClock(e.SimNow)
	return e
}

// SimNow returns the simulated time while a replay runs.
func (e *Engine) SimNow() (time.Time, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cur == nil || e.cur.ended || !e.cur.stepped {
		return time.Time{}, false
	}
	return e.cur.simTime, true
}

// StartResult is the POST /api/replay/start response.
type StartResult struct {
	ReplayID string `json:"replay_id"`
	SimStart string `json:"sim_start"`
	SimEnd   string `json:"sim_end"`
}

// ScenarioItem is one GET /api/replay/scenarios item.
type ScenarioItem struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	FIPS        string `json:"fips"`
	Start       string `json:"start"`
	End         string `json:"end"`
	Description string `json:"description"`
	IsFixture   bool   `json:"is_fixture"`
	Source      any    `json:"source"`
	Provenance  any    `json:"provenance"`
}

// Scenarios lists real scenario files and the embedded fixtures.
func (e *Engine) Scenarios() []ScenarioItem {
	all, issues := artifacts.LoadScenarios(e.artDir)
	for _, is := range issues {
		e.log.Warn("scenario not loaded", "file", is.File, "reason", is.Reason)
	}
	out := make([]ScenarioItem, 0, len(all))
	for _, s := range all {
		out = append(out, ScenarioItem{ID: s.ID, Title: s.Title, FIPS: s.FIPS, Start: s.Start, End: s.End, Description: s.Description,
			IsFixture: s.IsFixture, Source: rawOrNull(s.Source), Provenance: rawOrNull(s.Provenance)})
	}
	return out
}

type rawJSON []byte

func (r rawJSON) MarshalJSON() ([]byte, error) { return r, nil }

func rawOrNull(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return rawJSON(b)
}

func svcErr(status int, code, msg string) error {
	return &service.Error{Status: status, Code: code, Message: msg}
}

// Start stops any running replay and starts scenarioID at speed simulated
// seconds per real second.
func (e *Engine) Start(scenarioID string, speed float64) (StartResult, error) {
	if !(speed > 0 && speed <= MaxSpeed) {
		return StartResult{}, svcErr(http.StatusBadRequest, "invalid_speed", fmt.Sprintf("speed must be greater than 0 and at most %d.", MaxSpeed))
	}
	sc, err := artifacts.FindScenario(e.artDir, scenarioID)
	if errors.Is(err, artifacts.ErrMissing) {
		return StartResult{}, svcErr(http.StatusNotFound, "scenario_not_found", "No scenario "+scenarioID+".")
	}
	if err != nil {
		return StartResult{}, err
	}
	var params rules.Params
	if e.params != nil {
		params = *e.params
	} else {
		p, src, err := artifacts.LoadTriggerParams(e.artDir)
		if err != nil {
			return StartResult{}, err
		}
		if src.Fixture {
			e.log.Warn("trigger_params.json not usable; replay uses the FIXTURE trigger parameters", "reason", src.Reason)
		}
		params = p
	}
	schedule, ssrc := artifacts.LoadSchedule(e.artDir)
	if !ssrc.FromArtifact {
		e.log.Warn("payout schedule from SPEC.md section 4 (pricing_table.json not usable)", "reason", ssrc.Reason)
	}
	e.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	r := &run{
		id: ids.New("rpl_"), scenario: sc, params: params, schedule: schedule, speed: speed, startedAt: e.now().Add(-time.Second),
		det: rules.NewDetector(params, func() string { return ids.New(ids.Event) }), series: []model.Tick{},
		decided: map[string]map[rules.Key]bool{}, cancel: cancel, done: make(chan struct{}),
	}
	e.mu.Lock()
	e.cur = r
	e.mu.Unlock()
	go e.loop(ctx, r)
	e.log.Info("replay started", "replay_id", r.id, "scenario_id", sc.ID, "speed", speed, "theta_hi", params.ThetaHi, "theta_lo", params.ThetaLo)
	return StartResult{ReplayID: r.id, SimStart: sc.Start, SimEnd: sc.End}, nil
}

// Stop stops the running replay, if any, and waits for it to finish. The
// replay's state stays until Reset or the next Start.
func (e *Engine) Stop() bool {
	e.mu.Lock()
	r := e.cur
	e.mu.Unlock()
	if r == nil {
		return false
	}
	r.cancel()
	<-r.done
	return true
}

// Reset stops any replay and clears the in-memory state. It deletes no rows
// (CONTRACTS.md 1.4).
func (e *Engine) Reset() {
	e.Stop()
	e.mu.Lock()
	e.cur = nil
	e.mu.Unlock()
}

// Advance processes the next n readings in Manual mode and returns how many
// were processed (fewer when the replay ended).
func (e *Engine) Advance(n int) int {
	done := e.Done()
	for i := 0; i < n; i++ {
		ack := make(chan struct{})
		select {
		case e.manual <- ack:
			<-ack
		case <-done:
			return i
		}
	}
	return n
}

// Done returns a channel closed when the current replay finishes, or nil.
func (e *Engine) Done() <-chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cur == nil {
		return nil
	}
	return e.cur.done
}

func (e *Engine) loop(ctx context.Context, r *run) {
	defer close(r.done)
	stopped := false
	for i := range r.scenario.Series {
		if e.manual != nil {
			select {
			case <-ctx.Done():
				stopped = true
			case ack := <-e.manual:
				e.step(ctx, r, i)
				close(ack)
				continue
			}
			break
		}
		if i > 0 {
			gap := r.scenario.Times[i].Sub(r.scenario.Times[i-1])
			wait := time.Duration(float64(gap) / r.speed)
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				stopped = true
			case <-t.C:
			}
			if stopped {
				break
			}
		} else if ctx.Err() != nil {
			stopped = true
			break
		}
		e.step(ctx, r, i)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !stopped {
		// "When the replay ends": pending R4/R2 decisions are made now.
		e.evaluate(context.Background(), r, true)
	}
	r.ended = true
	if err := e.hub.Publish(model.EvReplayEnded, model.ReplayEnded{SimTime: ids.FormatTime(r.simTime), ReplayID: r.id}); err != nil {
		e.log.Error("publish replay_ended", "err", err)
	}
	e.log.Info("replay ended", "replay_id", r.id, "stopped", stopped, "claims", len(r.claimIDs))
}

// step processes reading i. It holds e.mu throughout, so stream messages
// leave in order and a snapshot is never taken half way through a step.
func (e *Engine) step(ctx context.Context, r *run, i int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	dbctx := context.Background() // a step's writes complete even if the replay is stopped meanwhile
	p := r.scenario.Series[i]
	t := r.scenario.Times[i]
	r.simTime, r.stepped = t, true
	st := ids.FormatTime(t)
	tick := model.Tick{SimTime: st, Share: p.Share, CustomersOut: p.CustomersOut, FIPS: r.scenario.FIPS}
	r.series = append(r.series, tick)
	e.pub(model.EvTick, tick)

	res := r.det.Observe(t, p.Share)
	sid := r.scenario.ID
	for _, id := range res.Changed {
		ev, ok := r.det.Event(id)
		if !ok {
			continue
		}
		if err := e.svc.UpsertOutageEvent(dbctx, service.EventRow(ev, r.scenario.FIPS, &sid)); err != nil {
			e.log.Error("replay: write outage event", "err", err)
		}
	}
	for _, id := range res.Deleted {
		if err := e.svc.DeleteOutageEvent(dbctx, id); err != nil {
			e.log.Error("replay: delete merged event", "err", err)
		}
	}
	for _, sg := range res.Signals {
		switch sg.Kind {
		case "opened":
			e.pub(model.EvEventOpened, model.EventOpened{SimTime: st, OutageEventID: sg.EventID, Threshold: sg.Threshold})
		case "closed":
			e.pub(model.EvEventClosed, model.EventClosed{SimTime: st, OutageEventID: sg.EventID, Threshold: sg.Threshold, DurationHours: sg.DurationHours})
		case "merged":
			e.pub(model.EvEventMerged, model.EventMerged{SimTime: st, OutageEventID: sg.EventID, IntoOutageEventID: sg.IntoEventID})
		}
	}
	e.evaluate(dbctx, r, false)
	_ = ctx
}

func (e *Engine) pub(event string, body any) {
	if err := e.hub.Publish(event, body); err != nil {
		e.log.Error("publish", "event", event, "err", err)
	}
}

// evaluate runs the rules for every active policy in the county. Caller
// holds e.mu.
func (e *Engine) evaluate(ctx context.Context, r *run, ended bool) {
	if !r.stepped {
		return
	}
	pols, err := e.svc.ActivePolicies(ctx, r.scenario.FIPS)
	if err != nil {
		e.log.Error("replay: active policies", "err", err)
		return
	}
	events := r.det.Events()
	for _, rp := range pols {
		pid := rp.Policy.ID
		entries, err := e.svc.RuleEntries(ctx, pid, r.startedAt)
		if err != nil {
			e.log.Error("replay: witness entries", "policy_id", pid, "err", err)
			continue
		}
		if r.decided[pid] == nil {
			r.decided[pid] = map[rules.Key]bool{}
		}
		keys := r.decided[pid]
		ds, err := rules.Evaluate(rules.Input{
			Now: r.simTime, ReplayEnded: ended, Events: events, Schedule: r.schedule,
			Policy:  rules.Policy{ID: pid, CoverCents: rp.Policy.CoverCents, HasDevice: len(rp.DeviceIDs) > 0, Entries: entries},
			Decided: func(k rules.Key) bool { return keys[k] },
		})
		if err != nil {
			e.log.Error("replay: evaluate", "policy_id", pid, "err", err)
			continue
		}
		for _, d := range ds {
			sim := r.simTime
			rec, err := e.svc.RecordDecision(ctx, pid, d, &sim)
			if err != nil {
				e.log.Error("replay: record claim", "policy_id", pid, "rule", d.Rule, "err", err)
				continue
			}
			keys[d.Key()] = true
			if !rec.Created {
				continue
			}
			r.claimIDs = append(r.claimIDs, rec.Claim.ID)
			e.log.Info("claim decided", "claim_id", rec.Claim.ID, "policy_id", pid, "rule", d.Rule, "decision", d.Decision, "amount_cents", d.AmountCents, "sim_time", ids.FormatTime(sim))
			st := ids.FormatTime(sim)
			e.pub(model.EvClaimDecided, model.ClaimDecided{SimTime: &st, Claim: rec.Claim})
			if rec.Payout != nil {
				e.svc.SendPayoutAsync(rec.Payout.ID)
			}
		}
	}
}

// Subscribe registers a stream subscriber and returns the snapshot that
// precedes its messages (CONTRACTS.md section 5). The caller must
// Unsubscribe the returned Sub.
func (e *Engine) Subscribe(ctx context.Context) (*stream.Sub, model.Snapshot, error) {
	e.mu.Lock()
	sub := e.hub.Subscribe()
	snap := model.Snapshot{OpenEvents: []model.OutageEvent{}, Series: []model.Tick{}, Claims: []model.ClaimItem{}}
	var claimIDs []string
	if r := e.cur; r != nil && !r.ended {
		id, scID := r.id, r.scenario.ID
		snap.ReplayID, snap.ScenarioID = &id, &scID
		if r.stepped {
			st := ids.FormatTime(r.simTime)
			snap.SimTime = &st
		}
		snap.Series = append(snap.Series, r.series...)
		sid := r.scenario.ID
		for _, ev := range r.det.Events() {
			if ev.LoEnd() == nil {
				snap.OpenEvents = append(snap.OpenEvents, service.EventRow(ev, r.scenario.FIPS, &sid))
			}
		}
		claimIDs = append([]string{}, r.claimIDs...)
	}
	e.mu.Unlock()
	if claimIDs != nil {
		items, err := e.svc.ClaimItems(ctx, claimIDs)
		if err != nil {
			e.hub.Unsubscribe(sub)
			return nil, model.Snapshot{}, err
		}
		snap.Claims = items
	}
	return sub, snap, nil
}
