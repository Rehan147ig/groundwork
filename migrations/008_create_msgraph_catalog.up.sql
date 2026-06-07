-- Migration 008: Microsoft Graph connector catalog schema.
--
-- These tables hold the connector's own view of the customer's Microsoft tenant
-- (principals, groups, sites, drives, documents, document_permissions, runs).
-- The connector writes here; the leak report (future PR) reads from here. The
-- audit_log (migration 003) and OpenFGA tuples (in the openfga store) remain
-- the authoritative records of decisions and authorization state respectively.
--
-- Conventions:
--   - All tables live in the msgraph schema (clean isolation from runtime tables).
--   - All tables carry tenant_id + created_at + updated_at.
--   - All tables are reversible (see 008_create_msgraph_catalog.down.sql).

CREATE SCHEMA IF NOT EXISTS msgraph;

-- One row per `enumerate` invocation. Tracks status + counts for the leak report
-- methodology page ("snapshot taken at X, N items synced").
CREATE TABLE msgraph.runs (
    run_id         UUID PRIMARY KEY,
    tenant_id      TEXT NOT NULL,
    started_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at   TIMESTAMPTZ,
    status         TEXT NOT NULL DEFAULT 'in_progress',
    item_counts    JSONB,
    error_summary  TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_msgraph_runs_tenant ON msgraph.runs (tenant_id, started_at DESC);

-- Canonicalized directory users. Joins to principal_aliases (migration 006) via
-- gw_canonical_id. account_enabled mirrors Entra; flipping to false triggers
-- revocation in future PRs.
CREATE TABLE msgraph.principals (
    tenant_id        TEXT NOT NULL,
    entra_oid        TEXT NOT NULL,
    gw_canonical_id  TEXT NOT NULL,
    upn              TEXT,
    email            TEXT,
    display_name     TEXT,
    account_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
    last_seen_at     TIMESTAMPTZ,
    attributes       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, entra_oid)
);
CREATE INDEX idx_msgraph_principals_canonical ON msgraph.principals (gw_canonical_id);

-- Entra security and M365 groups. Membership tuples live in OpenFGA, not here.
CREATE TABLE msgraph.groups (
    tenant_id       TEXT NOT NULL,
    entra_group_id  TEXT NOT NULL,
    display_name    TEXT,
    group_type      TEXT,
    member_count    INTEGER NOT NULL DEFAULT 0,
    last_seen_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, entra_group_id)
);

-- One row per chosen SharePoint site. MIP scope: one site per customer.
CREATE TABLE msgraph.sites (
    tenant_id        TEXT NOT NULL,
    site_id          TEXT NOT NULL,
    web_url          TEXT,
    display_name     TEXT,
    first_synced_at  TIMESTAMPTZ,
    last_synced_at   TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, site_id)
);

CREATE TABLE msgraph.drives (
    tenant_id    TEXT NOT NULL,
    drive_id     TEXT NOT NULL,
    site_id      TEXT NOT NULL,
    drive_type   TEXT,
    name         TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, drive_id)
);
CREATE INDEX idx_msgraph_drives_site ON msgraph.drives (tenant_id, site_id);

-- Files only (no folders — folders live as implicit OpenFGA folder tuples).
-- anonymous_share is the "anyone with the link" flag — surfaced loudly in the
-- leak report.
CREATE TABLE msgraph.documents (
    tenant_id          TEXT NOT NULL,
    item_id            TEXT NOT NULL,
    drive_id           TEXT NOT NULL,
    parent_item_id     TEXT,
    name               TEXT,
    web_url            TEXT,
    mime_type          TEXT,
    sensitivity_label  TEXT,
    last_modified_at   TIMESTAMPTZ,
    owner_oid          TEXT,
    anonymous_share    BOOLEAN NOT NULL DEFAULT FALSE,
    run_id             UUID,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, item_id)
);
CREATE INDEX idx_msgraph_documents_drive ON msgraph.documents (tenant_id, drive_id);
CREATE INDEX idx_msgraph_documents_anon  ON msgraph.documents (tenant_id) WHERE anonymous_share;

-- The "why was this user allowed" derivation table. Every grant the connector
-- writes a tuple for also lands here so the leak report can show the
-- permission path (direct user / direct group / inherited from folder / anon).
CREATE TABLE msgraph.document_permissions (
    tenant_id           TEXT NOT NULL,
    item_id             TEXT NOT NULL,
    principal_or_group  TEXT NOT NULL,
    kind                TEXT NOT NULL,
    source_label        TEXT,
    first_seen_at       TIMESTAMPTZ,
    last_seen_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, item_id, principal_or_group, kind)
);
CREATE INDEX idx_msgraph_perms_item ON msgraph.document_permissions (tenant_id, item_id);
