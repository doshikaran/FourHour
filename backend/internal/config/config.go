// Package config is the only package in the backend that reads the
// environment (CLAUDE.md, CONTRACTS.md section 2).
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// SandboxBaseURL is the only PayPal base URL the service accepts.
const SandboxBaseURL = "https://api-m.sandbox.paypal.com"

// Defaults from CONTRACTS.md section 2.
const (
	DefaultDatabaseURL  = "./fourhour.db"
	DefaultArtifactsDir = "../artifacts"
	DefaultPort         = "8080"
)

// ErrNotSandbox is returned when PAYPAL_BASE_URL is not exactly the sandbox URL.
var ErrNotSandbox = errors.New("refusing to start: PAYPAL_BASE_URL must be exactly " + SandboxBaseURL)

// Config holds every backend environment variable from CONTRACTS.md section 2.
// VITE_API_BASE_URL is frontend-only and not read here.
type Config struct {
	PayPalBaseURL      string // PAYPAL_BASE_URL
	PayPalClientID     string // PAYPAL_CLIENT_ID
	PayPalClientSecret string // PAYPAL_CLIENT_SECRET; never printed
	PayPalWebhookID    string // PAYPAL_WEBHOOK_ID
	AppBaseURL         string // APP_BASE_URL
	FrontendURL        string // FRONTEND_URL
	DatabaseURL        string // DATABASE_URL, a SQLite file path
	ArtifactsDir       string // ARTIFACTS_DIR
	Port               string // PORT, digits only, 1-65535
	DemoVaultToken     string // DEMO_VAULT_TOKEN; never printed
	SandboxMemberEmail string // SANDBOX_MEMBER_EMAIL
	SandboxPayoutEmail string // SANDBOX_PAYOUT_EMAIL
}

// IntegrationFlag is the environment variable that turns on the opt-in
// sandbox integration test. It is a test switch, not a service setting.
const IntegrationFlag = "FOURHOUR_SANDBOX_IT"

// IntegrationEnabled reports whether IntegrationFlag is set to "1".
func IntegrationEnabled(lookup LookupFunc) bool {
	v, ok := lookup(IntegrationFlag)
	return ok && v == "1"
}

// LookupFunc has the signature of os.LookupEnv.
type LookupFunc func(key string) (string, bool)

// OSLookup reads the real process environment. It is the only reference to
// the process environment in the backend.
func OSLookup(key string) (string, bool) { return os.LookupEnv(key) }

// Load builds a Config. If envFile is not empty, that file is read first in
// KEY=VALUE form and values from lookup override it. A non-sandbox
// PAYPAL_BASE_URL is an error.
func Load(lookup LookupFunc, envFile string) (Config, error) {
	file := map[string]string{}
	if envFile != "" {
		f, err := os.Open(envFile)
		if err != nil {
			return Config{}, fmt.Errorf("open env file: %w", err)
		}
		defer f.Close()
		file, err = ParseDotEnv(f)
		if err != nil {
			return Config{}, fmt.Errorf("parse env file %s: %w", envFile, err)
		}
	}
	get := func(k string) string {
		if v, ok := lookup(k); ok && v != "" {
			return v
		}
		return file[k]
	}
	c := Config{
		PayPalBaseURL:      get("PAYPAL_BASE_URL"),
		PayPalClientID:     get("PAYPAL_CLIENT_ID"),
		PayPalClientSecret: get("PAYPAL_CLIENT_SECRET"),
		PayPalWebhookID:    get("PAYPAL_WEBHOOK_ID"),
		AppBaseURL:         get("APP_BASE_URL"),
		FrontendURL:        get("FRONTEND_URL"),
		DatabaseURL:        get("DATABASE_URL"),
		ArtifactsDir:       get("ARTIFACTS_DIR"),
		Port:               get("PORT"),
		DemoVaultToken:     get("DEMO_VAULT_TOKEN"),
		SandboxMemberEmail: get("SANDBOX_MEMBER_EMAIL"),
		SandboxPayoutEmail: get("SANDBOX_PAYOUT_EMAIL"),
	}
	if c.Port == "" {
		c.Port = DefaultPort
	}
	if c.DatabaseURL == "" {
		c.DatabaseURL = DefaultDatabaseURL
	}
	if c.ArtifactsDir == "" {
		c.ArtifactsDir = DefaultArtifactsDir
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate enforces the startup rules. The sandbox rule is absolute
// (CLAUDE.md rule 6). PayPal credentials are checked when the PayPal client
// is built; the other optional values are reported by Missing.
func (c Config) Validate() error {
	if c.PayPalBaseURL != SandboxBaseURL {
		if c.PayPalBaseURL == "" {
			return fmt.Errorf("%w (it is not set)", ErrNotSandbox)
		}
		return fmt.Errorf("%w (got %q)", ErrNotSandbox, c.PayPalBaseURL)
	}
	if n, err := strconv.Atoi(c.Port); err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != c.Port {
		return fmt.Errorf("PORT must be a number from 1 to 65535: %q", c.Port)
	}
	if strings.ContainsRune(c.DatabaseURL, '?') {
		return fmt.Errorf("DATABASE_URL must be a plain file path without '?': %q", c.DatabaseURL)
	}
	return nil
}

// Missing lists optional variables that are empty. The service logs them at
// startup: without PAYPAL_WEBHOOK_ID webhooks are stored unverified, without
// APP_BASE_URL no vault setup token can be created, without FRONTEND_URL
// there is no CORS origin and no redirect target.
func (c Config) Missing() []string {
	var out []string
	for _, kv := range []struct{ k, v string }{
		{"PAYPAL_CLIENT_ID", c.PayPalClientID},
		{"PAYPAL_CLIENT_SECRET", c.PayPalClientSecret},
		{"PAYPAL_WEBHOOK_ID", c.PayPalWebhookID},
		{"APP_BASE_URL", c.AppBaseURL},
		{"FRONTEND_URL", c.FrontendURL},
	} {
		if kv.v == "" {
			out = append(out, kv.k)
		}
	}
	return out
}

// String never includes the client secret.
func (c Config) String() string {
	return fmt.Sprintf("Config{PayPalBaseURL:%s PayPalClientID:%s PayPalClientSecret:%s PayPalWebhookID:%s AppBaseURL:%s FrontendURL:%s DatabaseURL:%s ArtifactsDir:%s Port:%s DemoVaultToken:%s SandboxMemberEmail:%s SandboxPayoutEmail:%s}",
		c.PayPalBaseURL, c.PayPalClientID, redact(c.PayPalClientSecret), c.PayPalWebhookID, c.AppBaseURL, c.FrontendURL, c.DatabaseURL, c.ArtifactsDir, c.Port, redact(c.DemoVaultToken), c.SandboxMemberEmail, c.SandboxPayoutEmail)
}

func redact(v string) string {
	if v == "" {
		return "<unset>"
	}
	return "<redacted>"
}

// GoString never includes the client secret.
func (c Config) GoString() string { return c.String() }

// ParseDotEnv reads KEY=VALUE lines. Blank lines and # comments are skipped,
// an "export " prefix is allowed, and one pair of matching surrounding quotes
// is removed. There is no variable expansion.
func ParseDotEnv(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", n)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" {
			return nil, fmt.Errorf("line %d: empty key", n)
		}
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return out, nil
}
