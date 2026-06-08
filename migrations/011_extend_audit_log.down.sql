-- Reverse PR #21 audit_log + audit_log_decisions changes. Drop the
-- decision rows first (they reference audit_log via FK), then the
-- indexes, then the columns.

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
