package worker_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/infra/observability/logtest"
	"jungle-gaming-challeng/internal/worker"
)

func TestPublishDelay(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second}
	for attempts, delay := range want {
		if got := worker.PublishDelay(attempts); got != delay {
			t.Errorf("PublishDelay(%d) = %s, want %s", attempts, got, delay)
		}
	}
}

func TestWorkerRunStopsOnCancel(t *testing.T) {
	logs := &logtest.Buffer{}
	var cycles atomic.Int64
	w := worker.Worker{
		Name: "outbox", Interval: 5 * time.Millisecond, CycleTimeout: time.Second, Batch: 10,
		Logger: slog.New(slog.NewJSONHandler(logs, nil)),
		Cycle: func(context.Context) (int, error) {
			if cycles.Add(1) == 2 {
				return 0, errors.New("broker down")
			}
			return 0, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for cycles.Load() < 4 {
		select {
		case <-deadline:
			t.Fatalf("only %d cycles ran, want the worker to keep running after a failed cycle", cycles.Load())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
	ranAtStop := cycles.Load()
	time.Sleep(30 * time.Millisecond)
	if cycles.Load() != ranAtStop {
		t.Fatal("a cycle ran after Run returned")
	}
	if !strings.Contains(logs.String(), "worker stopped") {
		t.Fatalf("logs = %s, want the stop recorded", logs.String())
	}
}
