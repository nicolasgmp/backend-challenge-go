package worker

import (
	"context"
	"log/slog"
	"time"
)

type Cycle func(ctx context.Context) (handled int, err error)

type Worker struct {
	Name         string
	Interval     time.Duration
	CycleTimeout time.Duration
	Batch        int
	Cycle        Cycle
	Logger       *slog.Logger
}

func (w Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if w.runCycle(ctx) {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(w.Interval):
		}
	}
	w.Logger.Info("worker stopped", slog.String("worker", w.Name))
}

func (w Worker) runCycle(ctx context.Context) bool {
	cycle, cancel := context.WithTimeout(ctx, w.CycleTimeout)
	defer cancel()

	handled, err := w.Cycle(cycle)
	if err != nil && ctx.Err() == nil {
		w.Logger.Warn("worker cycle failed", slog.String("worker", w.Name), slog.String("error", err.Error()))
		return false
	}
	return handled >= w.Batch
}
