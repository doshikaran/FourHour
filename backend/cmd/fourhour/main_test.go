package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"fourhour/backend/internal/config"
)

func lookup(m map[string]string) config.LookupFunc {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestRefusesNonSandboxWithNoSideEffects(t *testing.T) {
	for _, base := range []string{"https://api-m.paypal.com", "", "https://api-m.sandbox.paypal.com/"} {
		dbPath := filepath.Join(t.TempDir(), "x.db")
		var logs bytes.Buffer
		err := run(context.Background(), []string{"-addr", "127.0.0.1:0"}, lookup(map[string]string{"PAYPAL_BASE_URL": base, "DATABASE_URL": dbPath}), &logs, func(string) {
			t.Errorf("%q: service started listening", base)
		})
		if !errors.Is(err, config.ErrNotSandbox) {
			t.Errorf("%q: want ErrNotSandbox, got %v", base, err)
		}
		if _, statErr := os.Stat(dbPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("%q: database file was created before refusing", base)
		}
	}
}

func TestStartsAndServesHealthWithoutArtifactsDir(t *testing.T) {
	dir := t.TempDir()
	env := lookup(map[string]string{
		"PAYPAL_BASE_URL": config.SandboxBaseURL,
		"DATABASE_URL":    filepath.Join(dir, "with space", "..", "fourhour.db"),
		"ARTIFACTS_DIR":   filepath.Join(dir, "no-such-dir"),
	})
	if err := os.MkdirAll(filepath.Join(dir, "with space"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	addrCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, []string{"-addr", "127.0.0.1:0"}, env, io.Discard, func(a string) { addrCh <- a })
	}()

	var addr string
	select {
	case addr = <-addrCh:
	case err := <-errCh:
		t.Fatalf("run exited: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for listen")
	}
	resp, err := http.Get("http://" + addr + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(body) != `{"status":"ok","paypal_env":"sandbox","artifacts_loaded":[]}`+"\n" {
		t.Errorf("%d %s", resp.StatusCode, body)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for shutdown")
	}
}

func TestHealthReportsFixtureArtifacts(t *testing.T) {
	dir := t.TempDir()
	env := lookup(map[string]string{
		"PAYPAL_BASE_URL": config.SandboxBaseURL,
		"DATABASE_URL":    filepath.Join(dir, "fourhour.db"),
		"ARTIFACTS_DIR":   "../../internal/artifacts/testdata/fixture-artifacts",
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, []string{"-addr", "127.0.0.1:0"}, env, io.Discard, func(a string) { addrCh <- a })
	}()
	var addr string
	select {
	case addr = <-addrCh:
	case err := <-errCh:
		t.Fatalf("run exited: %v", err)
	}
	resp, err := http.Get("http://" + addr + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	want := `{"status":"ok","paypal_env":"sandbox","artifacts_loaded":["trigger_params","pricing_table","backtest","solvency","basis_risk","data_quality"]}` + "\n"
	if string(body) != want {
		t.Errorf("got %s", body)
	}
	cancel()
	if err := <-errCh; err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestListensOnPORTWhenAddrNotGiven(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	env := lookup(map[string]string{
		"PAYPAL_BASE_URL": config.SandboxBaseURL,
		"DATABASE_URL":    filepath.Join(t.TempDir(), "fourhour.db"),
		"PORT":            strconv.Itoa(port),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() { errCh <- run(ctx, nil, env, io.Discard, func(a string) { addrCh <- a }) }()
	select {
	case a := <-addrCh:
		if !strings.HasSuffix(a, ":"+strconv.Itoa(port)) {
			t.Errorf("listening on %s, want port %d", a, port)
		}
	case err := <-errCh:
		t.Fatalf("run exited: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout")
	}
	cancel()
	if err := <-errCh; err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestSeedRefusesWithoutVaultTokenOrMemberEmail(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "seed.db")
	base := map[string]string{"PAYPAL_BASE_URL": config.SandboxBaseURL, "DATABASE_URL": dbPath}
	for _, missing := range []string{"DEMO_VAULT_TOKEN", "SANDBOX_MEMBER_EMAIL"} {
		env := map[string]string{"DEMO_VAULT_TOKEN": "tok-secret", "SANDBOX_MEMBER_EMAIL": "m@example.com"}
		for k, v := range base {
			env[k] = v
		}
		delete(env, missing)
		var out bytes.Buffer
		err := run(context.Background(), []string{"seed"}, lookup(env), &out, nil)
		if err == nil || !strings.Contains(err.Error(), missing) {
			t.Fatalf("missing %s: err %v", missing, err)
		}
		if _, statErr := os.Stat(dbPath); statErr == nil {
			t.Fatalf("missing %s: database created before refusing", missing)
		}
	}
}

func TestSeedIsIdempotentAndNeverPrintsToken(t *testing.T) {
	env := map[string]string{"PAYPAL_BASE_URL": config.SandboxBaseURL, "DATABASE_URL": filepath.Join(t.TempDir(), "seed.db"),
		"DEMO_VAULT_TOKEN": "tok-secret-xyz", "SANDBOX_MEMBER_EMAIL": "m@example.com", "SANDBOX_PAYOUT_EMAIL": "p@example.com"}
	var out1, out2 bytes.Buffer
	if err := run(context.Background(), []string{"seed"}, lookup(env), &out1, nil); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"seed"}, lookup(env), &out2, nil); err != nil {
		t.Fatal(err)
	}
	pol := func(s string) string {
		i := strings.Index(s, "policy=")
		return strings.Fields(s[i:])[0]
	}
	if !strings.Contains(out1.String(), "seed: created") || !strings.Contains(out2.String(), "seed: existing") || pol(out1.String()) != pol(out2.String()) {
		t.Fatalf("%s\n%s", out1.String(), out2.String())
	}
	if strings.Contains(out1.String()+out2.String(), "tok-secret-xyz") || !strings.Contains(out1.String(), "cover_cents=100 premium_cents=100 model_version=test-cover payout_email=p@example.com") {
		t.Fatalf("%s", out1.String())
	}
}

// startServer runs the service until the returned stop is called.
func startServer(t *testing.T, env config.LookupFunc, logs io.Writer) (addr string, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	addrCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() { errCh <- run(ctx, []string{"-addr", "127.0.0.1:0"}, env, logs, func(a string) { addrCh <- a }) }()
	select {
	case addr = <-addrCh:
	case err := <-errCh:
		cancel()
		t.Fatalf("run exited: %v", err)
	case <-time.After(20 * time.Second):
		cancel()
		t.Fatal("timeout waiting for listen")
	}
	return addr, func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("shutdown: %v", err)
		}
	}
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

func post(t *testing.T, url, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

// TestStartupDemoPolicy: with DEMO_VAULT_TOKEN set, startup creates the demo
// policy; the first premium charge fails here (no PayPal credentials, so no
// PayPal call is possible) and is logged, and the server still serves. A
// restart on the same database finds the same policy. The charge itself is
// tested against the fake PayPal in internal/service/demo_test.go.
func TestStartupDemoPolicy(t *testing.T) {
	dir := t.TempDir()
	art := filepath.Join(dir, "artifacts")
	if err := os.MkdirAll(art, 0o755); err != nil {
		t.Fatal(err)
	}
	pricing := `{"model_version":"m-main","counties":{` +
		`"48201":{"expected_events_per_year":0.61,"prob_premises_affected":0.22,"credibility_weight":0.74,"offered":true,"not_offered_reason":null},` +
		`"17031":{"expected_events_per_year":0,"prob_premises_affected":0.22,"credibility_weight":0,"offered":false,"not_offered_reason":"No qualifying event in this county's credibility group."}},` +
		`"rates":{"risk_margin_rate":0.35,"expense_load_cents":150},"payout_schedule":{"t4":0.6},` +
		`"provenance":{"generated_at":"2026-11-11T00:00:00Z","git_commit":"x","code":"test","data_sources":[],"train_years":[2014,2022],"test_years":[2023,2024],"seed":1,"notes":"test input"}}`
	if err := os.WriteFile(filepath.Join(art, "pricing_table.json"), []byte(pricing), 0o600); err != nil {
		t.Fatal(err)
	}
	env := lookup(map[string]string{
		"PAYPAL_BASE_URL": config.SandboxBaseURL, "DATABASE_URL": filepath.Join(dir, "fourhour.db"), "ARTIFACTS_DIR": art,
		"DEMO_VAULT_TOKEN": "tok-secret-demo", "SANDBOX_MEMBER_EMAIL": "m@example.com", "SANDBOX_PAYOUT_EMAIL": "p@example.com",
	})

	var logs bytes.Buffer
	addr, stop := startServer(t, env, &logs)
	code, demo1 := get(t, "http://"+addr+"/api/demo")
	if code != 200 || !strings.Contains(demo1, `"fips":"48201","cover_cents":5000,"premium_cents":195}`) || strings.Contains(demo1, `"policy_id":null`) {
		t.Fatalf("%d %s", code, demo1)
	}
	if code, body := get(t, "http://"+addr+"/api/counties"); code != 200 ||
		!strings.Contains(body, `{"fips":"48201","name":"Harris County","state":"TX","data_coverage":null,"offered":true,"not_offered_reason":null}`) ||
		!strings.Contains(body, `{"fips":"17031","name":"Cook County","state":"IL","data_coverage":null,"offered":false,"not_offered_reason":"No qualifying event in this county's credibility group."}`) ||
		!strings.Contains(body, `{"fips":"12086","name":"Miami-Dade County","state":"FL","data_coverage":null,"offered":false,"not_offered_reason":"No price has been computed for this county."}`) {
		t.Fatalf("counties %d %s", code, body)
	}
	if code, body := post(t, "http://"+addr+"/api/quotes", `{"fips":"17031","cover_cents":5000}`); code != 422 ||
		body != `{"error":{"code":"county_not_offered","message":"No qualifying event in this county's credibility group.","paypal_debug_id":null}}`+"\n" {
		t.Fatalf("quote not offered %d %s", code, body)
	}
	if code, body := post(t, "http://"+addr+"/api/quotes", `{"fips":"48201","cover_cents":100}`); code != 400 || !strings.Contains(body, "invalid_cover") {
		t.Fatalf("test cover quote %d %s", code, body)
	}
	stop()
	if !strings.Contains(logs.String(), "demo policy failed; the server continues") || !strings.Contains(logs.String(), "paypal_unavailable") {
		t.Fatalf("charge failure not logged:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), "tok-secret-demo") {
		t.Fatal("logs leak the vault token")
	}

	addr, stop = startServer(t, env, io.Discard)
	_, demo2 := get(t, "http://"+addr+"/api/demo")
	stop()
	if demo1 != demo2 {
		t.Fatalf("restart changed the demo policy:\n%s\n%s", demo1, demo2)
	}
}

func TestNoDemoPolicyWithoutToken(t *testing.T) {
	env := lookup(map[string]string{"PAYPAL_BASE_URL": config.SandboxBaseURL, "DATABASE_URL": filepath.Join(t.TempDir(), "fourhour.db")})
	addr, stop := startServer(t, env, io.Discard)
	defer stop()
	if code, body := get(t, "http://"+addr+"/api/demo"); code != 200 || body != `{"policy_id":null}`+"\n" {
		t.Fatalf("%d %s", code, body)
	}
}

// TestUnknownDeviceIs404: both witness entry endpoints answer 404
// device_not_found in the contract error shape for an unknown device_id.
func TestUnknownDeviceIs404(t *testing.T) {
	env := lookup(map[string]string{"PAYPAL_BASE_URL": config.SandboxBaseURL, "DATABASE_URL": filepath.Join(t.TempDir(), "fourhour.db")})
	addr, stop := startServer(t, env, io.Discard)
	defer stop()
	want := `{"error":{"code":"device_not_found","message":"No device dev_nope.","paypal_debug_id":null}}` + "\n"
	if code, body := post(t, "http://"+addr+"/api/witness/entries", `{"device_id":"dev_nope","entries":[]}`); code != 404 || body != want {
		t.Errorf("POST entries: %d %s", code, body)
	}
	if code, body := get(t, "http://"+addr+"/api/witness/devices/dev_nope/entries?limit=200"); code != 404 || body != want {
		t.Errorf("GET entries: %d %s", code, body)
	}
}
