-- Migration 010: Microsoft Graph group memberships.
--
-- Stores the (group_id, member_id, member_type) edges enumerated from
-- Microsoft Graph. Both user-membership and nested-group membership are
-- captured here; the member_type column distinguishes them.
--
-- The composite primary key makes upserts naturally idempotent — re-running
-- the connector against the same directory produces zero new rows.

CREATE TABLE msgraph.group_memberships (
    tenant_id     TEXT NOT NULL,
    group_id      TEXT NOT NULL,                 -- references msgraph.groups.entra_group_id (logical, no FK)
    member_id     TEXT NOT NULL,                 -- entra_oid for users, entra_group_id for nested groups
    member_type   TEXT NOT NULL,                 -- 'user' | 'group'
    last_seen_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, group_id, member_id, member_type),
    CHECK (member_type IN ('user', 'group'))
);

-- The reverse lookup ("which groups is this principal a member of") is what
-- the leak report and shadow-mode replay use, so we index it explicitly.
CREATE INDEX idx_msgraph_group_memberships_member
    ON msgraph.group_memberships (tenant_id, member_id, member_type);
