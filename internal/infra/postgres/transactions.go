package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
)

const (
	originExternal = "EXTERNAL"
	originInternal = "INTERNAL"

	selectTransaction = `SELECT id, kind, status, wallet_id, player_id, amount, currency,
		provider_id, external_transaction_id, idempotency_key, payload_hash, round_id, game_id,
		reference_external_transaction_id, reference_transaction_id, failure_code, result_balance,
		reference_expires_at, next_attempt_at, attempts, error_count, correlation_id,
		created_at, updated_at, completed_at
		FROM wager_transactions`
)

type TransactionRepository struct {
	pool *pgxpool.Pool
}

var _ app.TransactionRepository = (*TransactionRepository)(nil)

func NewTransactionRepository(pool *pgxpool.Pool) *TransactionRepository {
	return &TransactionRepository{pool: pool}
}

func (r *TransactionRepository) Insert(ctx context.Context, t *wager.Transaction) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	state := t.State()
	origin := originInternal
	if state.Kind.IsExternal() {
		origin = originExternal
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO wager_transactions (id, origin, kind, status, wallet_id, player_id, amount, currency,
			provider_id, external_transaction_id, idempotency_key, payload_hash, round_id, game_id,
			reference_external_transaction_id, reference_transaction_id, failure_code, result_balance,
			reference_expires_at, next_attempt_at, attempts, error_count, correlation_id,
			created_at, updated_at, completed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
			$21, $22, $23, $24, $25, $26)`,
		state.ID.String(), origin, string(state.Kind), string(state.Status), state.WalletID.String(),
		state.PlayerID.String(), state.Amount.MinorUnits(), state.Amount.Currency().Code(),
		nullText(state.External.ProviderID.String()), nullText(state.External.ExternalID.String()),
		nullText(state.External.IdempotencyKey.String()), nullText(state.External.PayloadHash),
		nullText(state.External.RoundID.String()), nullText(state.External.GameID.String()),
		nullText(state.External.ReferenceExternalID.String()), nullReference(state.ReferenceID),
		nullText(string(state.FailureCode)), nullBalance(state.ResultBalance),
		nullTime(state.ReferenceExpiresAt), nullTime(state.NextAttemptAt), state.Attempts, state.ErrorCount,
		state.CorrelationID, state.CreatedAt, state.UpdatedAt, nullTime(state.CompletedAt))
	return mapError(err)
}

func (r *TransactionRepository) Update(ctx context.Context, t *wager.Transaction) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	state := t.State()
	_, err = tx.Exec(ctx,
		`UPDATE wager_transactions SET status = $2, reference_transaction_id = $3, failure_code = $4,
			result_balance = $5, next_attempt_at = $6, attempts = $7, error_count = $8, updated_at = $9, completed_at = $10
		 WHERE id = $1`,
		state.ID.String(), string(state.Status), nullReference(state.ReferenceID), nullText(string(state.FailureCode)),
		nullBalance(state.ResultBalance), nullTime(state.NextAttemptAt), state.Attempts, state.ErrorCount,
		state.UpdatedAt, nullTime(state.CompletedAt))
	return mapError(err)
}

func (r *TransactionRepository) Get(ctx context.Context, id ids.TransactionID) (*wager.Transaction, error) {
	return scanTransaction(reader(ctx, r.pool).QueryRow(ctx, selectTransaction+` WHERE id = $1`, id.String()))
}

func (r *TransactionRepository) GetForUpdate(ctx context.Context, id ids.TransactionID) (*wager.Transaction, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	return scanTransaction(tx.QueryRow(ctx, selectTransaction+` WHERE id = $1 FOR UPDATE`, id.String()))
}

func (r *TransactionRepository) FindByIdempotencyKey(ctx context.Context, providerID ids.ProviderID, key ids.IdempotencyKey) (*wager.Transaction, error) {
	return scanTransaction(reader(ctx, r.pool).QueryRow(ctx,
		selectTransaction+` WHERE provider_id = $1 AND idempotency_key = $2`, providerID.String(), key.String()))
}

func (r *TransactionRepository) FindByExternalID(ctx context.Context, providerID ids.ProviderID, externalID ids.ExternalTransactionID) (*wager.Transaction, error) {
	return scanTransaction(reader(ctx, r.pool).QueryRow(ctx,
		selectTransaction+` WHERE provider_id = $1 AND external_transaction_id = $2`, providerID.String(), externalID.String()))
}

func (r *TransactionRepository) HasProcessedReversal(ctx context.Context, referenceID ids.TransactionID) (bool, error) {
	var found bool
	err := reader(ctx, r.pool).QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM wager_transactions
			WHERE reference_transaction_id = $1 AND status = 'PROCESSED' AND kind IN ('REFUND', 'ROLLBACK')
		)`, referenceID.String()).Scan(&found)
	return found, mapError(err)
}

