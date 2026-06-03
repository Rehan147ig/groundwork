ALTER TABLE audit_log DROP COLUMN IF EXISTS previous_hash;
DROP INDEX IF EXISTS idx_audit_log_tenant_timestamp;
