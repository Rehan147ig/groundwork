-- Reverse migration 008. Drops every catalog table and the msgraph schema.
-- Safe to run on a fresh database (IF EXISTS guards).

DROP TABLE IF EXISTS msgraph.document_permissions;
DROP TABLE IF EXISTS msgraph.documents;
DROP TABLE IF EXISTS msgraph.drives;
DROP TABLE IF EXISTS msgraph.sites;
DROP TABLE IF EXISTS msgraph.groups;
DROP TABLE IF EXISTS msgraph.principals;
DROP TABLE IF EXISTS msgraph.runs;

DROP SCHEMA IF EXISTS msgraph;
