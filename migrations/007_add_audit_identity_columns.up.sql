-- Record identity resolution on each audit entry: whether the query-time identity was
-- resolved to a canonical principal, and that principal's id (the audit user_id already
-- carries user:principal:<id> when canonical identity is enabled).
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS identity_resolution TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS principal_id TEXT NOT NULL DEFAULT '';
