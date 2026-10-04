-- plugin/trigram_setup.sql - trigram indexes used for substring recall.
--
-- The source-owned services use gin_trgm_ops for short-query recall where a
-- lexeme match is too strict (document titles, QQ file names). pg_trgm ships
-- with the official PostgreSQL image; this script only makes the extension
-- explicit instead of relying on it being present by accident.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
