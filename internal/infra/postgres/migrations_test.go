//go:build integration

package postgres_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/infra/postgres/pgtest"
)

func TestWalletConstraints(t *testing.T) {
	pool := pgtest.NewDatabase(context.Background(), t)
	insertWallet(t, pool, walletA, 100000)

	refused(t, pool, "wallets_balance_not_negative", `UPDATE wallets SET balance = -1 WHERE id = $1`, walletA)
	refused(t, pool, "wallets_version_positive", `UPDATE wallets SET version = 0 WHERE id = $1`, walletA)
	refused(t, pool, "wallets_player_currency_key",
		`INSERT INTO wallets (id, player_id, currency, balance, version, created_at, updated_at)
		 VALUES ($1, $2, 'BRL', 0, 1, now(), now())`, walletB, walletA)
	exec(t, pool, `INSERT INTO wallets (id, player_id, currency, balance, version, created_at, updated_at)
		VALUES ($1, $2, 'USD', 0, 1, now(), now())`, walletB, walletA)
}

func TestWagerTransactionChecks(t *testing.T) {
	pool := pgtest.NewDatabase(context.Background(), t)
	insertWallet(t, pool, walletA, 100000)
	insertExternalRow(t, pool, betRow(betA, "tx-1"))

	pending := betRow(otherTxA, "tx-2")
	pending.status = "PENDING"
	refused(t, pool, "wager_transactions_status_never_pending", insertExternal, pending.args()...)

	unknownKind := betRow(otherTxA, "tx-2")
	unknownKind.kind = "BONUS"
	refused(t, pool, "wager_transactions_kind_known", insertExternal, unknownKind.args()...)

	externalOpening := betRow(otherTxA, "tx-2")
	externalOpening.kind = "OPENING"
	refused(t, pool, "wager_transactions_origin_matches_metadata", insertExternal, externalOpening.args()...)

	refused(t, pool, "wager_transactions_origin_matches_metadata",
		`INSERT INTO wager_transactions (id, origin, kind, status, wallet_id, player_id, amount, currency,
			round_id, game_id, payload_hash, idempotency_key, external_transaction_id, correlation_id, created_at, updated_at)
		 VALUES ($1, 'EXTERNAL', 'BET', 'PROCESSED', $2, $2, 2500, 'BRL', 'round-1', 'game-1', 'hash', 'key-9', 'tx-9', 'c', now(), now())`,
		otherTxA, walletA)

	refused(t, pool, "wager_transactions_origin_matches_metadata",
		`INSERT INTO wager_transactions (id, origin, kind, status, wallet_id, player_id, amount, currency,
			provider_id, correlation_id, created_at, updated_at)
		 VALUES ($1, 'INTERNAL', 'OPENING', 'PROCESSED', $2, $2, 2500, 'BRL', 'provider-a', 'c', now(), now())`,
		otherTxA, walletA)

	rejectedWithoutCode := betRow(otherTxA, "tx-2")
	rejectedWithoutCode.status = "REJECTED"
	refused(t, pool, "wager_transactions_failure_code_matches_status", insertExternal, rejectedWithoutCode.args()...)

	processedWithCode := betRow(otherTxA, "tx-2")
	processedWithCode.failureCode = "INSUFFICIENT_FUNDS"
	refused(t, pool, "wager_transactions_failure_code_matches_status", insertExternal, processedWithCode.args()...)

	pendingWithoutDeadline := betRow(otherTxA, "tx-2")
	pendingWithoutDeadline.status = "PENDING_REFERENCE"
	refused(t, pool, "wager_transactions_pending_reference_has_deadline", insertExternal, pendingWithoutDeadline.args()...)

	for _, kind := range []string{"REFUND", "ROLLBACK"} {
		reversalWithoutReference := betRow(otherTxA, "tx-2")
		reversalWithoutReference.kind = kind
		refused(t, pool, "wager_transactions_processed_reversal_has_reference", insertExternal, reversalWithoutReference.args()...)
	}

	refused(t, pool, "wager_transactions_origin_matches_metadata",
		`INSERT INTO wager_transactions (id, origin, kind, status, wallet_id, player_id, amount, currency, correlation_id, created_at, updated_at)
		 VALUES ($1, 'INTERNAL', 'BET', 'PROCESSED', $2, $2, 2500, 'BRL', 'c', now(), now())`, otherTxA, walletA)

	refused(t, pool, "wager_transactions_amount_not_negative",
		`INSERT INTO wager_transactions (id, origin, kind, status, wallet_id, player_id, amount, currency, correlation_id, created_at, updated_at)
		 VALUES ($1, 'INTERNAL', 'OPENING', 'PROCESSED', $2, $2, -1, 'BRL', 'c', now(), now())`, otherTxA, walletA)
}

