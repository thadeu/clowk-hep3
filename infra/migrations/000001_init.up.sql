-- The full SIP message lives as one JSONB document in `data`; the fields
-- we filter, group, or index on are exposed as STORED generated columns
-- over data->>'...' and indexed. Adding a captured field is just a JSON
-- key — it only needs a migration when it becomes query-hot enough to
-- want its own generated column.
--
-- ts is a generated TEXT column (not timestamptz): casting text→timestamptz
-- is STABLE, not IMMUTABLE, so Postgres rejects it in a generated column.
-- The writer stores ts as fixed-width ISO8601 UTC, so lexicographic
-- comparison equals chronological order — range filters work as text.
CREATE TABLE IF NOT EXISTS sip_messages (
  id            BIGSERIAL PRIMARY KEY,
  data          JSONB NOT NULL,

  ts            TEXT    GENERATED ALWAYS AS (data->>'ts')                  STORED,
  call_id       TEXT    GENERATED ALWAYS AS (data->>'call_id')             STORED,
  x_cid         TEXT    GENERATED ALWAYS AS (data->>'x_cid')               STORED,
  method        TEXT    GENERATED ALWAYS AS (data->>'method')              STORED,
  response_code INTEGER GENERATED ALWAYS AS ((data->>'response_code')::int) STORED,
  from_user     TEXT    GENERATED ALWAYS AS (data->>'from_user')           STORED,
  to_user       TEXT    GENERATED ALWAYS AS (data->>'to_user')             STORED,
  cseq          TEXT    GENERATED ALWAYS AS (data->>'cseq')                STORED,

  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_sip_call_id ON sip_messages (call_id, ts);
CREATE INDEX IF NOT EXISTS idx_sip_x_cid   ON sip_messages (x_cid, ts);
CREATE INDEX IF NOT EXISTS idx_sip_ts      ON sip_messages (ts);
CREATE INDEX IF NOT EXISTS idx_sip_from    ON sip_messages (from_user);
CREATE INDEX IF NOT EXISTS idx_sip_to      ON sip_messages (to_user);
