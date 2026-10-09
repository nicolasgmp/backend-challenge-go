package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
)

type LedgerRepository struct {
	pool *pgxpool.Pool
}

var _ app.LedgerRepository = (*LedgerRepository)(nil)

func NewLedgerRepository(pool *pgxpool.Pool) *LedgerRepository {
	return &LedgerRepository{pool: pool}
}

func (r *LedgerRepository) Insert(ctx context.Context, entry ledger.Entry) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	fields := entry.Fields()
	_, err = tx.Exec(ctx,
		`INSERT INTO wallet_ledger_entries (id, wallet_id, transaction_id, direction, amount, currency,
			balance_before, balance_after, wallet_version, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		entry.ID().String(), fields.WalletID.String(), fields.TransactionID.String(), string(fields.Direction),
		fields.Amount.MinorUnits(), fields.Amount.Currency().Code(), fields.BalanceBefore.MinorUnits(),
		fields.BalanceAfter.MinorUnits(), fields.WalletVersion, entry.CreatedAt())
	return mapError(err)
}

func (r *LedgerRepository) ListPage(ctx context.Context, walletID ids.WalletID, afterVersion int64, limit int) ([]ledger.Entry, error) {
	rows, err := reader(ctx, r.pool).Query(ctx,
		`SELECT id, transaction_id, direction, amount, currency, balance_before, balance_after, wallet_version, created_at
		 FROM wallet_ledger_entries
		 WHERE wallet_id = $1 AND wallet_version > $2
		 ORDER BY wallet_version
		 LIMIT $3`, walletID.String(), afterVersion, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var page []ledger.Entry
	for rows.Next() {
		entry, err := scanEntry(rows, walletID)
		if err != nil {
			return nil, err
		}
		page = append(page, entry)
	}
	return page, mapError(rows.Err())
}

func (r *LedgerRepository) Totals(ctx context.Context, walletID ids.WalletID) (app.LedgerTotals, error) {
	var totals app.LedgerTotals
	err := reader(ctx, r.pool).QueryRow(ctx,
		`SELECT COALESCE(SUM(amount) FILTER (WHERE direction = 'CREDIT'), 0)::bigint,
			COALESCE(SUM(amount) FILTER (WHERE direction = 'DEBIT'), 0)::bigint,
			count(*)
		 FROM wallet_ledger_entries
		 WHERE wallet_id = $1`, walletID.String()).Scan(&totals.CreditUnits, &totals.DebitUnits, &totals.Entries)
	return totals, mapError(err)
}

func scanEntry(row pgx.Row, walletID ids.WalletID) (ledger.Entry, error) {
	var (
		id, transactionID, direction, currency string
		amount, before, after, version         int64
		createdAt                              time.Time
	)
	if err := row.Scan(&id, &transactionID, &direction, &amount, &currency, &before, &after, &version, &createdAt); err != nil {
		return ledger.Entry{}, mapError(err)
	}

	entryID, err := ids.ParseEntryID(id)
	if err != nil {
		return ledger.Entry{}, err
	}
	fields := ledger.Fields{WalletID: walletID, Direction: ledger.Direction(direction), WalletVersion: version}
	if fields.TransactionID, err = ids.ParseTransactionID(transactionID); err != nil {
		return ledger.Entry{}, err
	}
	if fields.Amount, err = toMoney(amount, currency); err != nil {
		return ledger.Entry{}, err
	}
	if fields.BalanceBefore, err = toMoney(before, currency); err != nil {
		return ledger.Entry{}, err
	}
	if fields.BalanceAfter, err = toMoney(after, currency); err != nil {
		return ledger.Entry{}, err
	}
	return ledger.Rehydrate(entryID, fields, createdAt.UTC())
}
