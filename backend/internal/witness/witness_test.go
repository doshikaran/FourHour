package witness

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
)

// testdata/fixture-s5-mac-chrome.json is a copy of
// frontend/spikes/fixtures/s5-mac-chrome.json: entries produced by real
// WebCrypto in Chrome 157 on a MacBook (spike S5).
type fixture struct {
	PublicKeyJWK  json.RawMessage   `json:"public_key_jwk"`
	GenesisHash   string            `json:"genesis_hash"`
	Entries       []json.RawMessage `json:"entries"`
	HashInputs    []string          `json:"hash_inputs"`
	TestSignature struct {
		Message string `json:"message"`
		Sig     string `json:"sig"`
	} `json:"test_signature"`
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	b, err := os.ReadFile("testdata/fixture-s5-mac-chrome.json")
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Entries) != 7 || len(f.HashInputs) != 7 {
		t.Fatalf("fixture has %d entries", len(f.Entries))
	}
	return f
}

func TestFixtureCopyMatchesFrontendOriginal(t *testing.T) {
	orig, err := os.ReadFile("../../../frontend/spikes/fixtures/s5-mac-chrome.json")
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("frontend fixture not present")
	}
	if err != nil {
		t.Fatal(err)
	}
	cp, err := os.ReadFile("testdata/fixture-s5-mac-chrome.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(orig, cp) {
		t.Fatal("backend copy differs from frontend/spikes/fixtures/s5-mac-chrome.json; recopy it")
	}
}

