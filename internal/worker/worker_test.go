package worker_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/app/apptest"
	"jungle-gaming-challeng/internal/domain/events"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/infra/observability/logtest"
	"jungle-gaming-challeng/internal/worker"
)

var errBroker = errors.New("broker down")

type fakePublisher struct {
	store     *apptest.Store
	published []ids.EventID
	failures  int
	callsSeen [][]string
}

func (p *fakePublisher) Publish(_ context.Context, record app.OutboxRecord) error {
	p.callsSeen = append(p.callsSeen, slices.Clone(p.store.Calls))
	if p.failures > 0 {
		p.failures--
		return errBroker
	}
	p.published = append(p.published, record.EventID)
	return nil
}

type fixture struct {
	publisher *worker.OutboxPublisher
	broker    *fakePublisher
	store     *apptest.Store
	metrics   *apptest.Metrics
	clock     *apptest.Clock
	logs      *logtest.Buffer
}

func newFixture(batch int) fixture {
	store := apptest.NewStore()
	broker := &fakePublisher{store: store}
	metrics := &apptest.Metrics{}
	clock := &apptest.Clock{Current: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	logs := &logtest.Buffer{}
	return fixture{
		publisher: &worker.OutboxPublisher{
			Outbox: store.Outbox(), Publisher: broker, Metrics: metrics, Clock: clock,
			Logger: slog.New(slog.NewJSONHandler(logs, nil)), Batch: batch, Lease: 30 * time.Second,
		},
		broker: broker, store: store, metrics: metrics, clock: clock, logs: logs,
	}
}

func (f fixture) addEvent(t *testing.T, version int64) ids.EventID {
	t.Helper()

	currency, err := money.ParseCurrency("BRL")
	if err != nil {
		t.Fatalf("ParseCurrency: %v", err)
	}
	value := func(units int64) money.Money {
		m, err := money.FromMinorUnits(units, currency)
		if err != nil {
			t.Fatalf("FromMinorUnits: %v", err)
		}
		return m
	}
	walletID, err := ids.NewWalletID()
	if err != nil {
		t.Fatalf("NewWalletID: %v", err)
	}
	transactionID, err := ids.NewTransactionID()
	if err != nil {
		t.Fatalf("NewTransactionID: %v", err)
	}
	event, err := events.NewWalletBalanceChanged(events.Origin{CorrelationID: "correlation-1"}, events.WalletBalanceChanged{
		WalletID: walletID, TransactionID: transactionID, Direction: ledger.Credit,
		Money: value(2500), BalanceBefore: value(0), BalanceAfter: value(2500), WalletVersion: version,
	})
	if err != nil {
		t.Fatalf("NewWalletBalanceChanged: %v", err)
	}
	if err := f.store.Outbox().Insert(context.Background(), event); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	return event.Header().EventID
}

func TestPublishPendingPublishesAfterTheClaim(t *testing.T) {
	f := newFixture(10)
	first, second := f.addEvent(t, 1), f.addEvent(t, 2)
	f.store.Calls = nil

	published, err := f.publisher.PublishPending(context.Background())
	if err != nil || published != 2 {
		t.Fatalf("PublishPending = %d, %v, want 2", published, err)
	}
	if !slices.Equal(f.broker.published, []ids.EventID{first, second}) {
		t.Fatalf("published = %v, want the two events", f.broker.published)
	}
	if seen := f.broker.callsSeen[0]; !slices.Equal(seen, []string{"outbox.OldestPendingAge", "outbox.Claim"}) {
		t.Fatalf("store calls before the first publication = %v, want only the claim: nothing is published inside a transaction", seen)
	}
	want := []string{"outbox.OldestPendingAge", "outbox.Claim", "outbox.MarkPublished", "outbox.MarkPublished"}
	if !slices.Equal(f.store.Calls, want) {
		t.Fatalf("calls = %v, want %v", f.store.Calls, want)
	}

	again, err := f.publisher.PublishPending(context.Background())
	if err != nil || again != 0 || len(f.broker.published) != 2 {
		t.Fatalf("second cycle = %d, %v, want nothing left to publish", again, err)
	}
}

func TestPublishPendingKeepsFailedEvents(t *testing.T) {
	f := newFixture(10)
	eventID := f.addEvent(t, 1)
	f.broker.failures = 50

	for attempt := 1; attempt <= 50; attempt++ {
		if _, err := f.publisher.PublishPending(context.Background()); err != nil {
			t.Fatalf("cycle %d: %v", attempt, err)
		}
	}
	if len(f.broker.published) != 0 || !slices.Contains(f.metrics.Calls, "Retry outbox") {
		t.Fatalf("published = %v, metrics = %v, want nothing published and the retries counted", f.broker.published, f.metrics.Calls)
	}
	logtest.AssertNoLeak(t, f.logs.String(), "25.00", "walletVersion")

	if _, err := f.publisher.PublishPending(context.Background()); err != nil {
		t.Fatalf("cycle after the broker came back: %v", err)
	}
	if !slices.Equal(f.broker.published, []ids.EventID{eventID}) {
		t.Fatalf("published = %v, want the event delivered after 50 failures, with the same id", f.broker.published)
	}
}

func TestPublishDelay(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second}
	for attempts, delay := range want {
		if got := worker.PublishDelay(attempts); got != delay {
			t.Errorf("PublishDelay(%d) = %s, want %s", attempts, got, delay)
		}
	}
	if got := worker.PublishDelay(1000); got != 60*time.Second {
		t.Errorf("PublishDelay(1000) = %s, want the 60s cap", got)
	}
}

func TestPublishPendingReportsTheOutboxLag(t *testing.T) {
	f := newFixture(10)

	if _, err := f.publisher.PublishPending(context.Background()); err != nil {
		t.Fatalf("PublishPending: %v", err)
	}
	f.addEvent(t, 1)
	if _, err := f.publisher.PublishPending(context.Background()); err != nil {
		t.Fatalf("PublishPending: %v", err)
	}

	lag := slices.DeleteFunc(slices.Clone(f.metrics.Calls), func(call string) bool { return call != "OutboxLag" })
	if len(lag) != 2 {
		t.Fatalf("metrics = %v, want the lag reported in each cycle, with and without pending events", f.metrics.Calls)
	}
}

func TestWorkerRunStopsOnCancel(t *testing.T) {
	logs := &logtest.Buffer{}
	var cycles atomic.Int64
	w := worker.Worker{
		Name: "outbox", Interval: 5 * time.Millisecond, CycleTimeout: time.Second, Batch: 10,
		Logger: slog.New(slog.NewJSONHandler(logs, nil)),
		Cycle: func(ctx context.Context) (int, error) {
			if cycles.Add(1) == 2 {
				return 0, errBroker
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
	if !strings.Contains(logs.String(), "worker stopped") || !strings.Contains(logs.String(), "worker cycle failed") {
		t.Fatalf("logs = %s, want the failed cycle and the stop recorded", logs.String())
	}
}

func TestWorkerRunsAgainAtOnceWhenTheBatchIsFull(t *testing.T) {
	var cycles atomic.Int64
	w := worker.Worker{
		Name: "outbox", Interval: time.Hour, CycleTimeout: time.Second, Batch: 10,
		Logger: slog.New(slog.DiscardHandler),
		Cycle: func(context.Context) (int, error) {
			if cycles.Add(1) < 3 {
				return 10, nil
			}
			return 2, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for cycles.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("%d cycles ran, want three without waiting for the interval", cycles.Load())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done
	if cycles.Load() != 3 {
		t.Fatalf("%d cycles ran, want the worker to wait for the interval after a partial batch", cycles.Load())
	}
}
