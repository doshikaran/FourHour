// Package artifacts reads and validates the files in ARTIFACTS_DIR
// (CONTRACTS.md section 8). The backend only reads artifacts; it never writes.
//
// Validation at this stage is deliberately light. For each of the six
// top-level files it checks that:
//   - the file exists, is at most 32 MiB, and is a JSON object;
//   - it has a "provenance" object with every key from section 8
//     (generated_at, git_commit, code, data_sources, train_years, test_years,
//     seed, notes), generated_at parses as RFC 3339, and data_sources is an array;
//   - it has the top-level keys section 8 names for that file. A key may be
//     null ("could not be computed"), but it must be present;
//   - the two inner shapes contract 1.2 pins down: pricing_table.counties is
//     an object keyed by 5-digit FIPS whose values carry the four county
//     breakdown keys, and solvency.stop_loss, when not null, carries
//     attachment_cents, limit_cents and price_cents.
//
// It does not check value types or ranges, or inner shapes the contract
// still lists as open. Scenario files are not part of artifacts_loaded
// (CONTRACTS.md section 4) and are not checked here.
package artifacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const maxArtifactBytes = 32 << 20

// File names as they appear in artifacts_loaded (the file stem).
const (
	TriggerParams = "trigger_params"
	PricingTable  = "pricing_table"
	Backtest      = "backtest"
	Solvency      = "solvency"
	BasisRisk     = "basis_risk"
	DataQuality   = "data_quality"
)

type spec struct {
	name     string
	required []string
	inner    func(top map[string]json.RawMessage) error // optional
}

// specs returns the section 8 top-level keys per file. basis_risk and
// data_quality list only the keys section 8 names explicitly; their per-scheme
// and per-county-year layout is not pinned down by the contract.
func specs() []spec {
	return []spec{
		{TriggerParams, []string{"theta_hi", "theta_lo", "gap_bridge_minutes", "grid"}, nil},
		{PricingTable, []string{"model_version", "counties", "rates", "payout_schedule"}, checkPricingCounties},
		{Backtest, []string{"frequency", "severity", "credibility", "pool"}, nil},
		{Solvency, []string{"members", "simulations", "ruin_probability", "capital_1_in_200_cents", "stop_loss", "annual_loss_quantiles"}, checkStopLoss},
		{BasisRisk, []string{"districts_used", "fit_period", "score_period"}, nil},
		{DataQuality, nil, nil},
	}
}

func provenanceKeys() []string {
	return []string{"generated_at", "git_commit", "code", "data_sources", "train_years", "test_years", "seed", "notes"}
}

// Status is the load result for one file.
type Status struct {
	Name   string
	Path   string
	Loaded bool
	Reason string // empty when Loaded
}

// Report is the result of loading a directory.
type Report struct {
	Dir   string
	Files []Status
}

// LoadedNames returns the names of valid files, in a fixed order. Never nil.
func (r Report) LoadedNames() []string {
	out := []string{}
	for _, f := range r.Files {
		if f.Loaded {
			out = append(out, f.Name)
		}
	}
	return out
}

// Load checks every known artifact in dir. It never fails: a missing
// directory or a bad file is recorded with its reason and logged.
func Load(dir string, log *slog.Logger) Report {
	r := Report{Dir: dir}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		reason := "artifacts directory not found"
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			reason = "artifacts directory unreadable: " + err.Error()
		} else if err == nil {
			reason = "artifacts path is not a directory"
		}
		log.Warn("artifacts not loaded", "dir", dir, "reason", reason)
		for _, s := range specs() {
			r.Files = append(r.Files, Status{Name: s.name, Path: filepath.Join(dir, s.name+".json"), Reason: reason})
		}
		return r
	}
	for _, s := range specs() {
		p := filepath.Join(dir, s.name+".json")
		st := Status{Name: s.name, Path: p}
		if err := validateFile(p, s); err != nil {
			st.Reason = err.Error()
			log.Warn("artifact not loaded", "file", p, "reason", st.Reason)
		} else {
			st.Loaded = true
			log.Info("artifact loaded", "file", p)
		}
		r.Files = append(r.Files, st)
	}
	return r
}

func validateFile(path string, s spec) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("missing")
		}
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxArtifactBytes+1))
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if len(b) > maxArtifactBytes {
		return fmt.Errorf("larger than %d bytes", maxArtifactBytes)
	}
	return validateBytes(b, s)
}

func validateBytes(b []byte, s spec) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return fmt.Errorf("not a JSON object: %w", err)
	}
	if top == nil {
		return errors.New("not a JSON object: null")
	}
	rawProv, ok := top["provenance"]
	if !ok {
		return errors.New(`missing "provenance"`)
	}
	var prov map[string]json.RawMessage
	if err := json.Unmarshal(rawProv, &prov); err != nil || prov == nil {
		return errors.New(`"provenance" is not an object`)
	}
	for _, k := range provenanceKeys() {
		if _, ok := prov[k]; !ok {
			return fmt.Errorf(`provenance missing %q`, k)
		}
	}
	var gen string
	if err := json.Unmarshal(prov["generated_at"], &gen); err != nil {
		return errors.New(`provenance.generated_at is not a string`)
	}
	if _, err := time.Parse(time.RFC3339, gen); err != nil {
		return fmt.Errorf(`provenance.generated_at is not RFC 3339: %q`, gen)
	}
	var sources []json.RawMessage
	if err := json.Unmarshal(prov["data_sources"], &sources); err != nil || sources == nil {
		return errors.New(`provenance.data_sources is not an array`)
	}
	for _, k := range s.required {
		if _, ok := top[k]; !ok {
			return fmt.Errorf("missing top-level key %q", k)
		}
	}
	if s.inner != nil {
		return s.inner(top)
	}
	return nil
}

func isNull(raw json.RawMessage) bool { return string(raw) == "null" }

// objectWithKeys decodes raw as an object and checks the keys are present.
func objectWithKeys(raw json.RawMessage, what string, keys []string) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("%s is not an object", what)
	}
	for _, k := range keys {
		if _, ok := obj[k]; !ok {
			return nil, fmt.Errorf("%s missing %q", what, k)
		}
	}
	return obj, nil
}

func isFIPS(s string) bool {
	if len(s) != 5 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// checkPricingCounties: counties is an object keyed by FIPS (null allowed).
func checkPricingCounties(top map[string]json.RawMessage) error {
	raw := top["counties"]
	if isNull(raw) {
		return nil
	}
	counties, err := objectWithKeys(raw, "counties", nil)
	if err != nil {
		return err
	}
	for fips, v := range counties {
		if !isFIPS(fips) {
			return fmt.Errorf("counties key %q is not a 5-digit FIPS", fips)
		}
		if isNull(v) {
			continue
		}
		if _, err := objectWithKeys(v, "counties."+fips, []string{"expected_events_per_year", "prob_24h_given_event", "prob_premises_affected", "credibility_weight"}); err != nil {
			return err
		}
	}
	return nil
}

// checkStopLoss: stop_loss, when not null, has the three cent keys.
func checkStopLoss(top map[string]json.RawMessage) error {
	raw := top["stop_loss"]
	if isNull(raw) {
		return nil
	}
	_, err := objectWithKeys(raw, "stop_loss", []string{"attachment_cents", "limit_cents", "price_cents"})
	return err
}
