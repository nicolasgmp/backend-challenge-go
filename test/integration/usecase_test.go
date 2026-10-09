//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/app/apptest"
	"jungle-gaming-challeng/internal/domain/events"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
	"jungle-gaming-challeng/internal/domain/wallet"
	"jungle-gaming-challeng/internal/infra/postgres"
	"jungle-gaming-challeng/internal/infra/postgres/pgtest"
)

var errBoom = errors.New("boom")

func TestMain(m *testing.M) {
	pgtest.Main(m, "../../migrations")
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

type environment struct {
	pool    *pgxpool.Pool
	service *app.Service
}

func newEnvironment(t *testing.T) environment {
	t.Helper()

	pool := pgtest.NewDatabase(context.Background(), t)
	return environment{pool: pool, service: newService(pool)}
}

func newService(pool *pgxpool.Pool) *app.Service {
	return &app.Service{
		Tx:           postgres.NewTxRunner(pool, 5*time.Second),
		Wallets:      postgres.NewWalletRepository(pool),
		Transactions: postgres.NewTransactionRepository(pool),
		Ledger:       postgres.NewLedgerRepository(pool),
		Outbox:       postgres.NewOutboxStore(pool),
		Clock:        systemClock{},
		Metrics:      &apptest.Metrics{},
		Logger:       slog.New(slog.DiscardHandler),
		ReferenceTTL: 5 * time.Minute,
	}
}

func parsed[T any](t *testing.T, parse func(string) (T, error), text string) T {
	t.Helper()

	value, err := parse(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return value
}

func amount(t *testing.T, text string) money.Money {
	t.Helper()

	m, err := money.Parse(text, parsed(t, money.ParseCurrency, "BRL"))
	if err != nil {
		t.Fatalf("Parse(%q): %v", text, err)
	}
	return m
}

func (e environment) open(t *testing.T, balance string) wallet.State {
	t.Helper()

	player, err := ids.NewWalletID()
	if err != nil {
		t.Fatalf("new player id: %v", err)
	}
	state, err := e.service.OpenWallet(context.Background(), app.OpenWalletInput{
		PlayerID:       parsed(t, ids.ParsePlayerID, player.String()),
		InitialBalance: amount(t, balance),
		CorrelationID:  "correlation-open",
	})
	if err != nil {
		t.Fatalf("OpenWallet: %v", err)
	}
	return state
}

func operation(t *testing.T, w wallet.State, kind wager.Kind, value, externalID, reference string) app.SubmitInput {
	t.Helper()

	in := app.SubmitInput{
		Channel:  app.ChannelHTTP,
		Kind:     kind,
		WalletID: w.ID,
		PlayerID: w.PlayerID,
		Money:    amount(t, value),
		External: wager.External{
			ProviderID:     parsed(t, ids.ParseProviderID, "provider-a"),
			ExternalID:     parsed(t, ids.ParseExternalTransactionID, externalID),
			IdempotencyKey: parsed(t, ids.ParseIdempotencyKey, "key-"+externalID),
			RoundID:        parsed(t, ids.ParseRoundID, "round-1"),
			GameID:         parsed(t, ids.ParseGameID, "game-1"),
		},
		CorrelationID: "correlation-" + externalID,
	}
	if reference != "" {
		in.External.ReferenceExternalID = parsed(t, ids.ParseExternalTransactionID, reference)
	}
	return in
}

func (e environment) submit(t *testing.T, in app.SubmitInput) wager.State {
	t.Helper()

	result, err := e.service.SubmitTransaction(context.Background(), in)
	if err != nil {
		t.Fatalf("SubmitTransaction(%s %s): %v", in.Kind, in.External.ExternalID, err)
	}
	return result.Transaction
}

func (e environment) inParallel(inputs []app.SubmitInput) ([]app.SubmitResult, []error) {
	results := make([]app.SubmitResult, len(inputs))
	failures := make([]error, len(inputs))
	var group sync.WaitGroup
	for i, in := range inputs {
		group.Go(func() {
			results[i], failures[i] = e.service.SubmitTransaction(context.Background(), in)
		})
	}
	group.Wait()
	return results, failures
}

type counts struct {
	wallets, transactions, entries, events int
}

func (e environment) counts(t *testing.T) counts {
	t.Helper()

	var c counts
	err := e.pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM wallets),
		(SELECT count(*) FROM wager_transactions),
		(SELECT count(*) FROM wallet_ledger_entries),
		(SELECT count(*) FROM outbox)`).Scan(&c.wallets, &c.transactions, &c.entries, &c.events)
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return c
}

func (e environment) wantWallet(t *testing.T, id ids.WalletID, balance string, version int64) {
	t.Helper()

	state, err := e.service.GetWallet(context.Background(), id)
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if state.Balance.Amount() != balance || state.Version != version {
		t.Fatalf("wallet = %s at version %d, want %s at version %d", state.Balance.Amount(), state.Version, balance, version)
	}

	reconciliation, err := e.service.ReconcileWallet(context.Background(), id)
	if err != nil {
		t.Fatalf("ReconcileWallet: %v", err)
	}
	if !reconciliation.Consistent {
		t.Fatalf("the stored balance %s diverges from the ledger total %s",
			reconciliation.StoredBalance.Amount(), reconciliation.CalculatedBalance.Amount())
	}
}

type failingWallets struct{ app.WalletRepository }

func (failingWallets) UpdateBalance(context.Context, *wallet.Wallet) error { return errBoom }

type failingTransactions struct{ app.TransactionRepository }

func (failingTransactions) Insert(context.Context, *wager.Transaction) error { return errBoom }

type failingLedger struct{ app.LedgerRepository }

func (failingLedger) Insert(context.Context, ledger.Entry) error { return errBoom }

type failingOutbox struct {
	app.OutboxStore
	failAt int
	calls  int
}

func (o *failingOutbox) Insert(ctx context.Context, event events.Event) error {
	o.calls++
	if o.calls == o.failAt {
		return errBoom
	}
	return o.OutboxStore.Insert(ctx, event)
}

func TestFailedWriteLeavesNothingBehind(t *testing.T) {
	breaks := map[string]func(*app.Service){
		"transaction":        func(s *app.Service) { s.Transactions = failingTransactions{s.Transactions} },
		"wallet balance":     func(s *app.Service) { s.Wallets = failingWallets{s.Wallets} },
		"ledger entry":       func(s *app.Service) { s.Ledger = failingLedger{s.Ledger} },
		"first outbox event": func(s *app.Service) { s.Outbox = &failingOutbox{OutboxStore: s.Outbox, failAt: 1} },
		"last outbox event":  func(s *app.Service) { s.Outbox = &failingOutbox{OutboxStore: s.Outbox, failAt: 2} },
	}

	for name, breakIt := range breaks {
		t.Run("bet/"+name, func(t *testing.T) {
			e := newEnvironment(t)
			w := e.open(t, "1000.00")
			before := e.counts(t)
			broken := newService(e.pool)
			breakIt(broken)

			_, err := broken.SubmitTransaction(context.Background(), operation(t, w, wager.Bet, "25.00", "tx-1", ""))
			if !errors.Is(err, errBoom) {
				t.Fatalf("err = %v, want %v", err, errBoom)
			}
			if after := e.counts(t); after != before {
				t.Fatalf("rows = %+v, want them unchanged: %+v", after, before)
			}
			e.wantWallet(t, w.ID, "1000.00", 1)

			if resent := e.submit(t, operation(t, w, wager.Bet, "25.00", "tx-1", "")); resent.Status != wager.Processed {
				t.Fatalf("resend after the failure = %s, want PROCESSED", resent.Status)
			}
			e.wantWallet(t, w.ID, "975.00", 2)
		})
	}

	for _, name := range []string{"transaction", "ledger entry", "first outbox event", "last outbox event"} {
		t.Run("opening/"+name, func(t *testing.T) {
			e := newEnvironment(t)
			broken := newService(e.pool)
			breaks[name](broken)

			_, err := broken.OpenWallet(context.Background(), app.OpenWalletInput{
				PlayerID:       parsed(t, ids.ParsePlayerID, "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1"),
				InitialBalance: amount(t, "1000.00"),
				CorrelationID:  "correlation-open",
			})
			if !errors.Is(err, errBoom) {
				t.Fatalf("err = %v, want %v", err, errBoom)
			}
			if after := e.counts(t); after != (counts{}) {
				t.Fatalf("rows = %+v, want none", after)
			}
		})
	}
}

func TestTwoBetsOfEightyOverOneHundred(t *testing.T) {
	e := newEnvironment(t)
	w := e.open(t, "100.00")
	inputs := []app.SubmitInput{
		operation(t, w, wager.Bet, "80.00", "tx-1", ""),
		operation(t, w, wager.Bet, "80.00", "tx-2", ""),
	}

	results, failures := e.inParallel(inputs)
	statuses := map[wager.Status]int{}
	for i, result := range results {
		if failures[i] != nil {
			t.Fatalf("bet %d: %v", i+1, failures[i])
		}
		statuses[result.Transaction.Status]++
		if result.Transaction.Status == wager.Rejected && result.Transaction.FailureCode != failure.InsufficientFunds {
			t.Fatalf("rejection code = %s, want INSUFFICIENT_FUNDS", result.Transaction.FailureCode)
		}
	}
	if statuses[wager.Processed] != 1 || statuses[wager.Rejected] != 1 {
		t.Fatalf("statuses = %v, want one PROCESSED and one REJECTED", statuses)
	}
	e.wantWallet(t, w.ID, "20.00", 2)
	before := e.counts(t)
	if before.entries != 2 {
		t.Fatalf("entries = %d, want the opening and a single debit", before.entries)
	}

	replays, failures := e.inParallel(inputs)
	for i, replay := range replays {
		if failures[i] != nil {
			t.Fatalf("resend %d: %v", i+1, failures[i])
		}
		if !replay.IdempotentReplay || replay.Transaction != results[i].Transaction {
			t.Fatalf("resend %d = %+v, want the original result as a replay", i+1, replay)
		}
	}
	if after := e.counts(t); after != before {
		t.Fatalf("rows = %+v, want them unchanged by the resends: %+v", after, before)
	}
	e.wantWallet(t, w.ID, "20.00", 2)
}

func TestSameBetFiftyTimesInParallel(t *testing.T) {
	e := newEnvironment(t)
	w := e.open(t, "1000.00")
	inputs := make([]app.SubmitInput, 50)
	for i := range inputs {
		inputs[i] = operation(t, w, wager.Bet, "25.00", "tx-1", "")
	}

	results, failures := e.inParallel(inputs)
	fresh := 0
	for i, result := range results {
		if failures[i] != nil {
			t.Fatalf("send %d: %v", i+1, failures[i])
		}
		if result.Transaction.ID != results[0].Transaction.ID || result.Transaction.ResultBalance.Amount() != "975.00" {
			t.Fatalf("send %d = %+v, want the same transaction with 975.00", i+1, result.Transaction)
		}
		if !result.IdempotentReplay {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("%d sends were applied, want exactly 1 and 49 replays", fresh)
	}
	if got := e.counts(t); got.transactions != 2 || got.entries != 2 {
		t.Fatalf("rows = %+v, want the opening and a single bet with a single debit", got)
	}
	e.wantWallet(t, w.ID, "975.00", 2)
}

func TestTwentyParallelCredits(t *testing.T) {
	e := newEnvironment(t)
	w := e.open(t, "0.00")
	inputs := make([]app.SubmitInput, 20)
	for i := range inputs {
		inputs[i] = operation(t, w, wager.Win, "10.00", fmt.Sprintf("tx-%d", i), "")
	}

	results, failures := e.inParallel(inputs)
	for i, result := range results {
		if failures[i] != nil || result.Transaction.Status != wager.Processed {
			t.Fatalf("credit %d = %s, %v, want PROCESSED", i+1, result.Transaction.Status, failures[i])
		}
	}
	if got := e.counts(t); got.entries != 20 {
		t.Fatalf("entries = %d, want 20", got.entries)
	}
	e.wantWallet(t, w.ID, "200.00", 21)
}

func TestWalletsAdvanceInParallel(t *testing.T) {
	e := newEnvironment(t)
	held := e.open(t, "1000.00")
	free := e.open(t, "1000.00")
	runner := postgres.NewTxRunner(e.pool, 5*time.Second)
	wallets := postgres.NewWalletRepository(e.pool)

	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- runner.Run(context.Background(), func(ctx context.Context) error {
			if _, err := wallets.GetForUpdate(ctx, held.ID); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	if bet := e.submit(t, operation(t, free, wager.Bet, "25.00", "tx-free", "")); bet.Status != wager.Processed {
		t.Fatalf("bet on the free wallet = %s, want PROCESSED while the other wallet is locked", bet.Status)
	}

	blocked := make(chan wager.Status, 1)
	go func() {
		result, _ := e.service.SubmitTransaction(context.Background(), operation(t, held, wager.Bet, "25.00", "tx-held", ""))
		blocked <- result.Transaction.Status
	}()
	select {
	case status := <-blocked:
		t.Fatalf("bet on the locked wallet finished with %q before the lock was released", status)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("lock holder: %v", err)
	}
	if status := <-blocked; status != wager.Processed {
		t.Fatalf("bet on the locked wallet = %s, want PROCESSED after the release", status)
	}
	e.wantWallet(t, free.ID, "975.00", 2)
	e.wantWallet(t, held.ID, "975.00", 2)
}

func TestRefundAndRollbackRaceForTheSameBet(t *testing.T) {
	e := newEnvironment(t)
	w := e.open(t, "1000.00")
	e.submit(t, operation(t, w, wager.Bet, "25.00", "tx-bet", ""))

	results, failures := e.inParallel([]app.SubmitInput{
		operation(t, w, wager.Refund, "25.00", "tx-refund", "tx-bet"),
		operation(t, w, wager.Rollback, "25.00", "tx-rollback", "tx-bet"),
	})
	statuses := map[wager.Status]int{}
	for i, result := range results {
		if failures[i] != nil {
			t.Fatalf("reversal %d: %v", i+1, failures[i])
		}
		statuses[result.Transaction.Status]++
		if result.Transaction.Status == wager.Rejected && result.Transaction.FailureCode != failure.ReferenceAlreadyReversed {
			t.Fatalf("rejection code = %s, want REFERENCE_ALREADY_REVERSED", result.Transaction.FailureCode)
		}
	}
	if statuses[wager.Processed] != 1 || statuses[wager.Rejected] != 1 {
		t.Fatalf("statuses = %v, want exactly one PROCESSED reversal", statuses)
	}
	e.wantWallet(t, w.ID, "1000.00", 3)
}

func TestReversalBeforeItsReferenceIsResolvedLater(t *testing.T) {
	e := newEnvironment(t)
	w := e.open(t, "1000.00")

	refund := e.submit(t, operation(t, w, wager.Refund, "25.00", "tx-refund", "tx-bet"))
	if refund.Status != wager.PendingReference {
		t.Fatalf("refund = %s, want PENDING_REFERENCE", refund.Status)
	}
	e.submit(t, operation(t, w, wager.Bet, "25.00", "tx-bet", ""))
	e.wantWallet(t, w.ID, "975.00", 2)

	if _, err := e.pool.Exec(context.Background(),
		`UPDATE wager_transactions SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, refund.ID.String()); err != nil {
		t.Fatalf("make the pending reference due: %v", err)
	}
	restarted := newService(e.pool)
	if resolved, err := restarted.ResolveDuePendingReferences(context.Background(), 10); err != nil || resolved != 1 {
		t.Fatalf("ResolveDuePendingReferences = %d, %v, want 1", resolved, err)
	}

	stored, err := e.service.GetTransaction(context.Background(), app.Caller{}, refund.ID)
	if err != nil || stored.Status != wager.Processed {
		t.Fatalf("refund = %s, %v, want PROCESSED by another service instance", stored.Status, err)
	}
	e.wantWallet(t, w.ID, "1000.00", 3)
}

func TestReconciliationSeesOneSnapshotUnderLoad(t *testing.T) {
	e := newEnvironment(t)
	w := e.open(t, "0.00")

	done := make(chan struct{})
	var writer sync.WaitGroup
	writer.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
				_, _ = e.service.SubmitTransaction(context.Background(), operation(t, w, wager.Win, "1.00", fmt.Sprintf("tx-%d", i), ""))
			}
		}
	})

	for range 200 {
		result, err := e.service.ReconcileWallet(context.Background(), w.ID)
		if err != nil {
			t.Errorf("ReconcileWallet: %v", err)
			break
		}
		if !result.Consistent {
			t.Errorf("false divergence: stored %s, calculated %s", result.StoredBalance.Amount(), result.CalculatedBalance.Amount())
			break
		}
	}
	close(done)
	writer.Wait()
}
