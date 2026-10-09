//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"jungle-gaming-challeng/internal/app"
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

type database struct {
	pool         *pgxpool.Pool
	runner       *postgres.TxRunner
	wallets      *postgres.WalletRepository
	transactions *postgres.TransactionRepository
	ledger       *postgres.LedgerRepository
	outbox       *postgres.OutboxStore
	inbox        *postgres.InboxStore
}

func newDatabase(t *testing.T) database {
	t.Helper()

	pool := pgtest.NewDatabase(context.Background(), t)
	return database{
		pool:         pool,
		runner:       newRunner(pool),
		wallets:      postgres.NewWalletRepository(pool),
		transactions: postgres.NewTransactionRepository(pool),
		ledger:       postgres.NewLedgerRepository(pool),
		outbox:       postgres.NewOutboxStore(pool),
		inbox:        postgres.NewInboxStore(pool),
	}
}

func (d database) write(t *testing.T, fn func(ctx context.Context) error) {
	t.Helper()

	if err := d.runner.Run(context.Background(), fn); err != nil {
		t.Fatalf("write: %v", err)
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

func amount(t *testing.T, units int64, code string) money.Money {
	t.Helper()

	m, err := money.FromMinorUnits(units, parsed(t, money.ParseCurrency, code))
	if err != nil {
		t.Fatalf("FromMinorUnits(%d, %s): %v", units, code, err)
	}
	return m
}

func (d database) openWallet(t *testing.T, units int64) *wallet.Wallet {
	t.Helper()

	playerID, err := ids.NewWalletID()
	if err != nil {
		t.Fatalf("NewWalletID: %v", err)
	}
	w, _, err := wallet.Open(parsed(t, ids.ParsePlayerID, playerID.String()), amount(t, units, "BRL"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	d.write(t, func(ctx context.Context) error { return d.wallets.Insert(ctx, w) })
	return w
}

func external(t *testing.T, w *wallet.Wallet, kind wager.Kind, provider, externalID, reference string) *wager.Transaction {
	t.Helper()

	metadata := wager.External{
		ProviderID:     parsed(t, ids.ParseProviderID, provider),
		ExternalID:     parsed(t, ids.ParseExternalTransactionID, externalID),
		IdempotencyKey: parsed(t, ids.ParseIdempotencyKey, "key-"+externalID),
		PayloadHash:    "hash-" + externalID,
		RoundID:        parsed(t, ids.ParseRoundID, "round-1"),
		GameID:         parsed(t, ids.ParseGameID, "game-1"),
	}
	if reference != "" {
		metadata.ReferenceExternalID = parsed(t, ids.ParseExternalTransactionID, reference)
	}
	tx, err := wager.NewExternal(kind, w.State().ID, w.State().PlayerID, amount(t, 2500, "BRL"), metadata, "correlation-1")
	if err != nil {
		t.Fatalf("NewExternal: %v", err)
	}
	return tx
}

func (d database) processedBet(t *testing.T, w *wallet.Wallet, externalID string) *wager.Transaction {
	t.Helper()

	bet := external(t, w, wager.Bet, "provider-a", externalID, "")
	if err := bet.MarkProcessed(amount(t, 97500, "BRL"), ids.TransactionID{}); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	d.write(t, func(ctx context.Context) error { return d.transactions.Insert(ctx, bet) })
	return bet
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

func TestWalletRepository(t *testing.T) {
	ctx := context.Background()
	db := newDatabase(t)
	w := db.openWallet(t, 100000)

	stored, err := db.wallets.Get(ctx, w.State().ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.State() != w.State() {
		t.Fatalf("Get = %+v, want %+v", stored.State(), w.State())
	}

	duplicate, _, err := wallet.Open(w.State().PlayerID, amount(t, 0, "BRL"))
	must(t, err)
	err = db.runner.Run(ctx, func(ctx context.Context) error { return db.wallets.Insert(ctx, duplicate) })
	if !errors.Is(err, app.ErrUniqueViolation) {
		t.Fatalf("same player and currency: err = %v, want %v", err, app.ErrUniqueViolation)
	}

	if _, err := db.wallets.Get(ctx, parsed(t, ids.ParseWalletID, walletB)); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("unknown wallet: err = %v, want %v", err, app.ErrNotFound)
	}
	if _, err := db.wallets.GetForUpdate(ctx, w.State().ID); !errors.Is(err, postgres.ErrNoTransaction) {
		t.Fatalf("GetForUpdate outside a transaction: err = %v, want %v", err, postgres.ErrNoTransaction)
	}
}

func TestWalletUpdateBalance(t *testing.T) {
	ctx := context.Background()
	db := newDatabase(t)
	w := db.openWallet(t, 100000)

	db.write(t, func(ctx context.Context) error {
		locked, err := db.wallets.GetForUpdate(ctx, w.State().ID)
		if err != nil {
			return err
		}
		if _, err := locked.Debit(amount(t, 2500, "BRL")); err != nil {
			return err
		}
		w = locked
		return db.wallets.UpdateBalance(ctx, locked)
	})

	stored, err := db.wallets.Get(ctx, w.State().ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.State() != w.State() || stored.State().Version != 2 || stored.State().Balance.Amount() != "975.00" {
		t.Fatalf("Get = %+v, want 975.00 at version 2", stored.State())
	}

	missing, _, err := wallet.Open(w.State().PlayerID, amount(t, 0, "USD"))
	must(t, err)
	err = db.runner.Run(ctx, func(ctx context.Context) error { return db.wallets.UpdateBalance(ctx, missing) })
	if !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("update of a missing wallet: err = %v, want %v", err, app.ErrNotFound)
	}
}

func TestTransactionRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := newDatabase(t)
	w := db.openWallet(t, 100000)
	bet := db.processedBet(t, w, "tx-bet")
	deadline := time.Date(2026, 1, 1, 12, 5, 0, 0, time.UTC)

	processed := external(t, w, wager.Refund, "provider-a", "tx-processed", "tx-bet")
	must(t, processed.MarkProcessed(amount(t, 100000, "BRL"), bet.State().ID))

	rejected := external(t, w, wager.Bet, "provider-a", "tx-rejected", "")
	must(t, rejected.MarkRejected(failure.InsufficientFunds))

	pending := external(t, w, wager.Rollback, "provider-a", "tx-pending", "tx-later")
	must(t, pending.MarkPendingReference(deadline, deadline.Add(-4*time.Minute)))

	failed := external(t, w, wager.Rollback, "provider-a", "tx-failed", "tx-later")
	must(t, failed.MarkPendingReference(deadline, deadline))
	_, err := failed.RecordUnexpectedError(deadline)
	must(t, err)
	must(t, failed.MarkFailed())

	opening, err := wager.NewOpening(w.State().ID, w.State().PlayerID, amount(t, 100000, "BRL"), "correlation-1")
	must(t, err)

	transactions := map[string]*wager.Transaction{
		"processed": processed, "rejected": rejected, "pending reference": pending, "failed": failed, "opening": opening,
	}
	for name, tx := range transactions {
		t.Run(name, func(t *testing.T) {
			db.write(t, func(ctx context.Context) error { return db.transactions.Insert(ctx, tx) })

			stored, err := db.transactions.Get(ctx, tx.State().ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if stored.State() != tx.State() {
				t.Fatalf("Get =\n%+v\nwant\n%+v", stored.State(), tx.State())
			}
		})
	}

	if _, err := db.transactions.Get(ctx, parsed(t, ids.ParseTransactionID, otherTxA)); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("unknown transaction: err = %v, want %v", err, app.ErrNotFound)
	}
}

func TestTransactionUniquenessAndProviderScope(t *testing.T) {
	ctx := context.Background()
	db := newDatabase(t)
	w := db.openWallet(t, 100000)
	bet := db.processedBet(t, w, "tx-1")
	metadata := bet.State().External
	otherProvider := parsed(t, ids.ParseProviderID, "provider-b")

	sameExternalID := external(t, w, wager.Bet, "provider-a", "tx-1", "")
	must(t, sameExternalID.MarkRejected(failure.InsufficientFunds))
	err := db.runner.Run(ctx, func(ctx context.Context) error { return db.transactions.Insert(ctx, sameExternalID) })
	if !errors.Is(err, app.ErrUniqueViolation) {
		t.Fatalf("same provider and external id: err = %v, want %v", err, app.ErrUniqueViolation)
	}

	byKey, err := db.transactions.FindByIdempotencyKey(ctx, metadata.ProviderID, metadata.IdempotencyKey)
	if err != nil || byKey.State() != bet.State() {
		t.Fatalf("FindByIdempotencyKey = %+v, %v, want the bet", byKey, err)
	}
	byExternalID, err := db.transactions.FindByExternalID(ctx, metadata.ProviderID, metadata.ExternalID)
	if err != nil || byExternalID.State() != bet.State() {
		t.Fatalf("FindByExternalID = %+v, %v, want the bet", byExternalID, err)
	}
	if _, err := db.transactions.FindByIdempotencyKey(ctx, otherProvider, metadata.IdempotencyKey); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("key of another provider: err = %v, want %v", err, app.ErrNotFound)
	}
	if _, err := db.transactions.FindByExternalID(ctx, otherProvider, metadata.ExternalID); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("external id of another provider: err = %v, want %v", err, app.ErrNotFound)
	}

	sameIDsOtherProvider := external(t, w, wager.Bet, "provider-b", "tx-1", "")
	must(t, sameIDsOtherProvider.MarkProcessed(amount(t, 95000, "BRL"), ids.TransactionID{}))
	db.write(t, func(ctx context.Context) error { return db.transactions.Insert(ctx, sameIDsOtherProvider) })
}

func TestPendingReferenceLifecycle(t *testing.T) {
	ctx := context.Background()
	db := newDatabase(t)
	w := db.openWallet(t, 100000)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	insertPending := func(externalID string, nextAttemptAt time.Time) *wager.Transaction {
		tx := external(t, w, wager.Refund, "provider-a", externalID, "tx-later")
		must(t, tx.MarkPendingReference(now.Add(5*time.Minute), nextAttemptAt))
		db.write(t, func(ctx context.Context) error { return db.transactions.Insert(ctx, tx) })
		return tx
	}
	due := insertPending("tx-due", now.Add(-time.Second))
	dueEarlier := insertPending("tx-due-earlier", now.Add(-time.Minute))
	insertPending("tx-future", now.Add(time.Second))
	db.processedBet(t, w, "tx-terminal")

	listed, err := db.transactions.ListDuePendingReferences(ctx, now, 10)
	must(t, err)
	if len(listed) != 2 || listed[0].State().ID != dueEarlier.State().ID || listed[1].State().ID != due.State().ID {
		t.Fatalf("listed %d transactions, want the two due ones, oldest attempt first", len(listed))
	}
	limited, err := db.transactions.ListDuePendingReferences(ctx, now, 1)
	must(t, err)
	if len(limited) != 1 {
		t.Fatalf("listed %d transactions with limit 1", len(limited))
	}

	db.write(t, func(ctx context.Context) error {
		locked, err := db.transactions.GetForUpdate(ctx, due.State().ID)
		if err != nil {
			return err
		}
		if err := locked.Reschedule(now.Add(2 * time.Second)); err != nil {
			return err
		}
		due = locked
		return db.transactions.Update(ctx, locked)
	})
	stored, err := db.transactions.Get(ctx, due.State().ID)
	must(t, err)
	if stored.State() != due.State() || stored.State().Attempts != 1 {
		t.Fatalf("after rescheduling = %+v, want %+v", stored.State(), due.State())
	}

	bet := db.processedBet(t, w, "tx-bet")
	processed := insertPending("tx-to-process", now.Add(-time.Second))
	must(t, processed.MarkProcessed(amount(t, 100000, "BRL"), bet.State().ID))
	failed := insertPending("tx-to-fail", now.Add(-time.Second))
	_, err = failed.RecordUnexpectedError(now)
	must(t, err)
	must(t, failed.MarkFailed())
	must(t, due.MarkRejected(failure.ReferenceNotFound))

	exits := map[string]*wager.Transaction{"processed": processed, "rejected": due, "failed": failed}
	for name, tx := range exits {
		db.write(t, func(ctx context.Context) error { return db.transactions.Update(ctx, tx) })
		stored, err := db.transactions.Get(ctx, tx.State().ID)
		must(t, err)
		if stored.State() != tx.State() {
			t.Fatalf("%s: stored\n%+v\nwant\n%+v", name, stored.State(), tx.State())
		}
	}

	stillDue, err := db.transactions.ListDuePendingReferences(ctx, now, 10)
	must(t, err)
	if len(stillDue) != 1 || stillDue[0].State().ID != dueEarlier.State().ID {
		t.Fatalf("listed %d transactions after three left the pending state, want only the one still waiting", len(stillDue))
	}
}

func (d database) insertEntry(t *testing.T, w *wallet.Wallet, transactionID ids.TransactionID, direction ledger.Direction, units, before, after, version int64) ledger.Entry {
	t.Helper()

	entry, err := ledger.NewEntry(ledger.Fields{
		WalletID:      w.State().ID,
		TransactionID: transactionID,
		Direction:     direction,
		Amount:        amount(t, units, "BRL"),
		BalanceBefore: amount(t, before, "BRL"),
		BalanceAfter:  amount(t, after, "BRL"),
		WalletVersion: version,
	})
	must(t, err)
	d.write(t, func(ctx context.Context) error { return d.ledger.Insert(ctx, entry) })
	return entry
}

func TestLedgerRepository(t *testing.T) {
	ctx := context.Background()
	db := newDatabase(t)
	w := db.openWallet(t, 100000)
	other := db.openWallet(t, 0)

	empty, err := db.ledger.Totals(ctx, w.State().ID)
	must(t, err)
	if empty != (app.LedgerTotals{}) {
		t.Fatalf("Totals of an empty ledger = %+v, want zeros", empty)
	}

	first := db.insertEntry(t, w, db.processedBet(t, w, "tx-1").State().ID, ledger.Credit, 100000, 0, 100000, 1)
	second := db.insertEntry(t, w, db.processedBet(t, w, "tx-2").State().ID, ledger.Debit, 2500, 100000, 97500, 2)
	third := db.insertEntry(t, w, db.processedBet(t, w, "tx-3").State().ID, ledger.Debit, 7500, 97500, 90000, 3)
	db.insertEntry(t, other, db.processedBet(t, other, "tx-other").State().ID, ledger.Credit, 500, 0, 500, 1)

	duplicate, err := ledger.NewEntry(second.Fields())
	must(t, err)
	err = db.runner.Run(ctx, func(ctx context.Context) error { return db.ledger.Insert(ctx, duplicate) })
	if !errors.Is(err, app.ErrUniqueViolation) {
		t.Fatalf("second entry for the same transaction: err = %v, want %v", err, app.ErrUniqueViolation)
	}

	page, err := db.ledger.ListPage(ctx, w.State().ID, 0, 2)
	must(t, err)
	if len(page) != 2 || page[0] != first || page[1] != second {
		t.Fatalf("first page = %+v, want the first two entries in order", page)
	}

	fourth := db.insertEntry(t, w, db.processedBet(t, w, "tx-4").State().ID, ledger.Credit, 1000, 90000, 91000, 4)
	page, err = db.ledger.ListPage(ctx, w.State().ID, 2, 2)
	must(t, err)
	if len(page) != 2 || page[0] != third || page[1] != fourth {
		t.Fatalf("second page = %+v, want the third entry and the one created during navigation", page)
	}
	page, err = db.ledger.ListPage(ctx, w.State().ID, 4, 2)
	must(t, err)
	if len(page) != 0 {
		t.Fatalf("page after the last entry = %+v, want it empty", page)
	}

	totals, err := db.ledger.Totals(ctx, w.State().ID)
	must(t, err)
	if totals != (app.LedgerTotals{CreditUnits: 101000, DebitUnits: 10000, Entries: 4}) {
		t.Fatalf("Totals = %+v, want 1010.00 in credits and 100.00 in debits over 4 entries", totals)
	}
}

func (d database) insertEvent(t *testing.T, w *wallet.Wallet, version int64) events.Event {
	t.Helper()

	transactionID, err := ids.NewTransactionID()
	must(t, err)
	event, err := events.NewWalletBalanceChanged(
		events.Origin{CorrelationID: "correlation-1", CausationID: "message-1"},
		events.WalletBalanceChanged{
			WalletID:      w.State().ID,
			TransactionID: transactionID,
			Direction:     ledger.Credit,
			Money:         amount(t, 2500, "BRL"),
			BalanceBefore: amount(t, 0, "BRL"),
			BalanceAfter:  amount(t, 2500, "BRL"),
			WalletVersion: version,
		})
	must(t, err)
	d.write(t, func(ctx context.Context) error { return d.outbox.Insert(ctx, event) })
	return event
}

func TestOutboxInsertAndClaim(t *testing.T) {
	ctx := context.Background()
	db := newDatabase(t)
	w := db.openWallet(t, 0)

	if age, err := db.outbox.OldestPendingAge(ctx); err != nil || age != 0 {
		t.Fatalf("OldestPendingAge of an empty outbox = %s, %v, want 0", age, err)
	}

	event := db.insertEvent(t, w, 2)
	if age, err := db.outbox.OldestPendingAge(ctx); err != nil || age <= 0 {
		t.Fatalf("OldestPendingAge with a pending event = %s, %v, want it positive", age, err)
	}

	claimed, err := db.outbox.Claim(ctx, 10, 200*time.Millisecond)
	must(t, err)
	if len(claimed) != 1 || claimed[0].EventID != event.Header().EventID || claimed[0].GroupKey != w.State().ID.String() {
		t.Fatalf("Claim = %+v, want the event grouped by its wallet", claimed)
	}
	original, err := json.Marshal(event)
	must(t, err)
	var want, got any
	must(t, json.Unmarshal(original, &want))
	must(t, json.Unmarshal(claimed[0].Payload, &got))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("payload = %s, want the same content as %s", claimed[0].Payload, original)
	}

	again, err := db.outbox.Claim(ctx, 10, 200*time.Millisecond)
	must(t, err)
	if len(again) != 0 {
		t.Fatalf("a reserved event was claimed again: %+v", again)
	}

	time.Sleep(300 * time.Millisecond)
	retaken, err := db.outbox.Claim(ctx, 10, time.Minute)
	must(t, err)
	if len(retaken) != 1 {
		t.Fatalf("claimed %d events after the lease expired, want 1", len(retaken))
	}

	must(t, db.outbox.MarkFailed(ctx, event.Header().EventID, time.Now().Add(time.Hour), "broker down"))
	postponed, err := db.outbox.Claim(ctx, 10, time.Minute)
	must(t, err)
	if len(postponed) != 0 {
		t.Fatalf("an event scheduled for later was claimed: %+v", postponed)
	}

	must(t, db.outbox.MarkFailed(ctx, event.Header().EventID, time.Now().Add(-time.Second), "broker down"))
	retried, err := db.outbox.Claim(ctx, 10, time.Minute)
	must(t, err)
	if len(retried) != 1 || retried[0].Attempts != 2 {
		t.Fatalf("Claim after two failures = %+v, want the event with two attempts", retried)
	}

	var lastError string
	must(t, db.pool.QueryRow(ctx, `SELECT last_error FROM outbox WHERE event_id = $1`, event.Header().EventID.String()).Scan(&lastError))
	if lastError != "broker down" {
		t.Fatalf("last_error = %q, want the reason of the failure", lastError)
	}

	must(t, db.outbox.MarkPublished(ctx, event.Header().EventID))
	exec(t, db.pool, `UPDATE outbox SET locked_until = NULL WHERE event_id = $1`, event.Header().EventID.String())
	published, err := db.outbox.Claim(ctx, 10, time.Minute)
	must(t, err)
	if len(published) != 0 {
		t.Fatalf("a published event was claimed: %+v", published)
	}
	if age, err := db.outbox.OldestPendingAge(ctx); err != nil || age != 0 {
		t.Fatalf("OldestPendingAge after publishing = %s, %v, want 0", age, err)
	}
}
