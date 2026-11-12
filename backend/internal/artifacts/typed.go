package artifacts

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"fourhour/backend/internal/ids"
	"fourhour/backend/internal/money"
	"fourhour/backend/internal/rules"
)

// Fixtures named fixture-* are embedded so the loop can run before ml/ has
// written real artifacts. They are never reported in artifacts_loaded and
// never served by the analytics endpoints.
//
//go:embed fixtures
var fixtureFS embed.FS

// ErrMissing means the artifact file does not exist.
var ErrMissing = errors.New("artifact missing")

// ErrNotComputed means a value the caller needs is null in the artifact.
var ErrNotComputed = errors.New("artifact value not computed")

// ReadRaw returns the bytes of a top-level artifact after the same checks
// Load makes. name is a file stem such as "backtest".
func ReadRaw(dir, name string) ([]byte, error) {
	var sp *spec
	for _, s := range specs() {
		if s.name == name {
			s := s
			sp = &s
		}
	}
	if sp == nil {
		return nil, fmt.Errorf("unknown artifact %q", name)
	}
	b, err := readCapped(filepath.Join(dir, name+".json"))
	if err != nil {
		return nil, err
	}
	if err := validateBytes(b, *sp); err != nil {
		return nil, fmt.Errorf("%s.json invalid: %w", name, err)
	}
	return b, nil
}

func readCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), ErrMissing)
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxArtifactBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(b) > maxArtifactBytes {
		return nil, fmt.Errorf("%s larger than %d bytes", path, maxArtifactBytes)
	}
	return b, nil
}

func decodeStrict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	return dec.Decode(v)
}

// ---------------------------------------------------------------------------
// trigger_params.json

type triggerFile struct {
	ThetaHi          *json.Number `json:"theta_hi"`
	ThetaLo          *json.Number `json:"theta_lo"`
	GapBridgeMinutes *json.Number `json:"gap_bridge_minutes"`
	EventMergeHours  *json.Number `json:"event_merge_hours"`
	DropoutMax       *json.Number `json:"dropout_max_fraction"`
}

// TriggerSource says where the trigger parameters came from.
type TriggerSource struct {
	Fixture bool
	Path    string
	// Reason the real file was not used, when Fixture is true.
	Reason string
}

// LoadTriggerParams reads ARTIFACTS_DIR/trigger_params.json. If it is
// missing, invalid or has a null value, it falls back to the embedded
// fixture-trigger_params.json and says so in the source.
func LoadTriggerParams(dir string) (rules.Params, TriggerSource, error) {
	path := filepath.Join(dir, "trigger_params.json")
	b, err := ReadRaw(dir, TriggerParams)
	if err == nil {
		p, perr := parseTrigger(b)
		if perr == nil {
			return p, TriggerSource{Path: path}, nil
		}
		err = perr
	}
	fb, ferr := fixtureFS.ReadFile("fixtures/fixture-trigger_params.json")
	if ferr != nil {
		return rules.Params{}, TriggerSource{}, fmt.Errorf("read fixture trigger params: %w", ferr)
	}
	p, perr := parseTrigger(fb)
	if perr != nil {
		return rules.Params{}, TriggerSource{}, fmt.Errorf("fixture trigger params: %w", perr)
	}
	return p, TriggerSource{Fixture: true, Path: "fixtures/fixture-trigger_params.json (embedded)", Reason: err.Error()}, nil
}

// ParseTriggerParams parses a trigger_params document (exported for tests).
func ParseTriggerParams(b []byte) (rules.Params, error) { return parseTrigger(b) }

