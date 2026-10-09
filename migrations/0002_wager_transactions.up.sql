CREATE TABLE wager_transactions (
    id                                uuid PRIMARY KEY,
    origin                            text NOT NULL,
    kind                              text NOT NULL,
    status                            text NOT NULL,
    wallet_id                         uuid NOT NULL REFERENCES wallets (id),
    player_id                         uuid NOT NULL,
    amount                            bigint NOT NULL,
    currency                          char(3) NOT NULL,
    provider_id                       text,
    external_transaction_id           text,
    idempotency_key                   text,
    payload_hash                      text,
    round_id                          text,
    game_id                           text,
    reference_external_transaction_id text,
    reference_transaction_id          uuid REFERENCES wager_transactions (id),
    failure_code                      text,
    result_balance                    bigint,
    reference_expires_at              timestamptz,
    next_attempt_at                   timestamptz,
    attempts                          integer NOT NULL DEFAULT 0,
    error_count                       integer NOT NULL DEFAULT 0,
    correlation_id                    text NOT NULL,
    created_at                        timestamptz NOT NULL,
    updated_at                        timestamptz NOT NULL,
    completed_at                      timestamptz,
    CONSTRAINT wager_transactions_kind_known CHECK (kind IN ('OPENING', 'BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK')),
    CONSTRAINT wager_transactions_status_never_pending CHECK (status IN ('PENDING_REFERENCE', 'PROCESSED', 'REJECTED', 'FAILED')),
    CONSTRAINT wager_transactions_amount_not_negative CHECK (amount >= 0),
    CONSTRAINT wager_transactions_origin_matches_metadata CHECK (
        (
            origin = 'EXTERNAL'
            AND kind <> 'OPENING'
            AND provider_id IS NOT NULL
            AND external_transaction_id IS NOT NULL
            AND idempotency_key IS NOT NULL
            AND payload_hash IS NOT NULL
            AND round_id IS NOT NULL
            AND game_id IS NOT NULL
        ) OR (
            origin = 'INTERNAL'
            AND kind = 'OPENING'
            AND provider_id IS NULL
            AND external_transaction_id IS NULL
            AND idempotency_key IS NULL
            AND payload_hash IS NULL
            AND round_id IS NULL
            AND game_id IS NULL
            AND reference_external_transaction_id IS NULL
            AND reference_transaction_id IS NULL
        )
    ),
    CONSTRAINT wager_transactions_processed_reversal_has_reference CHECK (
        status <> 'PROCESSED' OR kind NOT IN ('REFUND', 'ROLLBACK') OR reference_transaction_id IS NOT NULL
    )
);

CREATE UNIQUE INDEX wager_transactions_provider_idempotency_key
    ON wager_transactions (provider_id, idempotency_key);

CREATE UNIQUE INDEX wager_transactions_provider_external_id_key
    ON wager_transactions (provider_id, external_transaction_id);

CREATE UNIQUE INDEX wager_transactions_one_opening_per_wallet
    ON wager_transactions (wallet_id)
    WHERE kind = 'OPENING';

CREATE UNIQUE INDEX wager_transactions_one_processed_reversal
    ON wager_transactions (reference_transaction_id)
    WHERE status = 'PROCESSED' AND kind IN ('REFUND', 'ROLLBACK');

CREATE INDEX wager_transactions_pending_reference_due
    ON wager_transactions (next_attempt_at)
    WHERE status = 'PENDING_REFERENCE';
