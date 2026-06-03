ALTER TABLE audit_log DROP COLUMN IF EXISTS reason;
ALTER TABLE audit_log DROP COLUMN IF EXISTS acl_decision;
ALTER TABLE audit_log DROP COLUMN IF EXISTS decision_mode;
-- Note: reverting tenant_id back to UUID requires all values to be valid UUIDs.
ALTER TABLE audit_log ALTER COLUMN tenant_id TYPE UUID USING tenant_id::uuid;
