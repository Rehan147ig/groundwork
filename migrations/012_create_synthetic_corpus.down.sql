-- Reverse PR #21 synthetic-corpus schema. CASCADE drops the two
-- tables and any FKs.

DROP SCHEMA IF EXISTS demo CASCADE;
