-- Canonical principals: every external identity alias (JWT sub/oid/email, Entra id/upn/mail,
-- later Okta/Google) maps to one internal Groundwork principal per tenant. OpenFGA user
-- strings reference the principal as user:principal:<id>. No tenants FK in Phase 1
-- (tenants.id type not confirmed).

CREATE TABLE IF NOT EXISTS principals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_principals_tenant ON principals(tenant_id);

CREATE TABLE IF NOT EXISTS principal_aliases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id TEXT NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
    namespace TEXT NOT NULL,
    value TEXT NOT NULL,
    verified_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    UNIQUE(tenant_id, namespace, value)
);
CREATE INDEX IF NOT EXISTS idx_aliases_lookup ON principal_aliases(tenant_id, namespace, value);
CREATE INDEX IF NOT EXISTS idx_aliases_principal ON principal_aliases(tenant_id, principal_id);
