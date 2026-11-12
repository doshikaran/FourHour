package replay_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"fourhour/backend/internal/config"
	"fourhour/backend/internal/db"
	"fourhour/backend/internal/model"
	"fourhour/backend/internal/paypal"
	"fourhour/backend/internal/paypal/paypaltest"
	"fourhour/backend/internal/replay"
	"fourhour/backend/internal/rules"
	"fourhour/backend/internal/service"
	"fourhour/backend/internal/stream"
	"fourhour/backend/internal/witness"
)

// manualEnv runs the fixture scenario one reading at a time. Readings are
// 15 minutes apart from 2000-01-01T00:00Z; reading i is at i*15 minutes.
// Fixture shares: 0-3 zero, 4-7 0.02, 8-31 0.95 (16-19 missing), 32-35 0.5, 36-47 zero.
type manualEnv struct {
	svc    *service.Service
	eng    *replay.Engine
	policy model.Policy
}

func newManual(t *testing.T, p *rules.Params) manualEnv {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	f := paypaltest.New(t)
	pp, err := paypal.NewClient(paypal.Config{BaseURL: paypal.SandboxBaseURL, ClientID: paypaltest.ClientID, ClientSecret: paypaltest.ClientSecret, HTTPClient: f.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	art := t.TempDir()
	svc := service.New(service.Options{DB: d, PayPal: pp, Log: log, Hub: stream.NewHub(4096), Config: config.Config{}, ArtifactsDir: art,
		PollDelay: 5 * time.Millisecond, PollInterval: 5 * time.Millisecond, PollTimeout: 2 * time.Second})
	t.Cleanup(svc.Close)
	eng := replay.New(replay.Options{Service: svc, Log: log, ArtifactsDir: art, Manual: true, Params: p})
	t.Cleanup(eng.Reset)
	seed, err := svc.Seed(ctx, service.SeedInput{MemberEmail: "m@example.com", PayoutEmail: "p@example.com", VaultToken: "tok", FIPS: "48201"})
	if err != nil {
		t.Fatal(err)
	}
	return manualEnv{svc: svc, eng: eng, policy: seed.Policy}
}

// device registers a key and returns an uploader that signs and chains.
type device struct {
	t    *testing.T
	svc  *service.Service
	id   string
	key  *ecdsa.PrivateKey
	seq  int64
	prev string
}

func (m manualEnv) device(t *testing.T) *device {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := m.svc.RegisterDevice(context.Background(), m.policy.ID, witness.JWKFor(&k.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	return &device{t: t, svc: m.svc, id: reg.DeviceID, key: k, prev: reg.GenesisHash}
}

func (d *device) send(kind string, charging bool) {
	d.t.Helper()
	d.seq++
	bl := 0.5
	e, raw := witness.SignEntry(d.key, witness.Entry{Seq: d.seq, DeviceTime: time.Now().UTC().Format("2006-01-02T15:04:05Z"), Kind: kind, Charging: charging, BatteryLevel: &bl, Online: true, PrevHash: d.prev})
	res, err := d.svc.AddEntries(context.Background(), d.id, []json.RawMessage{raw})
	if err != nil || res.Accepted != 1 || len(res.Rejected) != 0 {
		d.t.Fatalf("upload %s: %+v %v", kind, res, err)
	}
	d.prev = e.Hash
}

func claims(t *testing.T, svc *service.Service) []model.ClaimItem {
	t.Helper()
	cs, err := svc.ClaimItems(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestEngineR1WitnessConfirms(t *testing.T) {
	m := newManual(t, nil) // fixture params: theta_hi 0.05, theta_lo 0.01
	if _, err := m.eng.Start("fixture-outage-harris", 1); err != nil {
		t.Fatal(err)
	}
	dev := m.device(t)
	m.eng.Advance(1)              // reading 0, 00:00
	dev.send("heartbeat", true)   // sim 00:00
	m.eng.Advance(8)              // readings 1-8, now 02:00
	dev.send("power_lost", false) // sim 02:00, hi run starts at 02:00
	m.eng.Advance(15)             // readings 9-23, now 05:45: nothing yet
	if cs := claims(t, m.svc); len(cs) != 0 {
		t.Fatalf("early claim %+v", cs)
	}
	m.eng.Advance(1) // reading 24, 06:00: hi run 4h, witness 4h
	cs := claims(t, m.svc)
	if len(cs) != 1 || cs[0].Rule != "R1" || cs[0].AmountCents != 60 || *cs[0].SimTime != "2000-01-01T06:00:00Z" {
		t.Fatalf("%+v", cs)
	}
	// Claim detail: the witness entries the rule engine used.
	det, err := m.svc.GetClaimDetail(context.Background(), cs[0].ID)
	if err != nil || det.OutageEvent == nil || len(det.WitnessEntries) != 1 || det.WitnessEntries[0].Kind != "power_lost" || len(det.IndexSeries) == 0 {
		t.Fatalf("detail %+v %v", det, err)
	}
}

func TestEngineR4WitnessShowsPowerOn(t *testing.T) {
	m := newManual(t, nil)
	if _, err := m.eng.Start("fixture-outage-harris", 1); err != nil {
		t.Fatal(err)
	}
	dev := m.device(t)
	m.eng.Advance(13)           // now 03:00
	dev.send("heartbeat", true) // inside the event window, charging
	m.eng.Advance(20)           // through 08:00: hi run qualified at 06:00 but device policy waits
	if cs := claims(t, m.svc); len(cs) != 0 {
		t.Fatalf("must wait for the run to close: %+v", cs)
	}
	m.eng.Advance(100) // to the end; the run closes after the dropout window
	<-m.eng.Done()
	cs := claims(t, m.svc)
	if len(cs) != 1 || cs[0].Rule != "R4" || cs[0].Decision != "declined" || cs[0].AmountCents != 0 || cs[0].Payout != nil {
		t.Fatalf("%+v", cs)
	}
}

func TestEngineR2DeviceButNoLog(t *testing.T) {
	m := newManual(t, nil)
	if _, err := m.eng.Start("fixture-outage-harris", 1); err != nil {
		t.Fatal(err)
	}
	m.device(t) // registered, never uploads
	m.eng.Advance(100)
	<-m.eng.Done()
	cs := claims(t, m.svc)
	if len(cs) != 1 || cs[0].Rule != "R2" || cs[0].AmountCents != 30 {
		t.Fatalf("%+v", cs)
	}
}

func TestEngineR3LenientIndexWithWitness(t *testing.T) {
	// Test thresholds so the 0.95 plateau is above theta_lo but below theta_hi.
	p := rules.Params{ThetaHi: 0.96, ThetaLo: 0.4, GapBridge: 120 * time.Minute, MergeWindow: 72 * time.Hour, DropoutMaxFraction: 0.1}
	m := newManual(t, &p)
	if _, err := m.eng.Start("fixture-outage-harris", 1); err != nil {
		t.Fatal(err)
	}
	dev := m.device(t)
	m.eng.Advance(5)
	dev.send("attest_lost", false) // self-reported, counts the same
	m.eng.Advance(20)              // reading 24, 06:00: lo run (from 02:00) 4h; witness since 01:00
	cs := claims(t, m.svc)
	if len(cs) != 1 || cs[0].Rule != "R3" || cs[0].AmountCents != 60 || cs[0].Decision != "paid" {
		t.Fatalf("%+v", cs)
	}
}

func TestEngineR5WitnessWithoutIndex(t *testing.T) {
	// Thresholds above every reading: the index never opens an event.
	p := rules.Params{ThetaHi: 0.99, ThetaLo: 0.98, GapBridge: 120 * time.Minute, MergeWindow: 72 * time.Hour, DropoutMaxFraction: 0.1}
	m := newManual(t, &p)
	if _, err := m.eng.Start("fixture-outage-harris", 1); err != nil {
		t.Fatal(err)
	}
	dev := m.device(t)
	m.eng.Advance(1)
	dev.send("power_lost", false) // 00:00
	m.eng.Advance(15)             // 03:45
	if cs := claims(t, m.svc); len(cs) != 0 {
		t.Fatalf("%+v", cs)
	}
	m.eng.Advance(1) // 04:00
	cs := claims(t, m.svc)
	if len(cs) != 1 || cs[0].Rule != "R5" || cs[0].Decision != "review" || cs[0].OutageEventID != nil || cs[0].WitnessEntryID == nil || cs[0].Payout != nil {
		t.Fatalf("%+v", cs)
	}
	m.eng.Advance(100)
	if cs := claims(t, m.svc); len(cs) != 1 {
		t.Fatalf("R5 must be recorded once: %+v", cs)
	}
	det, err := m.svc.GetClaimDetail(context.Background(), cs[0].ID)
	if err != nil || det.OutageEvent != nil || len(det.WitnessEntries) != 1 || len(det.IndexSeries) == 0 {
		t.Fatalf("R5 detail %+v %v", det, err)
	}
}
