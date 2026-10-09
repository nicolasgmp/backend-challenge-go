package sqs

import (
	"context"
	"errors"
	"log/slog"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/infra/observability"
)

const (
	ConsumerName  = "wager-transactions-consumer"
	retryConsumer = "consumer"
)

type Action string

const (
	Delete     Action = "DELETE"
	DeadLetter Action = "DEAD_LETTER"
	Retry      Action = "RETRY"
)

type Outcome struct {
	Action Action
	Reason string
}

type Submitter interface {
	SubmitTransaction(ctx context.Context, in app.SubmitInput) (app.SubmitResult, error)
}

type Handler struct {
	Tx      app.TxRunner
	Inbox   app.InboxStore
	Service Submitter
	Metrics app.Metrics
	Logger  *slog.Logger
}

var errMessageIDReused = errors.New("sqs: message id reused with another body")

func (h *Handler) Handle(ctx context.Context, body string) Outcome {
	parsed, reason := parseEnvelope(body)
	if reason != "" {
		return h.deadLetter(ctx, reason)
	}
	ctx = observability.WithMessageID(observability.WithCorrelationID(ctx, parsed.MessageID), parsed.MessageID)

	in, err := h.input(parsed)
	if err != nil {
		return h.outcome(ctx, err)
	}

	duplicate := false
	err = h.Tx.Run(ctx, func(ctx context.Context) error {
		status, err := h.Inbox.Register(ctx, ConsumerName, parsed.MessageID, bodyHash(body))
		if err != nil {
			return err
		}
		switch status {
		case app.InboxDuplicate:
			duplicate = true
			return nil
		case app.InboxHashMismatch:
			return errMessageIDReused
		}
		if _, err := h.Service.SubmitTransaction(ctx, in); err != nil {
			return err
		}
		return h.Inbox.Complete(ctx, ConsumerName, parsed.MessageID)
	})
	if err == nil && duplicate {
		h.Metrics.Duplicate(app.DuplicateInbox)
		h.Logger.InfoContext(ctx, "message already handled")
	}
	return h.outcome(ctx, err)
}

func (h *Handler) input(parsed envelope) (app.SubmitInput, error) {
	raw, err := parsed.operation()
	if err != nil {
		return app.SubmitInput{}, failure.InvalidInputError{Code: failure.MalformedRequest}
	}
	in, err := raw.Input()
	in.Channel = app.ChannelSQS
	in.CorrelationID = parsed.MessageID
	in.CausationID = parsed.MessageID
	return in, err
}

func (h *Handler) outcome(ctx context.Context, err error) Outcome {
	var invalid failure.InvalidInputError
	var conflict app.ConflictError
	switch {
	case err == nil:
		return Outcome{Action: Delete}
	case errors.Is(err, errMessageIDReused):
		return h.deadLetter(ctx, ReasonMessageIDReused)
	case errors.As(err, &invalid):
		return h.deadLetter(ctx, string(invalid.Code))
	case errors.As(err, &conflict):
		return h.deadLetter(ctx, conflict.Code)
	default:
		h.Metrics.Retry(retryConsumer)
		h.Logger.WarnContext(ctx, "message will be retried", slog.String("error", err.Error()))
		return Outcome{Action: Retry}
	}
}

func (h *Handler) deadLetter(ctx context.Context, reason string) Outcome {
	h.Logger.WarnContext(ctx, "message sent to the dead-letter queue", slog.String("reason", reason))
	return Outcome{Action: DeadLetter, Reason: reason}
}
