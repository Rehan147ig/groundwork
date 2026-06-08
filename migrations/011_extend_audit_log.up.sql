-- PR #21: Audit Foundation.
--
-- Two changes the downstream operator experience (Audit Read API, Replay,
-- Leak Report, Dashboard L2) needs:
--
--   1) per-query agent attribution on audit_log so the dashboard can group
--      "which API key ran this" and replay can re-run under the same caller
--   2) per-chunk decision storage so the Leak Report can produce
--      "denied attempts per document" without re-executing the query
--
-- All new columns are NON-CHAINED metadata: the digest payload in
-- engine.ComputeDigest is INTENTIONALLY unchanged, so VerifyChain over
-- pre-PR21 rows continues to match. Versioned tamper-evidence on
-- per-query / per-chunk attribution is a deliberate non-goal here; if
-- that becomes a pilot requirement we'll bump the digest payload to v2
-- and gate verification by row version.

-- ---------------------------------------------------------------------
-- audit_log: agent attribution + per-query decision blob
-- ---------------------------------------------------------------------

-- agent_key_id is the STABLE foreign key onto api_keys(id) — never
-- changes once the key is created. The actual FK is not declared
-- here because api_keys lives outside the migration system (created
-- in runtime/auth.go bootstrap) and we don't want migration 011 to
-- depend on its presence; the writer enforces the contract.
ALTER TABLE audit_log ADD COLUMN agent_key_id BIGINT;

-- agent_key_name is the DISPLAY snapshot of api_keys.name AT WRITE
-- TIME. The name in api_keys can be mutated by operators (rename
-- "treasury-agent" -> "treasury-agent-v2"); storing the snapshot
-- here means historical audit rows keep showing the name as it was
-- when the call landed, while the stable group-by key
-- (agent_key_id) is what the Dashboard joins on.
ALTER TABLE audit_log ADD COLUMN agent_key_name TEXT;

-- access_decisions is a JSONB blob of the per-chunk AccessDecision
-- objects captured in the trace. It mirrors the rows in
-- audit_log_decisions but stays denormalised for single-row reads
-- (Replay fetches one trace at a time and walks decisions; that
-- read shape doesn't want a JOIN).
ALTER TABLE audit_log ADD COLUMN access_decisions JSONB;

-- Dashboard L2 groups the tenant landing page by the stable agent
-- identifier. Partial index because the column is nullable for
-- pre-PR21 rows and writer paths that bypass the API-key context.
CREATE INDEX idx_audit_log_agent_key
    ON audit_log (tenant_id, agent_key_id)
    WHERE agent_key_id IS NOT NULL;

-- Audit Read API filter: enforce vs shadow over time, newest-first.
CREATE INDEX idx_audit_log_decision_mode
    ON audit_log (tenant_id, decision_mode, timestamp_utc DESC);

-- Fail-closed events are the high-value alert surface. Partial index
-- keeps the predicate scan tiny since the vast majority of rows are
-- NOT fail_closed.
CREATE INDEX idx_audit_log_fail_closed
    ON audit_log (tenant_id, fail_closed)
    WHERE fail_closed = true;

-- ---------------------------------------------------------------------
-- audit_log_decisions: normalised per-chunk rows
-- ---------------------------------------------------------------------

-- tenant_id is denormalised onto every decision row so the Leak
-- Report's per-tenant queries don't need to JOIN through audit_log
-- just to filter. Also acts as defense-in-depth for multi-tenant
-- isolation: a missing tenant_id filter on a SQL query still gets
-- caught by the column's NOT NULL semantics if combined with
-- application-level tenant scoping.
--
-- The writer guarantees this equals the parent audit_log.tenant_id;
-- the contract is not enforced at the SQL level because audit_log
-- only has a UNIQUE(trace_id) constraint, not a composite
-- UNIQUE(trace_id, tenant_id) that a composite FK would require,
-- and adding that composite UNIQUE on a hot write table is not
-- worth the index bloat.
CREATE TABLE audit_log_decisions (
    trace_id        TEXT    NOT NULL,
    tenant_id       TEXT    NOT NULL,
    ordinal         INTEGER NOT NULL,
    chunk_id        TEXT    NOT NULL,
    document_id     TEXT,
    allowed         BOOLEAN NOT NULL,
    reason          TEXT,
    required_scope  TEXT,
    region          TEXT,
    score           DOUBLE PRECISION,
    PRIMARY KEY (trace_id, ordinal),
    FOREIGN KEY (trace_id)
        REFERENCES audit_log (trace_id)
        ON DELETE CASCADE
);

-- audit_log_decisions inherits the no_update / no_delete contract
-- from audit_log: a decision row, once written, must be as
-- write-once as its parent audit row. Without these rules, an
-- attacker with table-write privileges could flip allowed=false ->
-- allowed=true without breaking the audit chain (the chain doesn't
-- cover per-chunk decisions in PR #21 — see the PR description for
-- the explicit non-goal).
CREATE RULE no_update_audit_decisions
    AS ON UPDATE TO audit_log_decisions DO INSTEAD NOTHING;
CREATE RULE no_delete_audit_decisions
    AS ON DELETE TO audit_log_decisions DO INSTEAD NOTHING;

-- Leak Report hot path: "all denials per document in this tenant".
-- Composite partial index supports the tenant filter directly without
-- joining audit_log.
CREATE INDEX idx_audit_log_decisions_tenant_denied
    ON audit_log_decisions (tenant_id, document_id)
    WHERE allowed = false AND document_id IS NOT NULL;

-- Cross-tenant forensic path: "all decisions for document X anywhere",
-- used by ops/security review tooling.
CREATE INDEX idx_audit_log_decisions_doc
    ON audit_log_decisions (document_id)
    WHERE document_id IS NOT NULL;
