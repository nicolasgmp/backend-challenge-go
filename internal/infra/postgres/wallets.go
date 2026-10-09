package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wallet"
)

const selectWallet = `SELECT id, player_id, currency, balance, version, created_at, updated_at FROM wallets WHERE id = $1`

type WalletRepository struct {
	pool *pgxpool.Pool
}

var _ app.WalletRepository = (*WalletRepository)(nil)

func NewWalletRepository(pool *pgxpool.Pool) *WalletRepository {
	return &WalletRepository{pool: pool}
}

func (r *WalletRepository) Insert(ctx context.Context, w *wallet.Wallet) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	state := w.State()
	_, err = tx.Exec(ctx,
		`INSERT INTO wallets (id, player_id, currency, balance, version, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		state.ID.String(), state.PlayerID.String(), state.Balance.Currency().Code(), state.Balance.MinorUnits(),
		state.Version, state.CreatedAt, state.UpdatedAt)
	return mapError(err)
}

func (r *WalletRepository) Get(ctx context.Context, id ids.WalletID) (*wallet.Wallet, error) {
	return scanWallet(reader(ctx, r.pool).QueryRow(ctx, selectWallet, id.String()))
}

func (r *WalletRepository) GetForUpdate(ctx context.Context, id ids.WalletID) (*wallet.Wallet, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	return scanWallet(tx.QueryRow(ctx, selectWallet+` FOR UPDATE`, id.String()))
}

func (r *WalletRepository) UpdateBalance(ctx context.Context, w *wallet.Wallet) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	state := w.State()
	tag, err := tx.Exec(ctx, `UPDATE wallets SET balance = $2, version = $3, updated_at = $4 WHERE id = $1`,
		state.ID.String(), state.Balance.MinorUnits(), state.Version, state.UpdatedAt)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return app.ErrNotFound
	}
	return nil
}

func scanWallet(row pgx.Row) (*wallet.Wallet, error) {
	var (
		id, playerID, currency string
		balance, version       int64
		createdAt, updatedAt   time.Time
	)
	if err := row.Scan(&id, &playerID, &currency, &balance, &version, &createdAt, &updatedAt); err != nil {
		return nil, mapError(err)
	}

	walletID, err := ids.ParseWalletID(id)
	if err != nil {
		return nil, err
	}
	player, err := ids.ParsePlayerID(playerID)
	if err != nil {
		return nil, err
	}
	stored, err := toMoney(balance, currency)
	if err != nil {
		return nil, err
	}
	return wallet.Rehydrate(wallet.State{
		ID:        walletID,
		PlayerID:  player,
		Balance:   stored,
		Version:   version,
		CreatedAt: createdAt.UTC(),
		UpdatedAt: updatedAt.UTC(),
	})
}

func toMoney(units int64, code string) (money.Money, error) {
	currency, err := money.ParseCurrency(code)
	if err != nil {
		return money.Money{}, err
	}
	return money.FromMinorUnits(units, currency)
}
