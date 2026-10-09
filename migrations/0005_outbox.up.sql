CREATE TABLE outbox (
    event_id        uuid PRIMARY KEY,
    aggregate_id    text NOT NULL,
    group_key       text NOT NULL,
    event_type      text NOT NULL,
    version         integer NOT NULL,
    payload         jsonb NOT NULL,
    correlation_id  text NOT NULL,
    causation_id    text,
    occurred_at     timestamptz NOT NULL,
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL,
    locked_until    timestamptz,
    published_at    timestamptz
);

CREATE INDEX outbox_pending
    ON outbox (next_attempt_at)
    WHERE published_at IS NULL;

CREATE FUNCTION outbox_block_event_change() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'outbox event % is an immutable snapshot', OLD.event_id;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER outbox_event_is_immutable
    BEFORE UPDATE ON outbox
    FOR EACH ROW
    WHEN (
        OLD.event_id IS DISTINCT FROM NEW.event_id
        OR OLD.aggregate_id IS DISTINCT FROM NEW.aggregate_id
        OR OLD.group_key IS DISTINCT FROM NEW.group_key
        OR OLD.event_type IS DISTINCT FROM NEW.event_type
        OR OLD.version IS DISTINCT FROM NEW.version
        OR OLD.payload IS DISTINCT FROM NEW.payload
        OR OLD.correlation_id IS DISTINCT FROM NEW.correlation_id
        OR OLD.causation_id IS DISTINCT FROM NEW.causation_id
        OR OLD.occurred_at IS DISTINCT FROM NEW.occurred_at
    )
    EXECUTE FUNCTION outbox_block_event_change();
