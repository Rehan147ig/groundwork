-- Align audit_log.tenant_id with the rest of the system (TEXT, matching api_keys)
-- and add the policy-mode / ACL-decision / reason columns required for a complete,
-- self-describing audit entry. Existing rows backfill to '' via the column default.
ALTER TABLE audit_log ALTER COLUMN tenant_id TYPE TEXT USING tenant_id::TEXT;
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS decision_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS acl_decision  TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS reason        TEXT NOT NULL DEFAULT '';
