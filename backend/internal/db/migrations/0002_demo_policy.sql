-- Startup demo policy marker (CONTRACTS.md 1.8 section 3, "Demo policy").
-- Internal bookkeeping, not an API row: it records which policy is the demo
-- policy so a restart on the same database finds it again. The single-slot
-- primary key makes a second demo policy impossible whatever the code does.

CREATE TABLE demo_policy (
    slot       INTEGER PRIMARY KEY CHECK (slot = 1),
    policy_id  TEXT NOT NULL UNIQUE REFERENCES policies(id),
    created_at TEXT NOT NULL
);
