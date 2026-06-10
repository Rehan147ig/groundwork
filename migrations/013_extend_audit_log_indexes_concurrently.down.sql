-- no-transaction
-- DROP INDEX CONCURRENTLY also requires running outside a transaction.

DROP INDEX CONCURRENTLY IF EXISTS idx_audit_log_decisions_doc;
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_log_decisions_tenant_denied;
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_log_fail_closed;
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_log_decision_mode;
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_log_agent_key;
