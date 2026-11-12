package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const zeros = "0000000000000000000000000000000000000000000000000000000000000000"

func openMigrated(t *testing.T) *sql.DB {
	t.Helper()
	// A space in the path matches the real repo location.
	dir := filepath.Join(t.TempDir(), "with space")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := Migrate(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return d
}

func mustExec(t *testing.T, d *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := d.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func mustFail(t *testing.T, d *sql.DB, wantSubstr, q string, args ...any) {
	t.Helper()
	_, err := d.Exec(q, args...)
	if err == nil {
		t.Fatalf("expected failure (%s) for %s", wantSubstr, q)
	}
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("want error containing %q, got %v", wantSubstr, err)
	}
}

// seed creates one member, policy, outage event, device and claim.
func seed(t *testing.T, d *sql.DB) {
	t.Helper()
	ts := "2026-11-11T00:00:00Z"
	mustExec(t, d, `INSERT INTO members VALUES ('mem_a','A','a@x','pa@x',?)`, ts)
	mustExec(t, d, `INSERT INTO policies (id,member_id,fips,cover_cents,premium_cents,status,model_version,created_at) VALUES ('pol_a','mem_a','48201',15000,412,'active','m1',?)`, ts)
	mustExec(t, d, `INSERT INTO outage_events (id,fips,lo_started_at,peak_share) VALUES ('evt_a','48201',?,0.3)`, ts)
	mustExec(t, d, `INSERT INTO witness_devices (id,policy_id,public_key_jwk,registered_at) VALUES ('dev_a','pol_a','{}',?)`, ts)
	mustExec(t, d, `INSERT INTO claims VALUES ('clm_a','pol_a','evt_a','t4','R1','paid',9000,'r',?,NULL,NULL)`, ts)
}

func TestMigrateIdempotent(t *testing.T) {
	d := openMigrated(t)
	applied, err := Migrate(context.Background(), d)
	if err != nil || len(applied) != 0 {
		t.Fatalf("second migrate: %v %v", applied, err)
	}
	want := []string{"claims", "ledger_entries", "members", "outage_events", "payouts", "policies", "premium_charges", "refunds", "schema_migrations", "webhook_events", "witness_devices", "witness_entries"}
	rows, err := d.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tables:\n got %v\nwant %v", got, want)
	}
}

func TestWitnessDeviceDefaults(t *testing.T) {
	d := openMigrated(t)
	seed(t, d)
	var seq int
	var h string
	if err := d.QueryRow(`SELECT last_seq, last_hash FROM witness_devices WHERE id='dev_a'`).Scan(&seq, &h); err != nil {
		t.Fatal(err)
	}
	if seq != 0 || h != zeros {
		t.Errorf("defaults %d %s", seq, h)
	}
}

