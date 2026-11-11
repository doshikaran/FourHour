package artifacts

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureDir = "testdata/fixture-artifacts"

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// copyFixtures copies the fixture set into a temp dir so tests can damage it.
func copyFixtures(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(fixtureDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func TestAllFixturesLoad(t *testing.T) {
	r := Load(fixtureDir, quiet())
	got := strings.Join(r.LoadedNames(), ",")
	if got != "trigger_params,pricing_table,backtest,solvency,basis_risk,data_quality" {
		for _, f := range r.Files {
			t.Logf("%s: %v %s", f.Name, f.Loaded, f.Reason)
		}
		t.Errorf("loaded %s", got)
	}
}

func TestMissingDirDoesNotCrash(t *testing.T) {
	r := Load(filepath.Join(t.TempDir(), "nope"), quiet())
	if n := r.LoadedNames(); n == nil || len(n) != 0 {
		t.Errorf("want empty non-nil, got %#v", n)
	}
	if len(r.Files) != 6 || !strings.Contains(r.Files[0].Reason, "not found") {
		t.Errorf("files: %+v", r.Files)
	}
}

func TestBadFilesLeftOut(t *testing.T) {
	cases := map[string]struct{ content, reason string }{
		"not json":          {"{nope", "not a JSON object"},
		"array":             {"[]", "not a JSON object"},
		"no provenance":     {`{"theta_hi":0.1,"theta_lo":0.05,"gap_bridge_minutes":30,"grid":[]}`, `missing "provenance"`},
		"provenance key":    {`{"provenance":{"generated_at":"2026-11-11T00:00:00Z"},"theta_hi":null,"theta_lo":null,"gap_bridge_minutes":null,"grid":[]}`, "provenance missing"},
		"bad generated_at":  {`{"provenance":{"generated_at":"yesterday","git_commit":"","code":"","data_sources":[],"train_years":[],"test_years":[],"seed":1,"notes":""},"theta_hi":null,"theta_lo":null,"gap_bridge_minutes":null,"grid":[]}`, "RFC 3339"},
		"data_sources type": {`{"provenance":{"generated_at":"2026-11-11T00:00:00Z","git_commit":"","code":"","data_sources":{},"train_years":[],"test_years":[],"seed":1,"notes":""},"theta_hi":null,"theta_lo":null,"gap_bridge_minutes":null,"grid":[]}`, "data_sources"},
		"missing key":       {`{"provenance":{"generated_at":"2026-11-11T00:00:00Z","git_commit":"","code":"","data_sources":[],"train_years":[],"test_years":[],"seed":1,"notes":""},"theta_hi":null,"theta_lo":null,"grid":[]}`, `"gap_bridge_minutes"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := copyFixtures(t)
			if err := os.WriteFile(filepath.Join(dir, "trigger_params.json"), []byte(c.content), 0o600); err != nil {
				t.Fatal(err)
			}
			r := Load(dir, quiet())
			if strings.Contains(strings.Join(r.LoadedNames(), ","), "trigger_params") {
				t.Fatal("bad file was loaded")
			}
			if len(r.LoadedNames()) != 5 {
				t.Errorf("other files affected: %v", r.LoadedNames())
			}
			if !strings.Contains(r.Files[0].Reason, c.reason) {
				t.Errorf("reason %q, want %q", r.Files[0].Reason, c.reason)
			}
		})
	}
}

func TestMissingFileLeftOut(t *testing.T) {
	dir := copyFixtures(t)
	if err := os.Remove(filepath.Join(dir, "solvency.json")); err != nil {
		t.Fatal(err)
	}
	r := Load(dir, quiet())
	if strings.Join(r.LoadedNames(), ",") != "trigger_params,pricing_table,backtest,basis_risk,data_quality" {
		t.Errorf("got %v", r.LoadedNames())
	}
}

const prov = `"provenance":{"generated_at":"2026-11-11T00:00:00Z","git_commit":"","code":"","data_sources":[],"train_years":[],"test_years":[],"seed":null,"notes":"test"}`

func TestInnerShapes(t *testing.T) {
	county := `{"expected_events_per_year":null,"prob_24h_given_event":null,"prob_premises_affected":null,"credibility_weight":null}`
	pricing := func(counties string) string {
		return `{` + prov + `,"model_version":null,"rates":null,"payout_schedule":null,"counties":` + counties + `}`
	}
	solvency := func(stopLoss string) string {
		return `{` + prov + `,"members":null,"simulations":null,"ruin_probability":null,"capital_1_in_200_cents":null,"annual_loss_quantiles":null,"stop_loss":` + stopLoss + `}`
	}
	cases := []struct {
		name, file, content string
		ok                  bool
		reason              string
	}{
		{"counties keyed by FIPS", "pricing_table", pricing(`{"48201":` + county + `,"12086":null}`), true, ""},
		{"counties null", "pricing_table", pricing(`null`), true, ""},
		{"counties not object", "pricing_table", pricing(`[]`), false, "counties is not an object"},
		{"counties bad key", "pricing_table", pricing(`{"Harris":` + county + `}`), false, "not a 5-digit FIPS"},
		{"county missing key", "pricing_table", pricing(`{"48201":{"expected_events_per_year":null}}`), false, `counties.48201 missing "prob_24h_given_event"`},
		{"stop_loss cents keys", "solvency", solvency(`{"attachment_cents":null,"limit_cents":null,"price_cents":null}`), true, ""},
		{"stop_loss null", "solvency", solvency(`null`), true, ""},
		{"stop_loss old keys", "solvency", solvency(`{"attachment":null,"limit":null,"price":null}`), false, `stop_loss missing "attachment_cents"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := copyFixtures(t)
			if err := os.WriteFile(filepath.Join(dir, c.file+".json"), []byte(c.content), 0o600); err != nil {
				t.Fatal(err)
			}
			r := Load(dir, quiet())
			var st Status
			for _, f := range r.Files {
				if f.Name == c.file {
					st = f
				}
			}
			if st.Loaded != c.ok || (!c.ok && !strings.Contains(st.Reason, c.reason)) {
				t.Errorf("loaded=%v reason=%q; want loaded=%v reason containing %q", st.Loaded, st.Reason, c.ok, c.reason)
			}
		})
	}
}
