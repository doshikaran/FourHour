package spikeenv

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadOK(t *testing.T) {
	p := write(t, "# comment\n\nexport PAYPAL_BASE_URL=https://api-m.sandbox.paypal.com\nPAYPAL_CLIENT_ID=\"abc\"\nPAYPAL_CLIENT_SECRET='s3cr=et'\nOTHER=\n")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.ClientID != "abc" || c.ClientSecret != "s3cr=et" {
		t.Errorf("got %+v", c)
	}
	if strings.Contains(c.String(), "s3cr=et") {
		t.Error("String leaks secret")
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), ".env"))
	if err == nil || !strings.Contains(err.Error(), "no .env file at") || !strings.Contains(err.Error(), ".env.example") {
		t.Errorf("got %v", err)
	}
}

func TestLoadRefusesNonSandbox(t *testing.T) {
	for _, base := range []string{"https://api-m.paypal.com", "https://api-m.sandbox.paypal.com/", "http://localhost"} {
		p := write(t, "PAYPAL_BASE_URL="+base+"\nPAYPAL_CLIENT_ID=a\nPAYPAL_CLIENT_SECRET=b\n")
		if _, err := Load(p); !errors.Is(err, ErrNotSandbox) {
			t.Errorf("%s: want ErrNotSandbox, got %v", base, err)
		}
	}
	// Empty .env from .env.example: refused as non-sandbox first.
	p := write(t, "PAYPAL_BASE_URL=\nPAYPAL_CLIENT_ID=\nPAYPAL_CLIENT_SECRET=\n")
	if _, err := Load(p); !errors.Is(err, ErrNotSandbox) {
		t.Errorf("empty: got %v", err)
	}
}

func TestLoadMissingCredentials(t *testing.T) {
	p := write(t, "PAYPAL_BASE_URL=https://api-m.sandbox.paypal.com\nPAYPAL_CLIENT_ID=a\n")
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "PAYPAL_CLIENT_SECRET") || strings.Contains(err.Error(), "PAYPAL_CLIENT_ID,") {
		t.Errorf("got %v", err)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse(strings.NewReader("NOEQUALS\n")); err == nil {
		t.Error("want error")
	}
}

func TestFindEnvFile(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "spikes", "paypal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CONTRACTS.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := FindEnvFile(sub)
	if err != nil || got != filepath.Join(root, ".env") {
		t.Errorf("got %q %v", got, err)
	}
}