func TestJSNumber(t *testing.T) {
	a, b := 0.1, 0.2 // variables: a constant 0.1+0.2 is folded exactly to 0.3
	cases := []struct {
		in   float64
		want string
	}{
		{1, "1"}, {0.5, "0.5"}, {0.83, "0.83"}, {0.29, "0.29"}, {0, "0"}, {math.Copysign(0, -1), "0"},
		{0.00001, "0.00001"}, {0.000001, "0.000001"}, {1e-7, "1e-7"}, {1.5e-7, "1.5e-7"},
		{a + b, "0.30000000000000004"}, {12, "12"}, {100, "100"}, {123.456, "123.456"},
		{1e21, "1e+21"}, {1e20, "100000000000000000000"}, {1.25e21, "1.25e+21"}, {-2.5, "-2.5"},
		{9007199254740993, "9007199254740992"},
	}
	for _, c := range cases {
		if got := JSNumber(c.in); got != c.want {
			t.Errorf("JSNumber(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestJSString(t *testing.T) {
	cases := map[string]string{
		"abc": `"abc"`, `a"b`: `"a\"b"`, `a\b`: `"a\\b"`, "<&>": `"<&>"`, "\n\t\r\b\f": `"\n\t\r\b\f"`,
		"\x01\x1f": `"\u0001\u001f"`, "é€😀": `"é€😀"`, " ": "\" \"",
	}
	for in, want := range cases {
		if got := JSString(in); got != want {
			t.Errorf("JSString(%q) = %s, want %s", in, got, want)
		}
	}
}

// The hash input rebuilt from parsed values must equal the browser's
// string byte for byte, including battery_level 1, 0.5, 0.83, null,
// 0.00001 and 0.30000000000000004.
func TestFixtureHashInputsAndSignatures(t *testing.T) {
	f := loadFixture(t)
	pub, err := ParseJWK(f.PublicKeyJWK)
	if err != nil {
		t.Fatal(err)
	}
	prev := GenesisHash
	seen := map[string]bool{}
	for i, raw := range f.Entries {
		e, err := ParseEntry(raw)
		if err != nil {
			t.Fatalf("entry %d: %v", i+1, err)
		}
		if got := HashInput(e); got != f.HashInputs[i] {
			t.Errorf("entry %d hash input\n got %s\nwant %s", i+1, got, f.HashInputs[i])
		}
		if Hash(e) != e.Hash {
			t.Errorf("entry %d: hash mismatch", i+1)
		}
		if e.PrevHash != prev {
			t.Errorf("entry %d: chain broken", i+1)
		}
		prev = e.Hash
		if !CheckSignature(pub, e) {
			t.Errorf("entry %d: signature does not verify", i+1)
		}
		bl := "null"
		if e.BatteryLevel != nil {
			bl = JSNumber(*e.BatteryLevel)
		}
		seen[bl] = true
	}
	for _, want := range []string{"1", "0.5", "0.83", "null", "0.00001", "0.30000000000000004"} {
		if !seen[want] {
			t.Errorf("fixture lacks battery_level %s", want)
		}
	}
	if !VerifySignature(pub, f.TestSignature.Message, f.TestSignature.Sig) {
		t.Error("test_signature does not verify")
	}
	if VerifySignature(pub, f.TestSignature.Message+"x", f.TestSignature.Sig) {
		t.Error("tampered message verifies")
	}
}

func TestTamperedEntryFailsSignature(t *testing.T) {
	f := loadFixture(t)
	pub, _ := ParseJWK(f.PublicKeyJWK)
	mutations := []func(*Entry){
		func(e *Entry) { v := 0.84; e.BatteryLevel = &v },
		func(e *Entry) { e.Kind = "heartbeat" },
		func(e *Entry) { e.Charging = !e.Charging },
		func(e *Entry) { e.DeviceTime = "2026-11-11T18:18:16Z" },
		func(e *Entry) { e.Online = !e.Online },
		func(e *Entry) { e.Sig = e.Sig[:len(e.Sig)-2] + "AA" },
		func(e *Entry) { e.Hash = strings.Repeat("a", 64) },
	}
	for i, m := range mutations {
		e, err := ParseEntry(f.Entries[2])
		if err != nil {
			t.Fatal(err)
		}
		m(&e)
		if CheckSignature(pub, e) {
			t.Errorf("mutation %d still verifies", i)
		}
	}
}

func TestParseEntryMalformed(t *testing.T) {
	base := `{"seq":1,"device_time":"2026-11-11T18:18:15Z","kind":"heartbeat","charging":true,"battery_level":0.5,"online":true,"prev_hash":"` + GenesisHash + `","hash":"` + GenesisHash + `","sig":"x"}`
	if _, err := ParseEntry(json.RawMessage(base)); err != nil {
		t.Fatalf("base: %v", err)
	}
	bad := map[string]string{
		"missing seq":         strings.Replace(base, `"seq":1,`, ``, 1),
		"seq string":          strings.Replace(base, `"seq":1`, `"seq":"1"`, 1),
		"seq fraction":        strings.Replace(base, `"seq":1`, `"seq":1.5`, 1),
		"seq zero":            strings.Replace(base, `"seq":1`, `"seq":0`, 1),
		"charging string":     strings.Replace(base, `"charging":true`, `"charging":"true"`, 1),
		"missing battery":     strings.Replace(base, `"battery_level":0.5,`, ``, 1),
		"battery string":      strings.Replace(base, `"battery_level":0.5`, `"battery_level":"0.5"`, 1),
		"battery over 1":      strings.Replace(base, `"battery_level":0.5`, `"battery_level":1.5`, 1),
		"bad kind":            strings.Replace(base, `"heartbeat"`, `"unplugged"`, 1),
		"time no Z":           strings.Replace(base, `18:18:15Z`, `18:18:15+00:00`, 1),
		"short hash":          strings.Replace(base, `"hash":"`+GenesisHash, `"hash":"abc`, 1),
		"upper hex prev_hash": strings.Replace(base, `"prev_hash":"`+GenesisHash, `"prev_hash":"`+strings.Repeat("A", 64), 1),
		"not object":          `[1,2]`,
		"online null":         strings.Replace(base, `"online":true`, `"online":null`, 1),
	}
	for name, raw := range bad {
		_, err := ParseEntry(json.RawMessage(raw))
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Errorf("%s: want ParseError, got %v", name, err)
		}
	}
	_, err := ParseEntry(json.RawMessage(bad["charging string"]))
	var pe *ParseError
	if errors.As(err, &pe) && (pe.Seq == nil || *pe.Seq != 1) {
		t.Error("seq should be reported when readable")
	}
}

func TestParseJWKRejectsBadKeys(t *testing.T) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	good := JWKFor(&k.PublicKey)
	if _, err := ParseJWK(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{"kty":"RSA","crv":"P-256","x":"AA","y":"AA"}`,
		`{"kty":"EC","crv":"P-384","x":"AA","y":"AA"}`,
		`{"kty":"EC","crv":"P-256","x":"` + strings.Repeat("A", 43) + `","y":"` + strings.Repeat("A", 43) + `"}`, // not on curve
		`nope`,
	} {
		if _, err := ParseJWK([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestParseEntryNullAndQuotedTypes(t *testing.T) {
	base := `{"seq":1,"device_time":"2026-11-11T18:18:15Z","kind":"heartbeat","charging":true,"battery_level":null,"online":true,"prev_hash":"` + GenesisHash + `","hash":"` + GenesisHash + `","sig":"x"}`
	for name, raw := range map[string]string{
		"kind null":     strings.Replace(base, `"kind":"heartbeat"`, `"kind":null`, 1),
		"charging null": strings.Replace(base, `"charging":true`, `"charging":null`, 1),
		"sig number":    strings.Replace(base, `"sig":"x"`, `"sig":5`, 1),
	} {
		if _, err := ParseEntry(json.RawMessage(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
