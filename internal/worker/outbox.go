package worker

import (
	"context"
	"log/slog"
	"time"

	"jungle-gaming-challeng/internal/app"
)

const (
	retryOutbox       = "outbox"
	publishFailed     = "publish failed"
	firstPublishDelay = time.Second
	maxPublishDelay   = 60 * time.Second
)

type EventPublisher interface {
	Publish(ctx context.Context, record app.OutboxRecord) error
}

type OutboxPublisher struct {
	Outbox    app.OutboxStore
	Publisher EventPublisher
	Metrics   app.Metrics
	Clock     app.Clock
	Logger    *slog.Logger
	Batch     int
	Lease     time.Duration
}

func (p *OutboxPublisher) PublishPending(ctx context.Context) (int, error) {
	if age, err := p.Outbox.OldestPendingAge(ctx); err == nil {
		p.Metrics.OutboxLag(age)
	}

	records, err := p.Outbox.Claim(ctx, p.Batch, p.Lease)
	if err != nil {
		return 0, err
	}
	for _, record := range records {
		if err := p.publish(ctx, record); err != nil {
			return len(records), err
		}
	}
	return len(records), nil
}

func (p *OutboxPublisher) publish(ctx context.Context, record app.OutboxRecord) error {
	if err := p.Publisher.Publish(ctx, record); err != nil {
		p.Metrics.Retry(retryOutbox)
		p.Logger.WarnContext(ctx, "event not published",
			slog.String("eventId", record.EventID.String()), slog.Int("attempts", record.Attempts+1), slog.String("error", err.Error()))
		nextAttemptAt := p.Clock.Now().Add(PublishDelay(record.Attempts))
		return p.Outbox.MarkFailed(ctx, record.EventID, nextAttemptAt, publishFailed)
	}
	return p.Outbox.MarkPublished(ctx, record.EventID)
}

func PublishDelay(attempts int) time.Duration {
	delay := firstPublishDelay
	for i := 0; i < attempts && delay < maxPublishDelay; i++ {
		delay *= 2
	}
	return min(delay, maxPublishDelay)
}
