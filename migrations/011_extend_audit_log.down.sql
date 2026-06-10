-- Reverse PR #21 schema changes. The five partial indexes that
-- originally lived here are now in migration 013; 013_down drops them
-- via DROP INDEX CONCURRENTLY before this file runs. The DROP INDEX
-- IF EXISTS statements below are kept as a safety net for DBs that
-- still have the original (PR #21 / PR #22 pre-MB-1) schema where the
-- indexes were created inside 011.

DROP INDEX IF EXISTS idx_audit_log_decisions_doc;
DROP INDEX IF EXISTS idx_audit_log_decisions_tenant_denied;
-- Rules go with the table; explicit drop kept for completeness.
DROP RULE IF EXISTS no_delete_audit_decisions ON audit_log_decisions;
DROP RULE IF EXISTS no_update_audit_decisions ON audit_log_decisions;
DROP TABLE IF EXISTS audit_log_decisions;

DROP INDEX IF EXISTS idx_audit_log_fail_closed;
DROP INDEX IF EXISTS idx_audit_log_decision_mode;
DROP INDEX IF EXISTS idx_audit_log_agent_key;

ALTER TABLE audit_log DROP COLUMN IF EXISTS access_decisions;
ALTER TABLE audit_log DROP COLUMN IF EXISTS agent_key_name;
ALTER TABLE audit_log DROP COLUMN IF EXISTS agent_key_id;
