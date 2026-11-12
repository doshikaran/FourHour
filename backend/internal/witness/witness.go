// Package witness verifies witness log entries (CONTRACTS.md section 6):
// strict parsing, the hash input string rebuilt byte for byte as
// JavaScript's JSON.stringify writes it, SHA-256, and ECDSA P-256
// signatures in WebCrypto's raw r‖s form against a JWK public key.
//
// The hash input is built field by field. encoding/json is never used for
// it: Go writes 1e-05 where JavaScript writes 0.00001, and escapes <, > and
// & where JavaScript does not.
package witness

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"fourhour/backend/internal/ids"
)

// GenesisHash is the prev_hash of entry 1: 64 zeros.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// Reject reasons (CONTRACTS.md section 6).
const (
	ReasonMalformed         = "malformed"
	ReasonBadSignature      = "bad_signature"
	ReasonBrokenChain       = "broken_chain"
	ReasonBadSequence       = "bad_sequence"
	ReasonTimeWentBackwards = "time_went_backwards"
	ReasonStale             = "stale"
)

// Kinds.
var kinds = map[string]bool{"heartbeat": true, "power_lost": true, "power_restored": true, "attest_lost": true, "attest_restored": true}

// JWK is the public key as registered.
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// ParseJWK decodes an EC P-256 public JWK and checks the point is on the curve.
func ParseJWK(raw []byte) (*ecdsa.PublicKey, error) {
	var k JWK
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, fmt.Errorf("jwk: %w", err)
	}
	if k.Kty != "EC" || k.Crv != "P-256" {
		return nil, errors.New("jwk: need kty EC and crv P-256")
	}
	x, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil || len(x) != 32 {
		return nil, errors.New("jwk: x must be 32 bytes, base64url without padding")
	}
	y, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil || len(y) != 32 {
		return nil, errors.New("jwk: y must be 32 bytes, base64url without padding")
	}
	pt := append([]byte{4}, append(x, y...)...)
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pt)
	if err != nil {
		return nil, fmt.Errorf("jwk: %w", err)
	}
	return pub, nil
}

// Entry is one parsed entry. Seq is kept as the float64 JavaScript had, so
// it is written back exactly; it must be a positive integer.
type Entry struct {
	Seq          int64
	DeviceTime   string
	Kind         string
	Charging     bool
	BatteryLevel *float64
	Online       bool
	PrevHash     string
	Hash         string
	Sig          string
}

// ParseError is a malformed entry. Seq is set when it could be read.
type ParseError struct {
	Seq    *int64
	Reason string
}

func (e *ParseError) Error() string { return "malformed entry: " + e.Reason }

// ParseEntry reads one entry strictly: every field present with its JSON
// type, kind one of the five values, hashes 64 lowercase hex characters,
// device_time a UTC timestamp. Anything else is malformed.
func ParseEntry(raw json.RawMessage) (Entry, error) {
	var m map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil || m == nil {
		return Entry{}, &ParseError{Reason: "not a JSON object"}
	}
	var e Entry
	var seqp *int64
	fail := func(why string) (Entry, error) { return Entry{}, &ParseError{Seq: seqp, Reason: why} }

	rawSeq, ok := m["seq"]
	if !ok {
		return fail("seq missing")
	}
	var seqNum json.Number
	if !isJSONNumber(rawSeq) || json.Unmarshal(rawSeq, &seqNum) != nil {
		return fail("seq is not a number")
	}
	f, err := strconv.ParseFloat(seqNum.String(), 64)
	if err != nil || f != math.Trunc(f) || f < 1 || f > 1<<53 {
		return fail("seq is not a positive integer")
	}
	e.Seq = int64(f)
	seqp = &e.Seq

	str := func(k string) (string, error) {
		r, ok := m[k]
		if !ok {
			return "", fmt.Errorf("%s missing", k)
		}
		var s string
		if t := bytes.TrimSpace(r); len(t) == 0 || t[0] != '"' || json.Unmarshal(r, &s) != nil {
			return "", fmt.Errorf("%s is not a string", k)
		}
		if !utf8.ValidString(s) {
			return "", fmt.Errorf("%s is not valid UTF-8", k)
		}
		return s, nil
	}
	boolean := func(k string) (bool, error) {
		r, ok := m[k]
		if !ok {
			return false, fmt.Errorf("%s missing", k)
		}
		switch string(bytes.TrimSpace(r)) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return false, fmt.Errorf("%s is not a boolean", k)
	}
	if e.DeviceTime, err = str("device_time"); err != nil {
		return fail(err.Error())
	}
	if !validTime(e.DeviceTime) {
		return fail("device_time is not an ISO 8601 UTC time with Z")
	}
	if e.Kind, err = str("kind"); err != nil {
		return fail(err.Error())
	}
	if !kinds[e.Kind] {
		return fail("kind is not one of the five kinds")
	}
	if e.Charging, err = boolean("charging"); err != nil {
		return fail(err.Error())
	}
	rb, ok := m["battery_level"]
	if !ok {
		return fail("battery_level missing")
	}
	if string(bytes.TrimSpace(rb)) != "null" {
		var n json.Number
		if !isJSONNumber(rb) || json.Unmarshal(rb, &n) != nil {
			return fail("battery_level is not a number or null")
		}
		v, err := strconv.ParseFloat(n.String(), 64)
		if err != nil || v < 0 || v > 1 {
			return fail("battery_level is not between 0 and 1")
		}
		e.BatteryLevel = &v
	}
	if e.Online, err = boolean("online"); err != nil {
		return fail(err.Error())
	}
	if e.PrevHash, err = str("prev_hash"); err != nil {
		return fail(err.Error())
	}
	if e.Hash, err = str("hash"); err != nil {
		return fail(err.Error())
	}
	if !isHex64(e.PrevHash) || !isHex64(e.Hash) {
		return fail("prev_hash and hash must be 64 lowercase hex characters")
	}
	if e.Sig, err = str("sig"); err != nil {
		return fail(err.Error())
	}
	return e, nil
}

