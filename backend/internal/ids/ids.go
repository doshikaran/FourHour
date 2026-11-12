// Package ids makes the contract's IDs and timestamps (CONTRACTS.md section 1).
package ids

import (
	"crypto/rand"
	"fmt"
	"time"
)

// Prefixes from CONTRACTS.md section 1.
const (
	Member       = "mem_"
	Policy       = "pol_"
	Charge       = "chg_"
	Event        = "evt_"
	Device       = "dev_"
	Claim        = "clm_"
	Payout       = "pay_"
	Refund       = "rfd_"
	WitnessEntry = "wen_"
	LedgerEntry  = "led_"
)

const alphabet = "abcdefghijklmnopqrstuvwxyz234567"

// New returns prefix plus 12 lowercase base32 characters from crypto/rand.
func New(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read never returns an error on supported platforms
		// (Go 1.24+); a failure here means the OS has no entropy source.
		panic(fmt.Sprintf("ids: crypto/rand failed: %v", err))
	}
	out := make([]byte, 0, len(prefix)+12)
	out = append(out, prefix...)
	for _, c := range b {
		out = append(out, alphabet[c&31])
	}
	return string(out)
}

// TimeLayout is ISO 8601 UTC with a Z suffix and whole seconds.
const TimeLayout = "2006-01-02T15:04:05Z"

// FormatTime writes t in the contract's time format.
func FormatTime(t time.Time) string { return t.UTC().Format(TimeLayout) }

// ParseTime reads the contract's time format. It accepts fractional seconds
// and requires the Z suffix.
func ParseTime(s string) (time.Time, error) {
	if len(s) == 0 || s[len(s)-1] != 'Z' {
		return time.Time{}, fmt.Errorf("time %q: must be UTC with a Z suffix", s)
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("time %q: %w", s, err)
	}
	return t.UTC(), nil
}