func TestUniqueConstraints(t *testing.T) {
	d := openMigrated(t)
	seed(t, d)
	ts := "2026-11-11T00:00:00Z"

	t.Run("premium_charges policy+period", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO premium_charges VALUES ('chg_1','pol_a','2026-12',412,'prem-pol_a-2026-12',NULL,NULL,'created',?)`, ts)
		mustFail(t, d, "UNIQUE", `INSERT INTO premium_charges VALUES ('chg_2','pol_a','2026-12',412,'other-key',NULL,NULL,'created',?)`, ts)
	})
	t.Run("premium_charges request_id", func(t *testing.T) {
		mustFail(t, d, "UNIQUE", `INSERT INTO premium_charges VALUES ('chg_3','pol_a','2027-01',412,'prem-pol_a-2026-12',NULL,NULL,'created',?)`, ts)
	})
	t.Run("witness_entries device+seq among valid rows", func(t *testing.T) {
		valid := `INSERT INTO witness_entries VALUES (?, 'dev_a', ?, ?, 'heartbeat', 1, 0.83, 1, ?, 'h', 's', ?, NULL, 1, NULL)`
		rejected := `INSERT INTO witness_entries VALUES (?, 'dev_a', ?, ?, 'heartbeat', 1, 0.83, 1, ?, 'h', 's', ?, NULL, 0, 'bad_signature')`
		mustExec(t, d, valid, "wen_1", 1, ts, zeros, ts)
		mustFail(t, d, "UNIQUE", valid, "wen_2", 1, ts, zeros, ts)
		// A rejected row may share a seq with a valid row, and with other rejected rows.
		mustExec(t, d, rejected, "wen_3", 1, ts, zeros, ts)
		mustExec(t, d, rejected, "wen_4", 2, ts, zeros, ts)
		mustExec(t, d, rejected, "wen_5", 2, ts, zeros, ts)
		// A corrected resend of a rejected seq can be stored as valid, once.
		mustExec(t, d, valid, "wen_6", 2, ts, zeros, ts)
		mustFail(t, d, "UNIQUE", valid, "wen_7", 2, ts, zeros, ts)
	})
	t.Run("claims policy+event+tier", func(t *testing.T) {
		mustFail(t, d, "UNIQUE", `INSERT INTO claims VALUES ('clm_b','pol_a','evt_a','t4','R1','paid',9000,'r',?,NULL,NULL)`, ts)
		mustExec(t, d, `INSERT INTO claims VALUES ('clm_c','pol_a','evt_a','t24','R1','paid',6000,'r',?,NULL,NULL)`, ts)
	})
	t.Run("payouts claim_id", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO payouts (id,claim_id,sender_batch_id,amount_cents,status,created_at) VALUES ('pay_1','clm_a','clm-pol_a-evt_a-t4',9000,'created',?)`, ts)
		mustFail(t, d, "UNIQUE", `INSERT INTO payouts (id,claim_id,sender_batch_id,amount_cents,status,created_at) VALUES ('pay_2','clm_a','other',9000,'created',?)`, ts)
	})
	t.Run("payouts sender_batch_id", func(t *testing.T) {
		mustFail(t, d, "UNIQUE", `INSERT INTO payouts (id,claim_id,sender_batch_id,amount_cents,status,created_at) VALUES ('pay_3','clm_c','clm-pol_a-evt_a-t4',6000,'created',?)`, ts)
	})
	t.Run("refunds request_id", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO refunds VALUES ('rfd_1','chg_1',100,'rfd-chg_1',NULL,'created',?)`, ts)
		mustFail(t, d, "UNIQUE", `INSERT INTO refunds VALUES ('rfd_2','chg_1',100,'rfd-chg_1',NULL,'created',?)`, ts)
		mustFail(t, d, "NOT NULL", `INSERT INTO refunds (id,premium_charge_id,amount_cents,request_id,status) VALUES ('rfd_3','chg_1',100,'rfd-x','created')`)
	})
	t.Run("webhook_events id", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO webhook_events VALUES ('WH-1','PAYMENT.PAYOUTSBATCH.SUCCESS','b',0,?,'{}')`, ts)
		mustFail(t, d, "UNIQUE", `INSERT INTO webhook_events VALUES ('WH-1','PAYMENT.PAYOUTSBATCH.SUCCESS','b',0,?,'{}')`, ts)
		mustFail(t, d, "NOT NULL", `INSERT INTO webhook_events VALUES ('WH-2','PAYMENT.PAYOUTSBATCH.SUCCESS',NULL,0,?,'{}')`, ts)
	})
	t.Run("ledger_entries kind+ref_id", func(t *testing.T) {
		mustExec(t, d, `INSERT INTO ledger_entries VALUES ('led_u1',?,'premium',412,'pol_a','chg_1')`, ts)
		mustFail(t, d, "UNIQUE", `INSERT INTO ledger_entries VALUES ('led_u2',?,'premium',412,'pol_a','chg_1')`, ts)
		// The same ref under a different kind is a different movement.
		mustExec(t, d, `INSERT INTO ledger_entries VALUES ('led_u3',?,'refund',-100,'pol_a','chg_1')`, ts)
	})
}

func TestCheckAndForeignKeys(t *testing.T) {
	d := openMigrated(t)
	seed(t, d)
	ts := "2026-11-11T00:00:00Z"
	mustFail(t, d, "FOREIGN KEY", `INSERT INTO policies (id,member_id,fips,cover_cents,premium_cents,status,model_version,created_at) VALUES ('pol_x','mem_missing','48201',15000,1,'active','m',?)`, ts)
	mustFail(t, d, "CHECK", `INSERT INTO policies (id,member_id,fips,cover_cents,premium_cents,status,model_version,created_at) VALUES ('pol_x','mem_a','48201',10000,1,'active','m',?)`, ts)
	mustFail(t, d, "CHECK", `INSERT INTO policies (id,member_id,fips,cover_cents,premium_cents,status,model_version,created_at) VALUES ('pol_x','mem_a','4820A',15000,1,'active','m',?)`, ts)
	mustFail(t, d, "CHECK", `INSERT INTO policies (id,member_id,fips,cover_cents,premium_cents,status,model_version,created_at) VALUES ('pol_x','mem_a','48201',15000,1,'open','m',?)`, ts)
	mustFail(t, d, "CHECK", `INSERT INTO claims VALUES ('clm_x','pol_a','evt_a','t8','R1','paid',1,'r',?,NULL,NULL)`, ts)
	// Value checks bind valid rows only.
	mustFail(t, d, "CHECK", `INSERT INTO witness_entries VALUES ('wen_x','dev_a',1,?,'unplugged',0,NULL,1,'p','h','s',?,NULL,1,NULL)`, ts, ts)
	mustFail(t, d, "CHECK", `INSERT INTO witness_entries VALUES ('wen_x','dev_a',1,?,'heartbeat',0,1.5,1,'p','h','s',?,NULL,1,NULL)`, ts, ts)
	mustFail(t, d, "CHECK", `INSERT INTO witness_entries VALUES ('wen_x','dev_a',0,?,'heartbeat',0,NULL,1,'p','h','s',?,NULL,1,NULL)`, ts, ts)
	mustFail(t, d, "CHECK", `INSERT INTO witness_entries VALUES ('wen_x','dev_a',1,?,'heartbeat',2,NULL,1,'p','h','s',?,NULL,1,NULL)`, ts, ts)
	// A rejected row is stored with its values as received.
	mustExec(t, d, `INSERT INTO witness_entries VALUES ('wen_r1','dev_a',0,?,'unplugged',2,1.5,7,'p','h','s',?,NULL,0,'bad_sequence')`, ts, ts)
	// reject_reason is set exactly when the row is rejected.
	mustFail(t, d, "CHECK", `INSERT INTO witness_entries VALUES ('wen_x','dev_a',3,?,'heartbeat',0,NULL,1,'p','h','s',?,NULL,0,NULL)`, ts, ts)
	mustFail(t, d, "CHECK", `INSERT INTO witness_entries VALUES ('wen_x','dev_a',3,?,'heartbeat',0,NULL,1,'p','h','s',?,NULL,1,'bad_signature')`, ts, ts)
	mustFail(t, d, "CHECK", `INSERT INTO payouts (id,claim_id,sender_batch_id,amount_cents,status,created_at,confirmed_via) VALUES ('pay_x','clm_a','s',1,'success',?,'email')`, ts)
	mustFail(t, d, "CHECK", `INSERT INTO premium_charges VALUES ('chg_x','pol_a','2026-12',0,'k',NULL,NULL,'created',?)`, ts)
	mustFail(t, d, "CHECK", `INSERT INTO premium_charges VALUES ('chg_x','pol_a','Dec-26',412,'k',NULL,NULL,'created',?)`, ts)
}

