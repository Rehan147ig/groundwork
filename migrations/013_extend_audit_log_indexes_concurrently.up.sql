-- no-transaction
-- This migration MUST run outside a transaction. CREATE INDEX
-- CONCURRENTLY is rejected by Postgres when wrapped in BEGIN/COMMIT;
-- the directive above instructs migrate/migrate (v4.x) — and any
-- runner that honors the leading `-- no-transaction` line — to skip
-- the transaction wrapper. For runners that don't honor it: invoke
-- with the `-x` / `--no-tx` flag, or run the SQL manually with psql
-- (which auto-commits each statement).
--
-- PR #22 review MB-1: the original migration 011 created these five
-- partial indexes inside a transaction with an ACCESS EXCLUSIVE lock
-- on audit_log + audit_log_decisions. On a hot tenant's audit_log
-- (millions of rows) this blocks every audit-write — and thus every
-- Engine.Execute that needs to record a trace — for the duration of
-- the index build. CREATE INDEX CONCURRENTLY trades a few extra
-- seconds of build time for no write blocking.
--
-- IF NOT EXISTS makes this migration idempotent: if a DB was already
-- migrated under the pre-MB-1 schema where 011 created the indexes
-- non-concurrently, those indexes already exist and these statements
-- become no-ops.

-- Dashboard L2 groups the tenant landing page by the stable agent
-- identifier. Partial index because the column is nullable for
-- pre-PR21 rows and writer paths that bypass the API-key context.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_log_agent_key
    ON audit_log (tenant_id, agent_key_id)
    WHERE agent_key_id IS NOT NULL;

-- Audit Read API filter: enforce vs shadow over time, newest-first.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_log_decision_mode
    ON audit_log (tenant_id, decision_mode, timestamp_utc DESC);

-- Fail-closed events are the high-value alert surface. Partial index
-- keeps the predicate scan tiny since the vast majority of rows are
-- NOT fail_closed.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_log_fail_closed
    ON audit_log (tenant_id, fail_closed)
    WHERE fail_closed = true;

-- Leak Report hot path: "all denials per document in this tenant".
-- Composite partial index supports the tenant filter directly without
-- joining audit_log.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_log_decisions_tenant_denied
    ON audit_log_decisions (tenant_id, document_id)
    WHERE allowed = false AND document_id IS NOT NULL;

-- Cross-tenant forensic path: "all decisions for document X anywhere",
-- used by ops/security review tooling.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_log_decisions_doc
    ON audit_log_decisions (document_id)
    WHERE document_id IS NOT NULL;
