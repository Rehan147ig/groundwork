-- Reverse PR #21 audit_log + audit_log_decisions changes. Drop the
-- decision rows first (they reference audit_log via FK), then the
-- indexes, then the columns.

DROP INDEX IF EXISTS idx_audit_log_decisions_denied;
DROP INDEX IF EXISTS idx_audit_log_decisions_doc;
DROP TABLE IF EXISTS audit_log_decisions;

DROP INDEX IF EXISTS idx_audit_log_fail_closed;
DROP INDEX IF EXISTS idx_audit_log_decision_mode;
DROP INDEX IF EXISTS idx_audit_log_agent_id;

ALTER TABLE audit_log DROP COLUMN IF EXISTS access_decisions;
ALTER TABLE audit_log DROP COLUMN IF EXISTS agent_id;
