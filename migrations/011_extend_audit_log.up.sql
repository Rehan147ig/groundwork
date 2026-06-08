-- PR #21: Audit Foundation.
--
-- Two changes the downstream operator experience (Audit Read API, Replay,
-- Leak Report, Dashboard L2) needs:
--
--   1) per-query agent attribution on audit_log so the dashboard can group
--      "which agent ran this" and replay can re-run under the same caller
--   2) per-chunk decision storage so the Leak Report can produce
--      "denied attempts per document" without re-executing the query
--
-- Both new audit_log columns are NON-CHAINED metadata: the digest payload in
-- engine.ComputeDigest is INTENTIONALLY unchanged, so VerifyChain over
-- pre-PR21 rows continues to match. Versioned tamper-evidence on agent_id
-- is a deliberate non-goal here; if that becomes a pilot requirement we'll
-- bump the digest payload to v2 and gate verification by row version.

-- agent_id is the human-readable name of the API key that made the call
-- (TenantContext.KeyName, sourced from api_keys.name). Nullable because
-- pre-PR21 rows have no attribution and writers running without an API key
-- context (e.g. fail_closed paths bypassing the verified-identity middleware)
-- legitimately have nothing to record.
ALTER TABLE audit_log ADD COLUMN agent_id TEXT;

-- access_decisions is a JSONB blob of the per-chunk AccessDecision objects
-- captured in the trace. It mirrors the rows in audit_log_decisions but
-- stays denormalised for single-row reads (the Replay engine fetches one
-- trace at a time and walks decisions; that read shape doesn't want a JOIN).
ALTER TABLE audit_log ADD COLUMN access_decisions JSONB;

-- Dashboard L2 groups the tenant landing page by agent_id.
CREATE INDEX idx_audit_log_agent_id
    ON audit_log (tenant_id, agent_id)
    WHERE agent_id IS NOT NULL;

-- Audit Read API filter: enforce vs shadow over time, newest-first.
CREATE INDEX idx_audit_log_decision_mode
    ON audit_log (tenant_id, decision_mode, timestamp_utc DESC);

-- Fail-closed events are the high-value alert surface. Partial index keeps
-- the predicate scan tiny since the vast majority of rows are NOT fail_closed.
CREATE INDEX idx_audit_log_fail_closed
    ON audit_log (tenant_id, fail_closed)
    WHERE fail_closed = true;

-- Per-chunk decision rows. ONE row per candidate Engine.Execute scored,
-- regardless of allow/deny. The Leak Report joins this against the demo
-- corpus (or, in production, a real document attribution view) to produce
-- per-document denial summaries.
--
-- FK to audit_log(trace_id): audit_log.trace_id is UNIQUE (declared in
-- migration 003) so the foreign key is valid. ON DELETE CASCADE is
-- defensive: audit_log has a no_delete_audit rule (also declared in 003)
-- that blocks row deletes, so the cascade never actually fires in
-- production — but if a future migration ever lifts that rule the
-- decision rows will go with their parent rather than orphan.
CREATE TABLE audit_log_decisions (
    trace_id        TEXT    NOT NULL,
    ordinal         INTEGER NOT NULL,
    chunk_id        TEXT    NOT NULL,
    document_id     TEXT,
    allowed         BOOLEAN NOT NULL,
    reason          TEXT,
    required_scope  TEXT,
    region          TEXT,
    score           NUMERIC,
    PRIMARY KEY (trace_id, ordinal),
    FOREIGN KEY (trace_id)
        REFERENCES audit_log (trace_id)
        ON DELETE CASCADE
);

-- Leak Report hot path: "all decisions for document X".
CREATE INDEX idx_audit_log_decisions_doc
    ON audit_log_decisions (document_id)
    WHERE document_id IS NOT NULL;

-- Tighter partial index for the denied-only fan-in (the Leak Report's
-- headline question is "denials on this document", not "all decisions").
CREATE INDEX idx_audit_log_decisions_denied
    ON audit_log_decisions (document_id)
    WHERE allowed = false AND document_id IS NOT NULL;
