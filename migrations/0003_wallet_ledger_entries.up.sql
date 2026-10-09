CREATE TABLE wallet_ledger_entries (
    id             uuid PRIMARY KEY,
    wallet_id      uuid NOT NULL REFERENCES wallets (id),
    transaction_id uuid NOT NULL REFERENCES wager_transactions (id),
    direction      text NOT NULL,
    amount         bigint NOT NULL,
    currency       char(3) NOT NULL,
    balance_before bigint NOT NULL,
    balance_after  bigint NOT NULL,
    wallet_version bigint NOT NULL,
    created_at     timestamptz NOT NULL,
    CONSTRAINT wallet_ledger_entries_amount_positive CHECK (amount > 0),
    CONSTRAINT wallet_ledger_entries_balance_before_not_negative CHECK (balance_before >= 0),
    CONSTRAINT wallet_ledger_entries_balance_after_not_negative CHECK (balance_after >= 0),
    CONSTRAINT wallet_ledger_entries_arithmetic CHECK (
        (direction = 'CREDIT' AND balance_after = balance_before + amount)
        OR (direction = 'DEBIT' AND balance_after = balance_before - amount)
    ),
    CONSTRAINT wallet_ledger_entries_wallet_transaction_key UNIQUE (wallet_id, transaction_id),
    CONSTRAINT wallet_ledger_entries_wallet_version_key UNIQUE (wallet_id, wallet_version)
);

CREATE FUNCTION wallet_ledger_entries_block_change() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'wallet ledger entries are append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER wallet_ledger_entries_no_update_or_delete
    BEFORE UPDATE OR DELETE ON wallet_ledger_entries
    FOR EACH ROW
    EXECUTE FUNCTION wallet_ledger_entries_block_change();

CREATE TRIGGER wallet_ledger_entries_no_truncate
    BEFORE TRUNCATE ON wallet_ledger_entries
    FOR EACH STATEMENT
    EXECUTE FUNCTION wallet_ledger_entries_block_change();
