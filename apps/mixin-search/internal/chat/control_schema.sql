CREATE SCHEMA IF NOT EXISTS mixin_search_control;

-- The chat corpus keeps its own table rather than sharing control_states with a
-- different namespace: ADR-014 requires independent persistence state, and a
-- separate table makes that structural instead of a naming convention.
CREATE TABLE IF NOT EXISTS mixin_search_control.chat_control_states (
    namespace text PRIMARY KEY,
    generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
    payload bytea NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
