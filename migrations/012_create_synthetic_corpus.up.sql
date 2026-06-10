-- PR #21: synthetic-corpus document attribution for the bank demo.
--
-- The Leak Report joins audit_log_decisions against
-- demo.document_permissions to produce "denied attempts per document
-- the user has no path to". Until a real document-attribution sync
-- connector lands (PR #20+ for SharePoint, similar work for other
-- sources), the bank demo populates these tables from personas.json so
-- the operator experience can be exercised end-to-end without depending
-- on Microsoft Graph.
--
-- Everything lives in schema 'demo' so a production deploy can
-- DROP SCHEMA demo CASCADE without touching anything load-bearing.
-- Nothing in the Engine.Execute path reads from these tables.

CREATE SCHEMA IF NOT EXISTS demo;

CREATE TABLE demo.documents (
    tenant_id         TEXT        NOT NULL,
    document_id       TEXT        NOT NULL,
    title             TEXT        NOT NULL,
    sensitivity_label TEXT,
    folder_id         TEXT,
    owner_principal   TEXT,
    anonymous_share   BOOLEAN     NOT NULL DEFAULT false,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, document_id)
);

CREATE INDEX idx_demo_documents_folder
    ON demo.documents (tenant_id, folder_id);

-- Anonymous-share documents are a specific Leak Report finding ("docs
-- exposed via a public link"). Partial index keeps it cheap.
CREATE INDEX idx_demo_documents_anon
    ON demo.documents (tenant_id)
    WHERE anonymous_share = true;

-- One row per grant. A document routinely has multiple rows because
-- inheritance, direct-user grants, group grants, and anonymous links
-- all stack independently. 'kind' is CHECK-constrained because the
-- Leak Report partitions rows by it ("inherited grants are the biggest
-- blast-radius surface" needs the partition to be reliable).
CREATE TABLE demo.document_permissions (
    tenant_id          TEXT NOT NULL,
    document_id        TEXT NOT NULL,
    principal_or_group TEXT NOT NULL,
    kind               TEXT NOT NULL CHECK (
        kind IN ('direct_user', 'direct_group', 'inherited', 'anon_link')
    ),
    source_label       TEXT,
    PRIMARY KEY (tenant_id, document_id, principal_or_group, kind),
    FOREIGN KEY (tenant_id, document_id)
        REFERENCES demo.documents (tenant_id, document_id)
        ON DELETE CASCADE
);

-- Reverse-lookup: "what does this principal have access to?" The
-- Dashboard L2 user-detail page hits this.
CREATE INDEX idx_demo_document_permissions_pg
    ON demo.document_permissions (tenant_id, principal_or_group);