func TestWagerTransactionUniqueIndexes(t *testing.T) {
	pool := pgtest.NewDatabase(context.Background(), t)
	insertWallet(t, pool, walletA, 100000)
	insertExternalRow(t, pool, betRow(betA, "tx-1"))

	sameKey := betRow(otherTxA, "tx-2")
	sameKey.key = "key-tx-1"
	refused(t, pool, "wager_transactions_provider_idempotency_key", insertExternal, sameKey.args()...)

	sameExternalID := betRow(otherTxA, "tx-1")
	sameExternalID.key = "key-other"
	refused(t, pool, "wager_transactions_provider_external_id_key", insertExternal, sameExternalID.args()...)

	otherProvider := betRow(otherTxA, "tx-1")
	otherProvider.provider = "provider-b"
	insertExternalRow(t, pool, otherProvider)

	const insertOpening = `INSERT INTO wager_transactions
		(id, origin, kind, status, wallet_id, player_id, amount, currency, result_balance, correlation_id, created_at, updated_at, completed_at)
		VALUES ($1, 'INTERNAL', 'OPENING', 'PROCESSED', $2, $2, 100000, 'BRL', 100000, 'c', now(), now(), now())`
	exec(t, pool, insertOpening, "0192f298-345e-7e38-af88-e43f851a81a1", walletA)
	refused(t, pool, "wager_transactions_one_opening_per_wallet", insertOpening, "0192f298-345e-7e38-af88-e43f851a81a2", walletA)
}

func TestOneProcessedReversalPerReference(t *testing.T) {
	pool := pgtest.NewDatabase(context.Background(), t)
	insertWallet(t, pool, walletA, 100000)
	insertExternalRow(t, pool, betRow(betA, "tx-1"))

	reversal := func(id, kind, status, externalID string, failureCode any) externalRow {
		row := betRow(id, externalID)
		row.kind, row.status, row.failureCode = kind, status, failureCode
		row.referenceExternalID, row.referenceID = "tx-1", betA
		return row
	}

	insertExternalRow(t, pool, reversal("0192f298-345e-7e38-af88-e43f851a81b1", "ROLLBACK", "REJECTED", "tx-r1", "REVERSAL_INSUFFICIENT_FUNDS"))
	insertExternalRow(t, pool, reversal("0192f298-345e-7e38-af88-e43f851a81b2", "REFUND", "REJECTED", "tx-r2", "REFERENCE_MISMATCH"))
	insertExternalRow(t, pool, reversal(refundA, "REFUND", "PROCESSED", "tx-r3", nil))

	refused(t, pool, "wager_transactions_one_processed_reversal", insertExternal,
		reversal("0192f298-345e-7e38-af88-e43f851a81b4", "ROLLBACK", "PROCESSED", "tx-r4", nil).args()...)
	refused(t, pool, "wager_transactions_one_processed_reversal", insertExternal,
		reversal("0192f298-345e-7e38-af88-e43f851a81b5", "REFUND", "PROCESSED", "tx-r5", nil).args()...)

	win := reversal("0192f298-345e-7e38-af88-e43f851a81b6", "WIN", "PROCESSED", "tx-w1", nil)
	insertExternalRow(t, pool, win)
}

