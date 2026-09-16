CREATE SCHEMA IF NOT EXISTS mixin_search_control;

CREATE TABLE IF NOT EXISTS mixin_search_control.control_states (
    namespace text PRIMARY KEY,
    generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
    payload bytea NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
