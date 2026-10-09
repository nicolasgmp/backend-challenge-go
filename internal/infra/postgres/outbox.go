package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/events"
	"jungle-gaming-challeng/internal/domain/ids"
)

type OutboxStore struct {
	pool *pgxpool.Pool
}

var _ app.OutboxStore = (*OutboxStore)(nil)

func NewOutboxStore(pool *pgxpool.Pool) *OutboxStore {
	return &OutboxStore{pool: pool}
}

func (s *OutboxStore) Insert(ctx context.Context, event events.Event) error {
	tx, err := transaction(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	header := event.Header()
	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (event_id, aggregate_id, group_key, event_type, version, payload, correlation_id,
			causation_id, occurred_at, next_attempt_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		header.EventID.String(), header.AggregateID, header.WalletID.String(), header.EventType, header.Version,
		string(payload), header.CorrelationID, nullText(header.CausationID), header.OccurredAt)
	return mapError(err)
}

func (s *OutboxStore) Claim(ctx context.Context, limit int, lease time.Duration) ([]app.OutboxRecord, error) {
	rows, err := s.pool.Query(ctx,
		`UPDATE outbox SET locked_until = now() + $2 * interval '1 millisecond'
		 WHERE event_id IN (
			SELECT event_id FROM outbox
			WHERE published_at IS NULL
			  AND next_attempt_at <= now()
			  AND (locked_until IS NULL OR locked_until <= now())
			ORDER BY next_attempt_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		 )
		 RETURNING event_id, group_key, payload, attempts`, limit, lease.Milliseconds())
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var records []app.OutboxRecord
	for rows.Next() {
		var (
			id     string
			record app.OutboxRecord
		)
		if err := rows.Scan(&id, &record.GroupKey, &record.Payload, &record.Attempts); err != nil {
			return nil, mapError(err)
		}
		if record.EventID, err = ids.ParseEventID(id); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, mapError(rows.Err())
}

func (s *OutboxStore) MarkPublished(ctx context.Context, eventID ids.EventID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE outbox SET published_at = now() WHERE event_id = $1`, eventID.String())
	return mapError(err)
}

func (s *OutboxStore) MarkFailed(ctx context.Context, eventID ids.EventID, nextAttemptAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE outbox SET attempts = attempts + 1, next_attempt_at = $2, locked_until = NULL
		 WHERE event_id = $1`, eventID.String(), nextAttemptAt)
	return mapError(err)
}

func (s *OutboxStore) OldestPendingAge(ctx context.Context) (time.Duration, error) {
	var oldest *time.Time
	err := s.pool.QueryRow(ctx, `SELECT min(occurred_at) FROM outbox WHERE published_at IS NULL`).Scan(&oldest)
	if err != nil {
		return 0, mapError(err)
	}
	if oldest == nil {
		return 0, nil
	}
	return time.Since(*oldest), nil
}