func TestTerminalTransactionsCannotChange(t *testing.T) {
	pool := pgtest.NewDatabase(context.Background(), t)
	insertWallet(t, pool, walletA, 100000)

	rows := map[string]externalRow{
		"PROCESSED": betRow("0192f298-345e-7e38-af88-e43f851a81c1", "tx-1"),
		"REJECTED":  betRow("0192f298-345e-7e38-af88-e43f851a81c2", "tx-2"),
		"FAILED":    betRow("0192f298-345e-7e38-af88-e43f851a81c3", "tx-3"),
	}
	for status, row := range rows {
		row.status = status
		if status != "PROCESSED" {
			row.failureCode = "PROCESSING_FAILED"
		}
		insertExternalRow(t, pool, row)

		refused(t, pool, "wager transaction "+row.id+" is terminal and cannot change",
			`UPDATE wager_transactions SET status = 'PENDING_REFERENCE', failure_code = NULL, updated_at = now() WHERE id = $1`, row.id)
		refused(t, pool, "wager transaction "+row.id+" is terminal and cannot change",
			`UPDATE wager_transactions SET amount = 1 WHERE id = $1`, row.id)
	}

	waiting := betRow(refundA, "tx-4")
	waiting.kind, waiting.status = "REFUND", "PENDING_REFERENCE"
	waiting.referenceExternalID, waiting.deadline, waiting.resultBalance = "tx-0", time.Now().Add(time.Minute), nil
	insertExternalRow(t, pool, waiting)
	exec(t, pool, `UPDATE wager_transactions SET attempts = attempts + 1 WHERE id = $1`, refundA)
	exec(t, pool, `UPDATE wager_transactions SET status = 'REJECTED', failure_code = 'REFERENCE_NOT_FOUND' WHERE id = $1`, refundA)
}

const insertEntry = `INSERT INTO wallet_ledger_entries
	(id, wallet_id, transaction_id, direction, amount, currency, balance_before, balance_after, wallet_version, created_at)
	VALUES ($1, $2, $3, $4, $5, 'BRL', $6, $7, $8, now())`

func TestLedgerConstraints(t *testing.T) {
	pool := pgtest.NewDatabase(context.Background(), t)
	insertWallet(t, pool, walletA, 100000)
	insertExternalRow(t, pool, betRow(betA, "tx-1"))
	insertExternalRow(t, pool, betRow(otherTxA, "tx-2"))
	exec(t, pool, insertEntry, entryA, walletA, betA, "DEBIT", 2500, 100000, 97500, 2)

	refused(t, pool, "wallet_ledger_entries_arithmetic", insertEntry, entryB, walletA, otherTxA, "DEBIT", 2500, 97500, 98000, 3)
	refused(t, pool, "wallet_ledger_entries_arithmetic", insertEntry, entryB, walletA, otherTxA, "CREDIT", 2500, 97500, 95000, 3)
	refused(t, pool, "wallet_ledger_entries_arithmetic", insertEntry, entryB, walletA, otherTxA, "TRANSFER", 2500, 97500, 95000, 3)
	refused(t, pool, "wallet_ledger_entries_amount_positive", insertEntry, entryB, walletA, otherTxA, "DEBIT", 0, 97500, 97500, 3)
	refused(t, pool, "wallet_ledger_entries_balance_before_not_negative", insertEntry, entryB, walletA, otherTxA, "CREDIT", 2500, -500, 2000, 3)
	refused(t, pool, "wallet_ledger_entries_balance_after_not_negative", insertEntry, entryB, walletA, otherTxA, "DEBIT", 2500, 1000, -1500, 3)
	refused(t, pool, "wallet_ledger_entries_wallet_transaction_key", insertEntry, entryB, walletA, betA, "DEBIT", 2500, 97500, 95000, 3)
	refused(t, pool, "wallet_ledger_entries_wallet_version_key", insertEntry, entryB, walletA, otherTxA, "DEBIT", 2500, 97500, 95000, 2)
	exec(t, pool, insertEntry, entryB, walletA, otherTxA, "DEBIT", 2500, 97500, 95000, 3)
}

