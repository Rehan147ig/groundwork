CREATE TABLE audit_log (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trace_id              TEXT NOT NULL UNIQUE,
    tenant_id             UUID NOT NULL,
    user_id               TEXT NOT NULL,
    query_hash            TEXT NOT NULL,
    timestamp_utc         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    region                TEXT NOT NULL,
    candidates_retrieved  INTEGER NOT NULL,
    candidates_allowed    INTEGER NOT NULL,
    candidates_blocked    INTEGER NOT NULL,
    fail_closed           BOOLEAN NOT NULL,
    fail_stage            TEXT,
    error_code            TEXT,
    error_message         TEXT,
    openfga_latency_ms    INTEGER,
    qdrant_latency_ms     INTEGER,
    total_latency_ms      INTEGER NOT NULL,
    circuit_breaker_state TEXT NOT NULL,
    immutable_digest      TEXT NOT NULL
);

-- Prevent any modification - rows are write-once
CREATE RULE no_update_audit AS ON UPDATE TO audit_log DO INSTEAD NOTHING;
CREATE RULE no_delete_audit AS ON DELETE TO audit_log DO INSTEAD NOTHING;
