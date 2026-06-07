-- Migration 009: Shadow-mode query replay tables.
--
-- The replay tool (cmd/replay-queries, future PR) drives a CSV of
-- (persona, query) pairs against the runtime in shadow mode. Each invocation
-- creates a replay_runs row; each query creates a replay_queries row indexing
-- the audit_log.trace_id produced by that call. The leak report joins from
-- audit_log -> replay_queries -> msgraph.documents to attribute every shadow
-- decision back to the (persona, query) that produced it.
--
-- We intentionally do NOT add a foreign key from replay_queries.trace_id to
-- audit_log.trace_id: audit_log has its own retention policy and per-tenant
-- partitioning is on the roadmap; a cross-schema FK would over-couple them.

CREATE TABLE msgraph.replay_runs (
    replay_id          UUID PRIMARY KEY,
    tenant_id          TEXT NOT NULL,
    customer_label     TEXT,
    started_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at       TIMESTAMPTZ,
    query_count        INTEGER NOT NULL DEFAULT 0,
    would_block_count  INTEGER NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_msgraph_replay_runs_tenant ON msgraph.replay_runs (tenant_id, started_at DESC);

CREATE TABLE msgraph.replay_queries (
    replay_id   UUID NOT NULL REFERENCES msgraph.replay_runs (replay_id) ON DELETE CASCADE,
    ordinal     INTEGER NOT NULL,
    persona_id  TEXT NOT NULL,
    query_text  TEXT NOT NULL,
    trace_id    TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (replay_id, ordinal)
);
CREATE INDEX idx_msgraph_replay_queries_trace ON msgraph.replay_queries (trace_id);