func TestLedgerIsAppendOnly(t *testing.T) {
	pool := pgtest.NewDatabase(context.Background(), t)
	insertWallet(t, pool, walletA, 100000)
	insertExternalRow(t, pool, betRow(betA, "tx-1"))
	exec(t, pool, insertEntry, entryA, walletA, betA, "DEBIT", 2500, 100000, 97500, 2)

	const appendOnly = "wallet ledger entries are append-only"
	refused(t, pool, appendOnly, `UPDATE wallet_ledger_entries SET amount = 1 WHERE id = $1`, entryA)
	refused(t, pool, appendOnly, `DELETE FROM wallet_ledger_entries WHERE id = $1`, entryA)
	refused(t, pool, appendOnly, `TRUNCATE wallet_ledger_entries`)

	var entries int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM wallet_ledger_entries`).Scan(&entries); err != nil {
		t.Fatalf("count: %v", err)
	}
	if entries != 1 {
		t.Fatalf("entries = %d, want the original one", entries)
	}
}

func TestInboxKey(t *testing.T) {
	pool := pgtest.NewDatabase(context.Background(), t)
	const insert = `INSERT INTO inbox (consumer_name, message_id, payload_hash, received_at) VALUES ($1, $2, 'hash', now())`

	exec(t, pool, insert, "wager-consumer", "msg-1")
	refused(t, pool, "inbox_pkey", insert, "wager-consumer", "msg-1")
	exec(t, pool, insert, "other-consumer", "msg-1")
	exec(t, pool, insert, "wager-consumer", "msg-2")
}

func TestOutboxEventIsImmutable(t *testing.T) {
	pool := pgtest.NewDatabase(context.Background(), t)
	exec(t, pool, `INSERT INTO outbox (event_id, aggregate_id, group_key, event_type, version, payload, correlation_id, occurred_at, next_attempt_at)
		VALUES ($1, 'aggregate-1', 'wallet-1', 'WalletBalanceChanged', 1, '{"a":1}', 'correlation-1', now(), now())`, eventA)

	immutable := "outbox event " + eventA + " is an immutable snapshot"
	refused(t, pool, immutable, `UPDATE outbox SET payload = '{"a":2}' WHERE event_id = $1`, eventA)
	refused(t, pool, immutable, `UPDATE outbox SET event_type = 'Other' WHERE event_id = $1`, eventA)
	refused(t, pool, immutable, `UPDATE outbox SET group_key = 'wallet-2' WHERE event_id = $1`, eventA)
	refused(t, pool, immutable, `UPDATE outbox SET causation_id = 'message-1' WHERE event_id = $1`, eventA)

	exec(t, pool, `UPDATE outbox SET attempts = attempts + 1, last_error = 'broker down', next_attempt_at = now(), locked_until = now()
		WHERE event_id = $1`, eventA)
	exec(t, pool, `UPDATE outbox SET published_at = now() WHERE event_id = $1`, eventA)
}

func TestMigrationsApplyAndRevert(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewEmptyDatabase(ctx, t)

	up, err := pgtest.Migrations("../../../migrations", "up")
	if err != nil {
		t.Fatalf("Migrations: %v", err)
	}
	down, err := pgtest.Migrations("../../../migrations", "down")
	if err != nil {
		t.Fatalf("Migrations: %v", err)
	}
	if len(up) != len(down) {
		t.Fatalf("%d up migrations and %d down migrations", len(up), len(down))
	}

	objects := func() int {
		var count int
		err := pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM pg_tables WHERE schemaname = 'public')
			+ (SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'public')
			+ (SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal)
			+ (SELECT count(*) FROM pg_indexes WHERE schemaname = 'public')`).Scan(&count)
		if err != nil {
			t.Fatalf("count schema objects: %v", err)
		}
		return count
	}

	for round := 1; round <= 2; round++ {
		for _, migration := range up {
			exec(t, pool, migration.SQL)
		}
		if objects() == 0 {
			t.Fatalf("round %d: the up migrations created nothing", round)
		}
		for _, migration := range slices.Backward(down) {
			exec(t, pool, migration.SQL)
		}
		if left := objects(); left != 0 {
			t.Fatalf("round %d: %d tables, functions, triggers or indexes left after reverting", round, left)
		}
	}
}
