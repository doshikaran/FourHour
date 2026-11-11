// Package spikeenv loads PayPal credentials for the spikes from the .env file
// at the repo root. It reads only that file, never the process environment,
// so a spike run is determined by the file alone.
package spikeenv

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"fourhour/spikes/paypal/paypal"
)

// Config is what the spikes need. ClientSecret must never be printed.
type Config struct {
	BaseURL      string
	ClientID     string
	ClientSecret string
	EnvFile      string
}

// ErrNotSandbox is returned when PAYPAL_BASE_URL is not the sandbox URL.
var ErrNotSandbox = errors.New("refusing to run: PAYPAL_BASE_URL must be exactly " + paypal.SandboxBaseURL)

// rootMarker identifies the repo root when searching upward.
const rootMarker = "CONTRACTS.md"

// FindEnvFile walks up from dir to the repo root (the directory holding
// CONTRACTS.md) and returns the path of .env there. The file may not exist.
func FindEnvFile(dir string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	for {
		if _, err := os.Stat(filepath.Join(d, rootMarker)); err == nil {
			return filepath.Join(d, ".env"), nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("could not find the repo root (a directory containing %s) above %s; pass -env-file", rootMarker, dir)
		}
		d = parent
	}
}

// Parse reads KEY=VALUE lines. Blank lines and lines starting with # are
// skipped, an optional "export " prefix is allowed, and one pair of matching
// surrounding quotes is removed. There is no variable expansion.
func Parse(r io.Reader) (map[string]string, error) {
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
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
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

// Load reads path and validates it. The sandbox check runs before the
// credential check, so a production URL is refused even with no credentials.
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("no .env file at %s: copy .env.example to .env at the repo root and fill in PAYPAL_BASE_URL, PAYPAL_CLIENT_ID and PAYPAL_CLIENT_SECRET", path)
		}
		return Config{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	vals, err := Parse(f)
	if err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return fromValues(vals, path)
}

func fromValues(vals map[string]string, path string) (Config, error) {
	base := vals["PAYPAL_BASE_URL"]
	if base == "" {
		return Config{}, fmt.Errorf("%w: PAYPAL_BASE_URL is missing or empty in %s", ErrNotSandbox, path)
	}
	if base != paypal.SandboxBaseURL {
		return Config{}, fmt.Errorf("%w: %s has %q", ErrNotSandbox, path, base)
	}
	var missing []string
	for _, k := range []string{"PAYPAL_CLIENT_ID", "PAYPAL_CLIENT_SECRET"} {
		if vals[k] == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing or empty in %s: %s", path, strings.Join(missing, ", "))
	}
	return Config{BaseURL: base, ClientID: vals["PAYPAL_CLIENT_ID"], ClientSecret: vals["PAYPAL_CLIENT_SECRET"], EnvFile: path}, nil
}

// String never includes the secret.
func (c Config) String() string {
	return fmt.Sprintf("spikeenv.Config{BaseURL:%s ClientID:%s ClientSecret:<redacted> EnvFile:%s}", c.BaseURL, c.ClientID, c.EnvFile)
}

// GoString never includes the secret.
func (c Config) GoString() string { return c.String() }
