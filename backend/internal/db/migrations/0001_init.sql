-- FourHour schema, CONTRACTS.md v1.4 section 3.
-- Times are ISO 8601 UTC strings with a Z suffix. Money is INTEGER cents.
-- Booleans are INTEGER 0/1. Enums are TEXT with CHECK constraints.
-- Every uniqueness rule in section 3 is a database constraint here.

CREATE TABLE members (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    email        TEXT NOT NULL,
    paypal_email TEXT NOT NULL,
    created_at   TEXT NOT NULL
);

CREATE TABLE policies (
    id                  TEXT PRIMARY KEY,
    member_id           TEXT NOT NULL REFERENCES members(id),
    fips                TEXT NOT NULL CHECK (length(fips) = 5 AND fips NOT GLOB '*[^0-9]*'),
    cover_cents         INTEGER NOT NULL,
    premium_cents       INTEGER NOT NULL CHECK (premium_cents >= 0),
    status              TEXT NOT NULL CHECK (status IN ('pending_vault', 'active', 'cancelled')),
    vault_setup_token   TEXT,
    vault_payment_token TEXT,
    model_version       TEXT NOT NULL,
    created_at          TEXT NOT NULL,
    cancelled_at        TEXT,
    -- 100 cents only for a seeded sandbox test policy (model_version 'test-cover').
    CHECK (cover_cents IN (5000, 15000, 30000)
           OR (cover_cents = 100 AND premium_cents = 100 AND model_version = 'test-cover'))
);

CREATE TABLE premium_charges (
    id                TEXT PRIMARY KEY,
    policy_id         TEXT NOT NULL REFERENCES policies(id),
    period            TEXT NOT NULL CHECK (period GLOB '[0-9][0-9][0-9][0-9]-[0-1][0-9]'),
    amount_cents      INTEGER NOT NULL CHECK (amount_cents > 0),
    request_id        TEXT NOT NULL UNIQUE,
    paypal_order_id   TEXT,
    paypal_capture_id TEXT,
    status            TEXT NOT NULL CHECK (status IN ('created', 'completed', 'failed', 'refunded')),
    created_at        TEXT NOT NULL,
    UNIQUE (policy_id, period)
);

CREATE TABLE outage_events (
    id            TEXT PRIMARY KEY,
    fips          TEXT NOT NULL CHECK (length(fips) = 5 AND fips NOT GLOB '*[^0-9]*'),
    scenario_id   TEXT,
    lo_started_at TEXT NOT NULL,
    lo_ended_at   TEXT,
    hi_started_at TEXT,
    hi_ended_at   TEXT,
    peak_share    REAL NOT NULL CHECK (peak_share >= 0 AND peak_share <= 1)
);

CREATE TABLE witness_devices (
    id             TEXT PRIMARY KEY,
    policy_id      TEXT NOT NULL REFERENCES policies(id),
    public_key_jwk TEXT NOT NULL,
    registered_at  TEXT NOT NULL,
    last_seq       INTEGER NOT NULL DEFAULT 0 CHECK (last_seq >= 0),
    last_hash      TEXT NOT NULL DEFAULT '0000000000000000000000000000000000000000000000000000000000000000'
);

CREATE TABLE witness_entries (
    id            TEXT PRIMARY KEY,
    device_id     TEXT NOT NULL REFERENCES witness_devices(id),
    seq           INTEGER NOT NULL,
    device_time   TEXT NOT NULL,
    kind          TEXT NOT NULL,
    charging      INTEGER NOT NULL,
    battery_level REAL,
    online        INTEGER NOT NULL,
    prev_hash     TEXT NOT NULL,
    hash          TEXT NOT NULL,
    sig           TEXT NOT NULL,
    received_at   TEXT NOT NULL,
    sim_time      TEXT,
    valid         INTEGER NOT NULL CHECK (valid IN (0, 1)),
    reject_reason TEXT,
    -- A rejected row has a reason; a valid row has none.
    CHECK ((valid = 1 AND reject_reason IS NULL) OR (valid = 0 AND reject_reason IS NOT NULL)),
    -- Rejected rows are stored with their values as received, so the value
    -- checks apply to valid rows only.
    CHECK (valid = 0 OR (
        seq >= 1
        AND kind IN ('heartbeat', 'power_lost', 'power_restored', 'attest_lost', 'attest_restored')
        AND charging IN (0, 1)
        AND online IN (0, 1)
        AND (battery_level IS NULL OR (battery_level >= 0 AND battery_level <= 1))
    ))
);

