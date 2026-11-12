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
