package observability

import (
	"context"
	"sync/atomic"
)

const ShuttingDown = "shutting_down"

type Check struct {
	Name string
	Ping func(ctx context.Context) error
}

type Health struct {
	checks       []Check
	shuttingDown atomic.Bool
}

func NewHealth(checks ...Check) *Health {
	return &Health{checks: checks}
}

func (h *Health) BeginShutdown() {
	h.shuttingDown.Store(true)
}

func (h *Health) NotReady(ctx context.Context) []string {
	if h.shuttingDown.Load() {
		return []string{ShuttingDown}
	}
	var failed []string
	for _, check := range h.checks {
		if err := check.Ping(ctx); err != nil {
			failed = append(failed, check.Name)
		}
	}
	return failed
}