-- Unique on (device_id, seq) among valid rows only, so a corrected resend of
-- a rejected seq can be stored.
CREATE UNIQUE INDEX witness_entries_device_seq_valid ON witness_entries (device_id, seq) WHERE valid = 1;

CREATE TABLE claims (
    id              TEXT PRIMARY KEY,
    policy_id       TEXT NOT NULL REFERENCES policies(id),
    outage_event_id TEXT REFERENCES outage_events(id), -- null only for R5
    tier            TEXT NOT NULL CHECK (tier IN ('t4', 't24')),
    rule            TEXT NOT NULL CHECK (rule IN ('R1', 'R2', 'R3', 'R4', 'R5')),
    decision        TEXT NOT NULL CHECK (decision IN ('paid', 'declined', 'review')),
    amount_cents    INTEGER NOT NULL CHECK (amount_cents >= 0),
    reason          TEXT NOT NULL,
    decided_at      TEXT NOT NULL,
    sim_time        TEXT,
    witness_entry_id TEXT REFERENCES witness_entries(id), -- set only for R5
    -- R5 has no outage event and is keyed by the entry that started the
    -- witness interval; every other rule has an outage event and no entry.
    CHECK ((rule = 'R5' AND outage_event_id IS NULL AND witness_entry_id IS NOT NULL)
        OR (rule <> 'R5' AND outage_event_id IS NOT NULL AND witness_entry_id IS NULL))
);

-- One claim per (policy, outage event, tier); R5 one per (policy, witness entry).
CREATE UNIQUE INDEX claims_policy_event_tier ON claims (policy_id, outage_event_id, tier) WHERE outage_event_id IS NOT NULL;
CREATE UNIQUE INDEX claims_policy_witness_entry ON claims (policy_id, witness_entry_id) WHERE witness_entry_id IS NOT NULL;

CREATE TABLE payouts (
    id              TEXT PRIMARY KEY,
    claim_id        TEXT NOT NULL UNIQUE REFERENCES claims(id),
    sender_batch_id TEXT NOT NULL UNIQUE,
    paypal_batch_id TEXT,
    paypal_item_id  TEXT,
    amount_cents    INTEGER NOT NULL CHECK (amount_cents > 0),
    status          TEXT NOT NULL CHECK (status IN ('created', 'pending', 'success', 'failed', 'unclaimed')),
    created_at      TEXT NOT NULL,
    confirmed_at    TEXT,
    confirmed_via   TEXT CHECK (confirmed_via IS NULL OR confirmed_via IN ('webhook', 'poll'))
);

CREATE TABLE refunds (
    id                TEXT PRIMARY KEY,
    premium_charge_id TEXT NOT NULL REFERENCES premium_charges(id),
    amount_cents      INTEGER NOT NULL CHECK (amount_cents > 0),
    request_id        TEXT NOT NULL UNIQUE,
    paypal_refund_id  TEXT,
    status            TEXT NOT NULL CHECK (status IN ('created', 'completed', 'failed')),
    created_at        TEXT NOT NULL
);

CREATE TABLE webhook_events (
    id           TEXT PRIMARY KEY, -- PayPal's event ID
    event_type   TEXT NOT NULL,
    resource_id  TEXT NOT NULL,
    verified     INTEGER NOT NULL CHECK (verified IN (0, 1)),
    received_at  TEXT NOT NULL,
    payload_json TEXT NOT NULL
);

CREATE TABLE ledger_entries (
    id           TEXT PRIMARY KEY,
    at           TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('premium', 'payout', 'refund')),
    -- Positive into the pool, negative out.
    amount_cents INTEGER NOT NULL CHECK (
        (kind = 'premium' AND amount_cents > 0) OR
        (kind IN ('payout', 'refund') AND amount_cents < 0)
    ),
    policy_id    TEXT NOT NULL REFERENCES policies(id),
    ref_id       TEXT NOT NULL, -- the premium charge, payout or refund ID
    -- A money movement is never appended twice.
    UNIQUE (kind, ref_id)
);

-- Append-only: rows are never updated or deleted.
CREATE TRIGGER ledger_entries_no_update BEFORE UPDATE ON ledger_entries
BEGIN
    SELECT RAISE(ABORT, 'ledger_entries is append-only');
END;

CREATE TRIGGER ledger_entries_no_delete BEFORE DELETE ON ledger_entries
BEGIN
    SELECT RAISE(ABORT, 'ledger_entries is append-only');
END;
