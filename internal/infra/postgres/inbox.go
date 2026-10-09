package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"jungle-gaming-challeng/internal/app"
)

type InboxStore struct {
	pool *pgxpool.Pool
}

var _ app.InboxStore = (*InboxStore)(nil)

func NewInboxStore(pool *pgxpool.Pool) *InboxStore {
	return &InboxStore{pool: pool}
}

func (s *InboxStore) Register(ctx context.Context, consumer, messageID, payloadHash string) (app.InboxStatus, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return "", err
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO inbox (consumer_name, message_id, payload_hash, received_at)
		 VALUES ($1, $2, $3, now())
		 ON CONFLICT (consumer_name, message_id) DO NOTHING`, consumer, messageID, payloadHash)
	if err != nil {
		return "", mapError(err)
	}
	if tag.RowsAffected() == 1 {
		return app.InboxNew, nil
	}

	var stored string
	err = tx.QueryRow(ctx,
		`SELECT payload_hash FROM inbox WHERE consumer_name = $1 AND message_id = $2`, consumer, messageID).Scan(&stored)
	if err != nil {
		return "", mapError(err)
	}
	if stored != payloadHash {
		return app.InboxHashMismatch, nil
	}
	return app.InboxDuplicate, nil
}

func (s *InboxStore) Complete(ctx context.Context, consumer, messageID string) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx,
		`UPDATE inbox SET completed_at = now() WHERE consumer_name = $1 AND message_id = $2`, consumer, messageID)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return app.ErrNotFound
	}
	return nil
}