// isJSONNumber reports whether raw is a JSON number literal. encoding/json
// would also accept a quoted string into json.Number, and null into a bool
// or string, so each field's raw token is checked first.
func isJSONNumber(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && (t[0] == '-' || (t[0] >= '0' && t[0] <= '9'))
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func validTime(s string) bool {
	// RFC 3339 with Z, optional fraction; parsed by the caller's clock code.
	if len(s) < 20 || s[len(s)-1] != 'Z' || s[4] != '-' || s[7] != '-' || s[10] != 'T' || s[13] != ':' || s[16] != ':' {
		return false
	}
	_, err := ids.ParseTime(s)
	return err == nil
}

// HashInput is the exact string the hash covers:
// {"seq":N,"device_time":"...","kind":"...","charging":B,"battery_level":N|null,"online":B,"prev_hash":"..."}
func HashInput(e Entry) string {
	var b strings.Builder
	b.WriteString(`{"seq":`)
	b.WriteString(JSNumber(float64(e.Seq)))
	b.WriteString(`,"device_time":`)
	b.WriteString(JSString(e.DeviceTime))
	b.WriteString(`,"kind":`)
	b.WriteString(JSString(e.Kind))
	b.WriteString(`,"charging":`)
	b.WriteString(strconv.FormatBool(e.Charging))
	b.WriteString(`,"battery_level":`)
	if e.BatteryLevel == nil {
		b.WriteString("null")
	} else {
		b.WriteString(JSNumber(*e.BatteryLevel))
	}
	b.WriteString(`,"online":`)
	b.WriteString(strconv.FormatBool(e.Online))
	b.WriteString(`,"prev_hash":`)
	b.WriteString(JSString(e.PrevHash))
	b.WriteString(`}`)
	return b.String()
}

// Hash is the lowercase hex SHA-256 of HashInput.
func Hash(e Entry) string {
	sum := sha256.Sum256([]byte(HashInput(e)))
	return hex.EncodeToString(sum[:])
}

// JSNumber formats a finite float64 as ECMAScript Number::toString does
// (the form JSON.stringify writes): the shortest digits that round-trip,
// plain notation for exponents from -7 to 20, exponent notation otherwise.
func JSNumber(f float64) string {
	if f == 0 {
		return "0" // also -0
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "null" // JSON.stringify writes null for these
	}
	neg := f < 0
	if neg {
		f = -f
	}
	// Shortest round-trip digits and exponent from Go's 'e' format.
	es := strconv.FormatFloat(f, 'e', -1, 64) // d.ddddde±XX
	mant, expStr, _ := strings.Cut(es, "e")
	digits := strings.Replace(mant, ".", "", 1)
	exp, _ := strconv.Atoi(expStr) // FormatFloat always writes a valid exponent
	k := len(digits)
	n := exp + 1 // position of the decimal point relative to the digits
	var out string
	switch {
	case k <= n && n <= 21:
		out = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		out = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		out = "0." + strings.Repeat("0", -n) + digits
	default:
		e := n - 1
		sign := "+"
		if e < 0 {
			sign = "-"
			e = -e
		}
		if k == 1 {
			out = digits + "e" + sign + strconv.Itoa(e)
		} else {
			out = digits[:1] + "." + digits[1:] + "e" + sign + strconv.Itoa(e)
		}
	}
	if neg {
		return "-" + out
	}
	return out
}

// JSString quotes s as JSON.stringify does: escapes ", \ and control
// characters below U+0020 (\b \f \n \r \t, else \u00xx in lowercase hex),
// and nothing else. s must be valid UTF-8 (ParseEntry checks).
func JSString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// VerifySignature checks sig (base64url, no padding, 64 raw bytes r‖s) as
// ECDSA P-256 with SHA-256 over the UTF-8 bytes of message.
func VerifySignature(pub *ecdsa.PublicKey, message, sig string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || len(raw) != 64 {
		return false
	}
	r := new(big.Int).SetBytes(raw[:32])
	s := new(big.Int).SetBytes(raw[32:])
	digest := sha256.Sum256([]byte(message))
	return ecdsa.Verify(pub, digest[:], r, s)
}

// CheckSignature recomputes the entry's hash from its values and verifies
// the signature over that recomputed hash. A changed value, a hash that
// does not match the values, or a bad signature all fail here, so a
// tampered entry is rejected as bad_signature.
func CheckSignature(pub *ecdsa.PublicKey, e Entry) bool {
	h := Hash(e)
	if h != e.Hash {
		return false
	}
	return VerifySignature(pub, h, e.Sig)
}
