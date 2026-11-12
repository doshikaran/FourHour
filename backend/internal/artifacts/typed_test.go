package artifacts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const provJSON = `"provenance":{"generated_at":"2026-11-11T00:00:00Z","git_commit":"x","code":"ml/x.py","data_sources":[],"train_years":[2014,2022],"test_years":[2023,2024],"seed":1,"notes":""}`

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTriggerParamsRealAndFallback(t *testing.T) {
	dir := t.TempDir()
	p, src, err := LoadTriggerParams(dir)
	if err != nil || !src.Fixture || !strings.Contains(src.Reason, "missing") {
		t.Fatalf("fallback: %+v %+v %v", p, src, err)
	}
	if p.GapBridge != 120*time.Minute || p.MergeWindow != 72*time.Hour {
		t.Fatalf("fixture params %+v", p)
	}
	write(t, dir, "trigger_params.json", `{"theta_hi":0.02,"theta_lo":0.005,"gap_bridge_minutes":120,"event_merge_hours":72,"dropout_max_fraction":0.1,"grid":[],`+provJSON+`}`)
	p, src, err = LoadTriggerParams(dir)
	if err != nil || src.Fixture || p.ThetaHi != 0.02 || p.ThetaLo != 0.005 {
		t.Fatalf("real: %+v %+v %v", p, src, err)
	}
	// A null threshold means not computed: fall back, with the reason.
	write(t, dir, "trigger_params.json", `{"theta_hi":null,"theta_lo":0.005,"gap_bridge_minutes":120,"event_merge_hours":72,"dropout_max_fraction":0.1,"grid":[],`+provJSON+`}`)
	if _, src, _ = LoadTriggerParams(dir); !src.Fixture || !strings.Contains(src.Reason, "not computed") {
		t.Fatalf("null: %+v", src)
	}
}

func TestScheduleFromArtifactOrSpec(t *testing.T) {
	dir := t.TempDir()
	s, src := LoadSchedule(dir)
	if src.FromArtifact || s.T4.FloatString(2) != "0.60" {
		t.Fatalf("spec schedule: %v %+v", s.T4, src)
	}
	write(t, dir, "pricing_table.json", `{"model_version":"m","counties":{},"rates":null,"payout_schedule":{"t4":0.55},`+provJSON+`}`)
	s, src = LoadSchedule(dir)
	if !src.FromArtifact || s.T4.FloatString(2) != "0.55" {
		t.Fatalf("artifact schedule: %+v", src)
	}
	// A t24 key is a schedule the backend does not pay: refused, SPEC.md used.
	write(t, dir, "pricing_table.json", `{"model_version":"m","counties":{},"rates":null,"payout_schedule":{"t4":0.55,"t24":1.0},`+provJSON+`}`)
	if s, src = LoadSchedule(dir); src.FromArtifact || !strings.Contains(src.Reason, "t24") || s.T4.FloatString(2) != "0.60" {
		t.Fatalf("t24 schedule: %v %+v", s.T4, src)
	}
}

func TestOfferFor(t *testing.T) {
	dir := t.TempDir()
	if o := CountyOffers(dir, []string{"48201"}); o["48201"].Offered || o["48201"].Reason == nil || *o["48201"].Reason != NoPriceReason {
		t.Fatalf("no file: %+v", o)
	}
	write(t, dir, "pricing_table.json", `{"model_version":"m","rates":null,"payout_schedule":{"t4":0.6},"counties":{`+
		`"48201":{"expected_events_per_year":0.25,"prob_premises_affected":0.4,"credibility_weight":0.1,"offered":true,"not_offered_reason":null},`+
		`"17031":{"expected_events_per_year":0,"prob_premises_affected":0.4,"credibility_weight":0.1,"offered":false,"not_offered_reason":"No qualifying event in this county's credibility group."},`+
		`"12086":{"expected_events_per_year":null,"prob_premises_affected":null,"credibility_weight":null,"offered":null,"not_offered_reason":null}},`+provJSON+`}`)
	o := CountyOffers(dir, []string{"48201", "17031", "12086", "06037"})
	if !o["48201"].Offered || o["48201"].Reason != nil {
		t.Errorf("48201: %+v", o["48201"])
	}
	if o["17031"].Offered || *o["17031"].Reason != "No qualifying event in this county's credibility group." {
		t.Errorf("17031: %+v", o["17031"])
	}
	for _, f := range []string{"12086", "06037"} {
		if o[f].Offered || *o[f].Reason != NoPriceReason {
			t.Errorf("%s: %+v", f, o[f])
		}
	}
}

func TestCountyCoverage(t *testing.T) {
	dir := t.TempDir()
	if c := CountyCoverage(dir); len(c) != 0 {
		t.Fatal(c)
	}
	write(t, dir, "data_quality.json", `{"county_coverage":{"48201":{"data_coverage":0.99},"12086":{"data_coverage":null}},`+provJSON+`}`)
	c := CountyCoverage(dir)
	if c["48201"] == nil || *c["48201"] != 0.99 || c["12086"] != nil {
		t.Fatalf("%v", c)
	}
}

func TestScenariosFixtureAndReal(t *testing.T) {
	dir := t.TempDir()
	all, issues := LoadScenarios(dir)
	if len(issues) != 0 || len(all) != 1 || all[0].ID != "fixture-outage-harris" || !all[0].IsFixture {
		t.Fatalf("%+v %+v", all, issues)
	}
	fx := all[0]
	if len(fx.Series) != 48 || fx.Series[16].Share != nil || fx.Series[16].CustomersOut != nil {
		t.Fatalf("fixture series wrong")
	}
	good := `{"id":"real-one","title":"T","description":"D","fips":"48201","county_customers":10,"start":"2024-05-16T00:00:00Z","end":"2024-05-16T00:15:00Z","series":[{"t":"2024-05-16T00:00:00Z","customers_out":1,"share":0.1},{"t":"2024-05-16T00:15:00Z","customers_out":null,"share":null}],"source":{"dataset":"d","url":"u","file":"f","sha256":"s"},` + provJSON + `}`
	write(t, dir, "scenarios/real-one.json", good)
	write(t, dir, "scenarios/bad.json", strings.Replace(good, `"real-one"`, `"other"`, 1))
	write(t, dir, "scenarios/half-null.json", strings.Replace(strings.Replace(good, `"real-one"`, `"half-null"`, 1), `"customers_out":null`, `"customers_out":3`, 1))
	all, issues = LoadScenarios(dir)
	if len(all) != 2 || all[0].ID != "real-one" || all[0].IsFixture || len(issues) != 2 {
		t.Fatalf("%d scenarios, issues %+v", len(all), issues)
	}
	if _, err := FindScenario(dir, "nope"); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
}