func parseTrigger(b []byte) (rules.Params, error) {
	var tf triggerFile
	if err := decodeStrict(b, &tf); err != nil {
		return rules.Params{}, fmt.Errorf("trigger_params: %w", err)
	}
	num := func(n *json.Number, name string) (float64, error) {
		if n == nil {
			return 0, fmt.Errorf("trigger_params.%s: %w", name, ErrNotComputed)
		}
		v, err := n.Float64()
		if err != nil {
			return 0, fmt.Errorf("trigger_params.%s: %w", name, err)
		}
		return v, nil
	}
	hi, err := num(tf.ThetaHi, "theta_hi")
	if err != nil {
		return rules.Params{}, err
	}
	lo, err := num(tf.ThetaLo, "theta_lo")
	if err != nil {
		return rules.Params{}, err
	}
	gap, err := num(tf.GapBridgeMinutes, "gap_bridge_minutes")
	if err != nil {
		return rules.Params{}, err
	}
	merge, err := num(tf.EventMergeHours, "event_merge_hours")
	if err != nil {
		return rules.Params{}, err
	}
	drop, err := num(tf.DropoutMax, "dropout_max_fraction")
	if err != nil {
		return rules.Params{}, err
	}
	p := rules.Params{ThetaHi: hi, ThetaLo: lo, GapBridge: time.Duration(gap * float64(time.Minute)), MergeWindow: time.Duration(merge * float64(time.Hour)), DropoutMaxFraction: drop}
	if err := p.Validate(); err != nil {
		return rules.Params{}, err
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// pricing_table.json

// CountyPricing is one pricing_table.counties value. Values stay as JSON
// number literals so money arithmetic stays exact.
type CountyPricing struct {
	ExpectedEventsPerYear *json.Number `json:"expected_events_per_year"`
	ProbPremisesAffected  *json.Number `json:"prob_premises_affected"`
	CredibilityWeight     *json.Number `json:"credibility_weight"`
	Offered               *bool        `json:"offered"`
	NotOfferedReason      *string      `json:"not_offered_reason"`
}

// NoPriceReason is the not_offered_reason for a county missing from
// pricing_table.json (CONTRACTS.md section 4).
const NoPriceReason = "No price has been computed for this county."

// Offer is whether a county can be quoted, and why not.
type Offer struct {
	Offered bool
	// Reason is one sentence, nil when offered.
	Reason *string
}

// OfferFor says whether fips is offered. A county missing from the table,
// a null county, or a null offered is not offered; a missing reason falls
// back to NoPriceReason so a refusal always carries a sentence.
func (p Pricing) OfferFor(fips string) Offer {
	c := p.Counties[fips]
	if c != nil && c.Offered != nil && *c.Offered {
		return Offer{Offered: true}
	}
	reason := NoPriceReason
	if c != nil && c.NotOfferedReason != nil && strings.TrimSpace(*c.NotOfferedReason) != "" {
		reason = *c.NotOfferedReason
	}
	return Offer{Reason: &reason}
}

// CountyOffers returns OfferFor for each fips. If pricing_table.json is
// missing or invalid, every county is not offered with NoPriceReason.
func CountyOffers(dir string, fips []string) map[string]Offer {
	pt, err := LoadPricingTable(dir)
	if err != nil {
		pt = Pricing{}
	}
	out := make(map[string]Offer, len(fips))
	for _, f := range fips {
		out[f] = pt.OfferFor(f)
	}
	return out
}

// Rates is pricing_table.rates.
type Rates struct {
	RiskMarginRate   *json.Number `json:"risk_margin_rate"`
	ExpenseLoadCents *json.Number `json:"expense_load_cents"`
}

// PayoutSchedule is pricing_table.payout_schedule. Validation guarantees
// its keys are exactly rules.PaidTiers() (only "t4" since CONTRACTS.md 1.8).
type PayoutSchedule struct {
	T4 *json.Number `json:"t4"`
}

// Pricing is pricing_table.json.
type Pricing struct {
	ModelVersion   *string                   `json:"model_version"`
	Counties       map[string]*CountyPricing `json:"counties"`
	Rates          *Rates                    `json:"rates"`
	PayoutSchedule *PayoutSchedule           `json:"payout_schedule"`
	Provenance     json.RawMessage           `json:"provenance"`
}

// LoadPricingTable reads and validates pricing_table.json.
func LoadPricingTable(dir string) (Pricing, error) {
	b, err := ReadRaw(dir, PricingTable)
	if err != nil {
		return Pricing{}, err
	}
	var pt Pricing
	if err := decodeStrict(b, &pt); err != nil {
		return Pricing{}, fmt.Errorf("pricing_table: %w", err)
	}
	return pt, nil
}

// Rat converts a JSON number to an exact rational; nil is ErrNotComputed.
func Rat(n *json.Number, name string) (*big.Rat, error) {
	if n == nil {
		return nil, fmt.Errorf("%s: %w", name, ErrNotComputed)
	}
	r, err := money.Decimal(n.String())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return r, nil
}

// ScheduleSource says where the payout schedule came from.
type ScheduleSource struct {
	FromArtifact bool
	Reason       string
}

// SpecSchedule is the payout schedule stated in SPEC.md section 4 (60% at
// 4 hours; the 24-hour top-up was cut). It is a product assumption, not a
// fitted value, and is used only when pricing_table.json has no schedule.
func SpecSchedule() rules.Schedule {
	return rules.Schedule{T4: big.NewRat(6, 10)}
}

// LoadSchedule returns pricing_table.payout_schedule, or the SPEC.md
// schedule with the reason when the artifact is missing, invalid (including
// a schedule whose tiers differ from the paid tiers) or has nulls.
func LoadSchedule(dir string) (rules.Schedule, ScheduleSource) {
	pt, err := LoadPricingTable(dir)
	if err != nil {
		return SpecSchedule(), ScheduleSource{Reason: err.Error()}
	}
	if pt.PayoutSchedule == nil {
		return SpecSchedule(), ScheduleSource{Reason: "pricing_table.payout_schedule is null"}
	}
	t4, err := Rat(pt.PayoutSchedule.T4, "payout_schedule.t4")
	if err != nil {
		return SpecSchedule(), ScheduleSource{Reason: err.Error()}
	}
	return rules.Schedule{T4: t4}, ScheduleSource{FromArtifact: true}
}

// ScheduleShares decodes a payout_schedule object as tier -> exact share,
// keeping every key it has (so an unpaid tier such as t24 is visible).
// A null share is ErrNotComputed.
func ScheduleShares(raw json.RawMessage) (map[string]*big.Rat, error) {
	var m map[string]*json.Number
	if err := decodeStrict(raw, &m); err != nil {
		return nil, fmt.Errorf("payout_schedule: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("payout_schedule: %w", ErrNotComputed)
	}
	out := make(map[string]*big.Rat, len(m))
	for k, n := range m {
		r, err := Rat(n, "payout_schedule."+k)
		if err != nil {
			return nil, err
		}
		out[k] = r
	}
	return out, nil
}

// CheckScheduleParity returns an error unless priced (the schedule the
// premium is computed from) and paid (the share the rule engine pays per
// tier) have the same tiers with exactly equal shares.
func CheckScheduleParity(priced, paid map[string]*big.Rat) error {
	var problems []string
	for tier, share := range priced {
		p, ok := paid[tier]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("tier %s is priced but never paid", tier))
		case p.Cmp(share) != 0:
			problems = append(problems, fmt.Sprintf("tier %s is priced at %s but paid at %s", tier, share.RatString(), p.RatString()))
		}
	}
	for tier := range paid {
		if _, ok := priced[tier]; !ok {
			problems = append(problems, fmt.Sprintf("tier %s is paid but not priced", tier))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("payout schedule parity: %s", strings.Join(problems, "; "))
	}
	return nil
}

// ---------------------------------------------------------------------------
// data_quality.json

// CountyCoverage reads data_quality.county_coverage[fips].data_coverage.
// It returns nil when the file, the county or the value is missing or null.
func CountyCoverage(dir string) map[string]*float64 {
	out := map[string]*float64{}
	b, err := ReadRaw(dir, DataQuality)
	if err != nil {
		return out
	}
	var dq struct {
		CountyCoverage map[string]*struct {
			DataCoverage *float64 `json:"data_coverage"`
		} `json:"county_coverage"`
	}
	if json.Unmarshal(b, &dq) != nil {
		return out
	}
	for fips, v := range dq.CountyCoverage {
		if v != nil {
			out[fips] = v.DataCoverage
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// scenarios/{id}.json

// Point is one 15-minute reading. CustomersOut and Share are both null for
// a missing interval.
type Point struct {
	T            string   `json:"t"`
	CustomersOut *int64   `json:"customers_out"`
	Share        *float64 `json:"share"`
}

// Scenario is a scenario file (CONTRACTS.md section 8).
type Scenario struct {
	ID              string          `json:"id"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	FIPS            string          `json:"fips"`
	CountyCustomers int64           `json:"county_customers"`
	Start           string          `json:"start"`
	End             string          `json:"end"`
	Series          []Point         `json:"series"`
	Source          json.RawMessage `json:"source"`
	Provenance      json.RawMessage `json:"provenance"`

	IsFixture bool        `json:"-"`
	Times     []time.Time `json:"-"` // parsed Series[i].T
}

var scenarioID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ParseScenario decodes and validates a scenario file. The file name stem
// must equal the id.
func ParseScenario(b []byte, stem string) (Scenario, error) {
	var s Scenario
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&s); err != nil {
		return Scenario{}, fmt.Errorf("scenario %s: %w", stem, err)
	}
	if s.ID != stem || !scenarioID.MatchString(s.ID) {
		return Scenario{}, fmt.Errorf("scenario %s: id %q must match the file name", stem, s.ID)
	}
	if !isFIPS(s.FIPS) {
		return Scenario{}, fmt.Errorf("scenario %s: fips %q", stem, s.FIPS)
	}
	if len(s.Series) == 0 {
		return Scenario{}, fmt.Errorf("scenario %s: empty series", stem)
	}
	if len(s.Provenance) == 0 || string(s.Provenance) == "null" {
		return Scenario{}, fmt.Errorf("scenario %s: missing provenance", stem)
	}
	s.Times = make([]time.Time, len(s.Series))
	for i, p := range s.Series {
		t, err := ids.ParseTime(p.T)
		if err != nil {
			return Scenario{}, fmt.Errorf("scenario %s: series[%d]: %w", stem, i, err)
		}
		if i > 0 && !t.After(s.Times[i-1]) {
			return Scenario{}, fmt.Errorf("scenario %s: series[%d] not after the previous point", stem, i)
		}
		if (p.Share == nil) != (p.CustomersOut == nil) {
			return Scenario{}, fmt.Errorf("scenario %s: series[%d]: share and customers_out must both be null or both set", stem, i)
		}
		if p.Share != nil && (*p.Share < 0 || *p.Share > 1) {
			return Scenario{}, fmt.Errorf("scenario %s: series[%d]: share %v outside [0,1]", stem, i, *p.Share)
		}
		s.Times[i] = t
	}
	if _, err := ids.ParseTime(s.Start); err != nil {
		return Scenario{}, fmt.Errorf("scenario %s: start: %w", stem, err)
	}
	if _, err := ids.ParseTime(s.End); err != nil {
		return Scenario{}, fmt.Errorf("scenario %s: end: %w", stem, err)
	}
	s.IsFixture = strings.HasPrefix(s.ID, "fixture-")
	return s, nil
}

// ScenarioIssue is a scenario file that could not be used.
type ScenarioIssue struct {
	File   string
	Reason string
}

// LoadScenarios returns every valid scenario in ARTIFACTS_DIR/scenarios
// plus the embedded fixtures, sorted with real scenarios first. A real file
// never shares an id with a fixture because fixture ids start "fixture-".
func LoadScenarios(dir string) ([]Scenario, []ScenarioIssue) {
	var out []Scenario
	var issues []ScenarioIssue
	sdir := filepath.Join(dir, "scenarios")
	if ents, err := os.ReadDir(sdir); err == nil {
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			p := filepath.Join(sdir, e.Name())
			b, err := readCapped(p)
			if err != nil {
				issues = append(issues, ScenarioIssue{p, err.Error()})
				continue
			}
			s, err := ParseScenario(b, strings.TrimSuffix(e.Name(), ".json"))
			if err != nil {
				issues = append(issues, ScenarioIssue{p, err.Error()})
				continue
			}
			out = append(out, s)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		issues = append(issues, ScenarioIssue{sdir, err.Error()})
	}
	have := map[string]bool{}
	for _, s := range out {
		have[s.ID] = true
	}
	fents, err := fs.ReadDir(fixtureFS, "fixtures/scenarios")
	if err == nil {
		for _, e := range fents {
			b, err := fixtureFS.ReadFile("fixtures/scenarios/" + e.Name())
			if err != nil {
				issues = append(issues, ScenarioIssue{e.Name(), err.Error()})
				continue
			}
			s, err := ParseScenario(b, strings.TrimSuffix(e.Name(), ".json"))
			if err != nil {
				issues = append(issues, ScenarioIssue{e.Name(), err.Error()})
				continue
			}
			if !have[s.ID] {
				out = append(out, s)
			}
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].IsFixture != out[b].IsFixture {
			return !out[a].IsFixture
		}
		return out[a].ID < out[b].ID
	})
	return out, issues
}

// FindScenario returns one scenario by id.
func FindScenario(dir, id string) (Scenario, error) {
	all, _ := LoadScenarios(dir)
	for _, s := range all {
		if s.ID == id {
			return s, nil
		}
	}
	return Scenario{}, fmt.Errorf("scenario %q: %w", id, ErrMissing)
}
