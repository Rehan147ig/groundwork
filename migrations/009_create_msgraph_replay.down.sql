-- Reverse migration 009. Drops the replay tables (and any rows in them).
-- The CASCADE on replay_queries.replay_id means dropping replay_runs first
-- would already cascade; explicit ordering kept for clarity.

DROP TABLE IF EXISTS msgraph.replay_queries;
DROP TABLE IF EXISTS msgraph.replay_runs;
