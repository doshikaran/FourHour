package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mapLookup(m map[string]string) LookupFunc {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(mapLookup(map[string]string{"PAYPAL_BASE_URL": SandboxBaseURL}), "")
	if err != nil {
		t.Fatal(err)
	}
	if c.DatabaseURL != "./fourhour.db" || c.ArtifactsDir != "../artifacts" || c.Port != "8080" {
		t.Errorf("defaults: %+v", c)
	}
	if len(c.Missing()) != 5 {
		t.Errorf("missing: %v", c.Missing())
	}
}

func TestLoadRefusesNonSandbox(t *testing.T) {
	for _, u := range []string{"", "https://api-m.paypal.com", "https://api-m.sandbox.paypal.com/", "HTTPS://api-m.sandbox.paypal.com", " https://api-m.sandbox.paypal.com", "http://localhost:9999"} {
		_, err := Load(mapLookup(map[string]string{"PAYPAL_BASE_URL": u}), "")
		if !errors.Is(err, ErrNotSandbox) {
			t.Errorf("%q: want ErrNotSandbox, got %v", u, err)
		}
	}
}

func TestEnvOverridesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte("PAYPAL_BASE_URL=https://api-m.paypal.com\nPAYPAL_CLIENT_ID=fromfile\nARTIFACTS_DIR=/a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// File alone is production: refused.
	if _, err := Load(mapLookup(nil), p); !errors.Is(err, ErrNotSandbox) {
		t.Errorf("file-only: %v", err)
	}
	c, err := Load(mapLookup(map[string]string{"PAYPAL_BASE_URL": SandboxBaseURL, "ARTIFACTS_DIR": "/b"}), p)
	if err != nil {
		t.Fatal(err)
	}
	if c.PayPalClientID != "fromfile" || c.ArtifactsDir != "/b" {
		t.Errorf("got %+v", c)
	}
}

func TestMissingEnvFileIsError(t *testing.T) {
	_, err := Load(mapLookup(map[string]string{"PAYPAL_BASE_URL": SandboxBaseURL}), filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Error("want error")
	}
}

func TestStringRedactsSecret(t *testing.T) {
	c := Config{PayPalClientSecret: "hunter2"}
	if strings.Contains(c.String(), "hunter2") || strings.Contains(c.GoString(), "hunter2") {
		t.Error("secret leaked")
	}
}

func TestPort(t *testing.T) {
	c, err := Load(mapLookup(map[string]string{"PAYPAL_BASE_URL": SandboxBaseURL, "PORT": "10000"}), "")
	if err != nil || c.Port != "10000" {
		t.Errorf("got %q %v", c.Port, err)
	}
	for _, p := range []string{"0", "65536", "80a", "-1", " 80", "080"} {
		if _, err := Load(mapLookup(map[string]string{"PAYPAL_BASE_URL": SandboxBaseURL, "PORT": p}), ""); err == nil {
			t.Errorf("PORT %q: want error", p)
		}
	}
}

func TestDatabaseURLWithQueryRejected(t *testing.T) {
	_, err := Load(mapLookup(map[string]string{"PAYPAL_BASE_URL": SandboxBaseURL, "DATABASE_URL": "x.db?mode=ro"}), "")
	if err == nil {
		t.Error("want error")
	}
}
