CREATE TABLE inbox (
    consumer_name text NOT NULL,
    message_id    text NOT NULL,
    payload_hash  text NOT NULL,
    received_at   timestamptz NOT NULL,
    completed_at  timestamptz,
    PRIMARY KEY (consumer_name, message_id)
);