func (r *TransactionRepository) ListDuePendingReferences(ctx context.Context, now time.Time, limit int) ([]*wager.Transaction, error) {
	rows, err := reader(ctx, r.pool).Query(ctx,
		selectTransaction+` WHERE status = 'PENDING_REFERENCE' AND next_attempt_at <= $1 ORDER BY next_attempt_at LIMIT $2`,
		now, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var due []*wager.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		due = append(due, t)
	}
	return due, mapError(rows.Err())
}

func scanTransaction(row pgx.Row) (*wager.Transaction, error) {
	var (
		id, kind, status, walletID, playerID, currency, correlationID string
		amount                                                        int64
		providerID, externalID, key, payloadHash, roundID, gameID     *string
		referenceExternalID, referenceID, failureCode                 *string
		resultBalance                                                 *int64
		expiresAt, nextAttemptAt, completedAt                         *time.Time
		attempts, errorCount                                          int
		createdAt, updatedAt                                          time.Time
	)
	err := row.Scan(&id, &kind, &status, &walletID, &playerID, &amount, &currency,
		&providerID, &externalID, &key, &payloadHash, &roundID, &gameID,
		&referenceExternalID, &referenceID, &failureCode, &resultBalance,
		&expiresAt, &nextAttemptAt, &attempts, &errorCount, &correlationID,
		&createdAt, &updatedAt, &completedAt)
	if err != nil {
		return nil, mapError(err)
	}

	state := wager.State{
		Kind:               wager.Kind(kind),
		Status:             wager.Status(status),
		FailureCode:        failure.Code(text(failureCode)),
		ReferenceExpiresAt: utc(expiresAt),
		NextAttemptAt:      utc(nextAttemptAt),
		Attempts:           attempts,
		ErrorCount:         errorCount,
		CorrelationID:      correlationID,
		CreatedAt:          createdAt.UTC(),
		UpdatedAt:          updatedAt.UTC(),
		CompletedAt:        utc(completedAt),
	}
	state.External.PayloadHash = text(payloadHash)

	if state.ID, err = ids.ParseTransactionID(id); err != nil {
		return nil, err
	}
	if state.WalletID, err = ids.ParseWalletID(walletID); err != nil {
		return nil, err
	}
	if state.PlayerID, err = ids.ParsePlayerID(playerID); err != nil {
		return nil, err
	}
	if state.Amount, err = toMoney(amount, currency); err != nil {
		return nil, err
	}
	if resultBalance != nil {
		if state.ResultBalance, err = toMoney(*resultBalance, currency); err != nil {
			return nil, err
		}
	}
	if state.ReferenceID, err = optional(ids.ParseTransactionID, referenceID); err != nil {
		return nil, err
	}
	if state.External.ProviderID, err = optional(ids.ParseProviderID, providerID); err != nil {
		return nil, err
	}
	if state.External.ExternalID, err = optional(ids.ParseExternalTransactionID, externalID); err != nil {
		return nil, err
	}
	if state.External.IdempotencyKey, err = optional(ids.ParseIdempotencyKey, key); err != nil {
		return nil, err
	}
	if state.External.RoundID, err = optional(ids.ParseRoundID, roundID); err != nil {
		return nil, err
	}
	if state.External.GameID, err = optional(ids.ParseGameID, gameID); err != nil {
		return nil, err
	}
	if state.External.ReferenceExternalID, err = optional(ids.ParseExternalTransactionID, referenceExternalID); err != nil {
		return nil, err
	}
	return wager.Rehydrate(state)
}

func optional[T any](parse func(string) (T, error), value *string) (T, error) {
	if value == nil {
		var none T
		return none, nil
	}
	return parse(*value)
}

func text(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func utc(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.UTC()
}

func nullText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func nullReference(id ids.TransactionID) any {
	if id.IsZero() {
		return nil
	}
	return id.String()
}

func nullBalance(balance money.Money) any {
	if balance.Currency().Code() == "" {
		return nil
	}
	return balance.MinorUnits()
}
