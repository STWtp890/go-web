-- service/service_roles.sql
--
-- Database accounts and cross-schema grants for the source-owned services.
--
-- Each service writes its own schema with its own role. The only cross-schema
-- privilege in the whole layout is a read-only grant that lets document-search
-- read the document service change Outbox it consumes. There is deliberately no
-- grant that lets any service write another service's business tables.
--
-- These are development accounts: the passwords are fixed and public. A real
-- deployment must supply its own credentials; the point of this file is that the
-- isolation is expressed in the database, not only in code review.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'document_service_writer') THEN
        CREATE ROLE document_service_writer LOGIN PASSWORD 'document_service';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'document_search_writer') THEN
        CREATE ROLE document_search_writer LOGIN PASSWORD 'document_search';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'qq_search_writer') THEN
        CREATE ROLE qq_search_writer LOGIN PASSWORD 'qq_search';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'go_web_app') THEN
        CREATE ROLE go_web_app LOGIN PASSWORD 'go_web_app';
    END IF;
END $$;

GRANT CONNECT ON DATABASE gin_demo TO document_service_writer, document_search_writer, qq_search_writer, go_web_app;

-- go-web: the Web account tables and nothing else.
--
-- Code review already stopped go-web from writing document business tables; this
-- is the same statement in the database, so a future change that tried it would
-- be refused by PostgreSQL rather than by a reviewer. The role has no privilege
-- on any source-owned schema, and it never reaches the document service's own
-- schema either - the Web document surface talks to document-service over gRPC.
--
-- The Chat tables are deliberately NOT granted: the Chat surface is not
-- registered, and switching it on must come with its own grant change. Leaving
-- the privilege out is what makes that a decision instead of an accident.
GRANT USAGE ON SCHEMA public TO go_web_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.users TO go_web_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.managers TO go_web_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.manager_registration_requests TO go_web_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO go_web_app;
REVOKE ALL ON SCHEMA document_service, document_search, qq_search FROM go_web_app;
REVOKE ALL ON ALL TABLES IN SCHEMA document_service FROM go_web_app;
REVOKE ALL ON ALL TABLES IN SCHEMA document_search FROM go_web_app;
REVOKE ALL ON ALL TABLES IN SCHEMA qq_search FROM go_web_app;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA document_service FROM go_web_app;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA document_search FROM go_web_app;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA qq_search FROM go_web_app;

-- document-service: full ownership of its own schema, nothing else.
GRANT USAGE, CREATE ON SCHEMA document_service TO document_service_writer;
ALTER ROLE document_service_writer SET search_path = document_service, public;

-- document-search: full ownership of its own schema plus read-only access to the
-- Outbox stream it consumes. SELECT is granted per column so the consumer cannot
-- even read document body columns from the fact source.
GRANT USAGE, CREATE ON SCHEMA document_search TO document_search_writer;
ALTER ROLE document_search_writer SET search_path = document_search, public;
GRANT USAGE ON SCHEMA document_service TO document_search_writer;
GRANT SELECT (sequence, event_id, document_id, event_kind, aggregate_revision, payload, occurred_at)
    ON document_service.document_events TO document_search_writer;

-- qq-search: its own schema only. It receives QQ source events over the network
-- from py-agent and never reads the QQ source database directly.
GRANT USAGE, CREATE ON SCHEMA qq_search TO qq_search_writer;
ALTER ROLE qq_search_writer SET search_path = qq_search, public;

-- Objects created later by each service role inherit the same rights for its own
-- schema, so a new table cannot accidentally become unreachable or shared.
ALTER DEFAULT PRIVILEGES FOR ROLE document_service_writer IN SCHEMA document_service
    GRANT ALL ON TABLES TO document_service_writer;
ALTER DEFAULT PRIVILEGES FOR ROLE document_service_writer IN SCHEMA document_service
    GRANT ALL ON SEQUENCES TO document_service_writer;
ALTER DEFAULT PRIVILEGES FOR ROLE document_search_writer IN SCHEMA document_search
    GRANT ALL ON TABLES TO document_search_writer;
ALTER DEFAULT PRIVILEGES FOR ROLE qq_search_writer IN SCHEMA qq_search
    GRANT ALL ON TABLES TO qq_search_writer;
ALTER DEFAULT PRIVILEGES FOR ROLE qq_search_writer IN SCHEMA qq_search
    GRANT ALL ON SEQUENCES TO qq_search_writer;

-- The schema baseline is applied by the bootstrap superuser, so the service
-- roles need explicit rights on the objects that already exist. The separate
-- deployments/postgresql/verify-service-isolation.ps1 gate proves the resulting
-- matrix: each writer can write its own schema, cannot write another service's
-- tables, and document-search can only read the Outbox.
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA document_service TO document_service_writer;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA document_service TO document_service_writer;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA document_search TO document_search_writer;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA document_search TO document_search_writer;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA qq_search TO qq_search_writer;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA qq_search TO qq_search_writer;

-- Explicitly revoke the one thing that would break the model: a service writing
-- another service's tables. PostgreSQL never granted it, and this makes the
-- intent auditable.
REVOKE INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA document_search FROM document_service_writer;
REVOKE INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA qq_search FROM document_service_writer;
REVOKE ALL ON SCHEMA document_search FROM document_service_writer;
REVOKE ALL ON SCHEMA qq_search FROM document_service_writer;

REVOKE INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA document_service FROM document_search_writer;
REVOKE INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA qq_search FROM document_search_writer;
REVOKE ALL ON SCHEMA qq_search FROM document_search_writer;

REVOKE INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA document_service FROM qq_search_writer;
REVOKE INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA document_search FROM qq_search_writer;
REVOKE ALL ON SCHEMA document_service FROM qq_search_writer;
REVOKE ALL ON SCHEMA document_search FROM qq_search_writer;
