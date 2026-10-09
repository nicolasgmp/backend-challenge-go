package observability

import (
	"context"
	"io"
	"log/slog"
)

const (
	KeyCorrelationID = "correlationId"
	KeyMessageID     = "messageId"
)

type attrsKey struct{}

func NewLogger(out io.Writer, level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level, ReplaceAttr: timeInUTC})
	return slog.New(contextHandler{handler})
}

func WithLogAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	return context.WithValue(ctx, attrsKey{}, append(logAttrs(ctx), attrs...))
}

func WithCorrelationID(ctx context.Context, id string) context.Context {
	return WithLogAttrs(ctx, slog.String(KeyCorrelationID, id))
}

func WithMessageID(ctx context.Context, id string) context.Context {
	return WithLogAttrs(ctx, slog.String(KeyMessageID, id))
}

func logAttrs(ctx context.Context) []slog.Attr {
	attrs, _ := ctx.Value(attrsKey{}).([]slog.Attr)
	return attrs[:len(attrs):len(attrs)]
}

func timeInUTC(groups []string, attr slog.Attr) slog.Attr {
	if len(groups) == 0 && attr.Key == slog.TimeKey {
		attr.Value = slog.TimeValue(attr.Value.Time().UTC())
	}
	return attr
}

type contextHandler struct {
	slog.Handler
}

func (h contextHandler) Handle(ctx context.Context, record slog.Record) error {
	record.AddAttrs(logAttrs(ctx)...)
	return h.Handler.Handle(ctx, record)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}
