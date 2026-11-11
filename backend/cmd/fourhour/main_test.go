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
