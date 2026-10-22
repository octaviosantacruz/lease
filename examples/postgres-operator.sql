-- Run explicitly with psql as the cluster administrator, after creating the
-- base NOLOGIN entitlement role. Names are quoted psql identifier variables.
-- This grants cluster-level CREATEROLE and pg_signal_backend: it is not a
-- database/namespace sandbox. Supply authentication separately, never here.
-- psql -X -v ON_ERROR_STOP=1 -v lease_operator=lease_operator \
--   -v lease_base_role=lease_demo_reader -v lease_database=lease_lab \
--   -f examples/postgres-operator.sql
BEGIN;
CREATE ROLE :"lease_operator" LOGIN NOSUPERUSER CREATEROLE NOCREATEDB
    NOINHERIT NOREPLICATION NOBYPASSRLS;
GRANT :"lease_base_role" TO :"lease_operator"
    WITH ADMIN TRUE, INHERIT FALSE, SET FALSE;
GRANT pg_signal_backend TO :"lease_operator"
    WITH ADMIN FALSE, INHERIT TRUE, SET FALSE;
GRANT CONNECT ON DATABASE :"lease_database" TO :"lease_operator";
COMMIT;
