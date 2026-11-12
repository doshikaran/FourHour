package witness

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// The helpers below build signed entries the way the witness page does. They
// exist for tests in this and other packages; production code never signs.

// JWKFor returns the public JWK of a key.
func JWKFor(pub *ecdsa.PublicKey) json.RawMessage {
	b, err := pub.Bytes() // 0x04 || X || Y
	if err != nil {
		panic(err)
	}
	enc := base64.RawURLEncoding
	return json.RawMessage(fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`, enc.EncodeToString(b[1:33]), enc.EncodeToString(b[33:65])))
}

// SignEntry fills e.Hash and e.Sig (raw r‖s, base64url) and returns the
// entry as JSON, as the witness page would send it.
func SignEntry(priv *ecdsa.PrivateKey, e Entry) (Entry, json.RawMessage) {
	e.Hash = Hash(e)
	digest := sha256.Sum256([]byte(e.Hash))
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
	if err != nil {
		panic(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	e.Sig = base64.RawURLEncoding.EncodeToString(sig)
	bl := "null"
	if e.BatteryLevel != nil {
		bl = JSNumber(*e.BatteryLevel)
	}
	raw := fmt.Sprintf(`{"seq":%d,"device_time":%s,"kind":%s,"charging":%t,"battery_level":%s,"online":%t,"prev_hash":%s,"hash":%q,"sig":%q}`,
		e.Seq, JSString(e.DeviceTime), JSString(e.Kind), e.Charging, bl, e.Online, JSString(e.PrevHash), e.Hash, e.Sig)
	return e, json.RawMessage(raw)
}
