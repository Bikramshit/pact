-- PACT MVP 0.1 schema. Applied idempotently at pact-core startup.

CREATE TABLE IF NOT EXISTS documents (
    kind        TEXT        NOT NULL,
    id          TEXT        NOT NULL,
    doc         JSONB       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, id)
);
CREATE INDEX IF NOT EXISTS documents_kind_created ON documents (kind, created_at);

-- One row per (capability, nonce). The primary key is the replay check.
CREATE TABLE IF NOT EXISTS nonces (
    cap_id   TEXT        NOT NULL,
    nonce    TEXT        NOT NULL,
    seen_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (cap_id, nonce)
);

-- Append-only decision log. record_json is the exact canonical text that was
-- hashed and signed; it is TEXT, not JSONB, so the database never reformats it.
CREATE TABLE IF NOT EXISTS evidence (
    seq          BIGINT      PRIMARY KEY,
    task_id      TEXT        NOT NULL,
    ts           TIMESTAMPTZ NOT NULL,
    record_json  TEXT        NOT NULL,
    hash         TEXT        NOT NULL
);
CREATE INDEX IF NOT EXISTS evidence_task ON evidence (task_id, seq);