func TestLedgerAppendOnlyAndSign(t *testing.T) {
	d := openMigrated(t)
	seed(t, d)
	ts := "2026-11-11T00:00:00Z"
	mustExec(t, d, `INSERT INTO ledger_entries VALUES ('led_1',?,'premium',412,'pol_a','chg_1')`, ts)
	mustExec(t, d, `INSERT INTO ledger_entries VALUES ('led_2',?,'payout',-9000,'pol_a','pay_1')`, ts)
	mustFail(t, d, "CHECK", `INSERT INTO ledger_entries VALUES ('led_3',?,'premium',-412,'pol_a','chg_1')`, ts)
	mustFail(t, d, "CHECK", `INSERT INTO ledger_entries VALUES ('led_4',?,'refund',100,'pol_a','rfd_1')`, ts)
	mustFail(t, d, "append-only", `UPDATE ledger_entries SET amount_cents = 1 WHERE id = 'led_1'`)
	mustFail(t, d, "append-only", `DELETE FROM ledger_entries WHERE id = 'led_1'`)
	var sum int64
	if err := d.QueryRow(`SELECT COALESCE(SUM(amount_cents),0) FROM ledger_entries`).Scan(&sum); err != nil {
		t.Fatal(err)
	}
	if sum != 412-9000 {
		t.Errorf("balance %d", sum)
	}
}

func TestOpenRejectsQueryInPath(t *testing.T) {
	if _, err := Open(context.Background(), "x.db?mode=ro"); err == nil {
		t.Error("want error")
	}
}

func TestTestCoverAndR5Claims(t *testing.T) {
	d := openMigrated(t)
	seed(t, d)
	ts := "2026-11-11T00:00:00Z"
	// cover 100 only as a seeded test policy.
	mustExec(t, d, `INSERT INTO policies (id,member_id,fips,cover_cents,premium_cents,status,model_version,created_at) VALUES ('pol_t','mem_a','48201',100,100,'active','test-cover',?)`, ts)
	mustFail(t, d, "CHECK", `INSERT INTO policies (id,member_id,fips,cover_cents,premium_cents,status,model_version,created_at) VALUES ('pol_u','mem_a','48201',100,100,'active','m1',?)`, ts)
	mustFail(t, d, "CHECK", `INSERT INTO policies (id,member_id,fips,cover_cents,premium_cents,status,model_version,created_at) VALUES ('pol_u','mem_a','48201',100,412,'active','test-cover',?)`, ts)
	mustExec(t, d, `INSERT INTO witness_entries VALUES ('wen_l','dev_a',1,?,'power_lost',0,NULL,1,'p','h','s',?,NULL,1,NULL)`, ts, ts)
	// R5: no outage event, keyed by the witness entry.
	mustExec(t, d, `INSERT INTO claims VALUES ('clm_r5','pol_a',NULL,'t4','R5','review',0,'r',?,NULL,'wen_l')`, ts)
	mustFail(t, d, "UNIQUE", `INSERT INTO claims VALUES ('clm_r5b','pol_a',NULL,'t4','R5','review',0,'r',?,NULL,'wen_l')`, ts)
	mustFail(t, d, "CHECK", `INSERT INTO claims VALUES ('clm_r5c','pol_a',NULL,'t4','R5','review',0,'r',?,NULL,NULL)`, ts)
	mustFail(t, d, "CHECK", `INSERT INTO claims VALUES ('clm_r2','pol_a',NULL,'t4','R2','paid',1,'r',?,NULL,NULL)`, ts)
	mustFail(t, d, "CHECK", `INSERT INTO claims VALUES ('clm_r2','pol_a','evt_a','t24','R2','paid',1,'r',?,NULL,'wen_l')`, ts)
}
