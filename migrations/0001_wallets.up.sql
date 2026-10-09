CREATE TABLE wallets (
    id         uuid PRIMARY KEY,
    player_id  uuid NOT NULL,
    currency   char(3) NOT NULL,
    balance    bigint NOT NULL,
    version    bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT wallets_balance_not_negative CHECK (balance >= 0),
    CONSTRAINT wallets_version_positive CHECK (version >= 1),
    CONSTRAINT wallets_player_currency_key UNIQUE (player_id, currency)
);
