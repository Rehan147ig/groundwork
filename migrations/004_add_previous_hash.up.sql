-- Add previous_hash column for cryptographic hash chaining
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS previous_hash TEXT;

-- Index for efficient chain lookups (fetching latest entry per tenant)
CREATE INDEX IF NOT EXISTS idx_audit_log_tenant_timestamp
    ON audit_log (tenant_id, timestamp_utc DESC);
