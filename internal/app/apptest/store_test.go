package apptest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/app/apptest"
	"jungle-gaming-challeng/internal/domain/events"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
	"jungle-gaming-challeng/internal/domain/wallet"
)

var errBoom = errors.New("boom")

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

func player(t *testing.T) ids.PlayerID {
	t.Helper()

	return parsed(t, ids.ParsePlayerID, "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1")
}

func openWallet(t *testing.T, units int64, code string) (*wallet.Wallet, *wallet.Movement) {
	t.Helper()

	w, movement, err := wallet.Open(player(t), amount(t, units, code))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return w, movement
}

func transaction(t *testing.T, walletID ids.WalletID, kind wager.Kind, provider, externalID, key, reference string) *wager.Transaction {
	t.Helper()

	external := wager.External{
		ProviderID:     parsed(t, ids.ParseProviderID, provider),
		ExternalID:     parsed(t, ids.ParseExternalTransactionID, externalID),
		IdempotencyKey: parsed(t, ids.ParseIdempotencyKey, key),
		PayloadHash:    "hash",
		RoundID:        parsed(t, ids.ParseRoundID, "round-1"),
		GameID:         parsed(t, ids.ParseGameID, "game-1"),
	}
	if reference != "" {
		external.ReferenceExternalID = parsed(t, ids.ParseExternalTransactionID, reference)
	}
	tx, err := wager.NewExternal(kind, walletID, player(t), amount(t, 2500, "BRL"), external, "correlation-1")
	if err != nil {
		t.Fatalf("NewExternal: %v", err)
	}
	return tx
}

func entry(t *testing.T, walletID ids.WalletID, transactionID ids.TransactionID, version int64) ledger.Entry {
	t.Helper()

	e, err := ledger.NewEntry(ledger.Fields{
		WalletID:      walletID,
		TransactionID: transactionID,
		Direction:     ledger.Credit,
		Amount:        amount(t, 2500, "BRL"),
		BalanceBefore: amount(t, 0, "BRL"),
		BalanceAfter:  amount(t, 2500, "BRL"),
		WalletVersion: version,
	})
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	return e
}

func TestWalletsAreUniquePerPlayerAndCurrency(t *testing.T) {
	ctx := context.Background()
	store := apptest.NewStore()
	first, _ := openWallet(t, 100000, "BRL")
	sameCurrency, _ := openWallet(t, 0, "BRL")
	otherCurrency, _ := openWallet(t, 0, "USD")

	if err := store.Wallets().Insert(ctx, first); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := store.Wallets().Insert(ctx, sameCurrency); !errors.Is(err, app.ErrUniqueViolation) {
		t.Fatalf("same player and currency: err = %v, want %v", err, app.ErrUniqueViolation)
	}
	if err := store.Wallets().Insert(ctx, otherCurrency); err != nil {
		t.Fatalf("same player, other currency: %v", err)
	}

	stored, err := store.Wallets().Get(ctx, first.State().ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.State() != first.State() {
		t.Fatalf("Get = %+v, want %+v", stored.State(), first.State())
	}
	if _, err := store.Wallets().Get(ctx, parsed(t, ids.ParseWalletID, "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a9")); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("unknown wallet: err = %v, want %v", err, app.ErrNotFound)
	}
}

func TestLockingReadsNeedATransaction(t *testing.T) {
	ctx := context.Background()
	store := apptest.NewStore()
	w, _ := openWallet(t, 100000, "BRL")
	if err := store.Wallets().Insert(ctx, w); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if _, err := store.Wallets().GetForUpdate(ctx, w.State().ID); !errors.Is(err, apptest.ErrOutsideTransaction) {
		t.Fatalf("outside a transaction: err = %v, want %v", err, apptest.ErrOutsideTransaction)
	}
	err := store.Run(ctx, func(ctx context.Context) error {
		_, err := store.Wallets().GetForUpdate(ctx, w.State().ID)
		return err
	})
	if err != nil {
		t.Fatalf("inside a transaction: %v", err)
	}
}

func TestRunRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	store := apptest.NewStore()
	w, _ := openWallet(t, 100000, "BRL")
	tx := transaction(t, w.State().ID, wager.Bet, "provider-a", "tx-1", "key-1", "")

	err := store.Run(ctx, func(ctx context.Context) error {
		if err := store.Wallets().Insert(ctx, w); err != nil {
			return err
		}
		if err := store.Transactions().Insert(ctx, tx); err != nil {
			return err
		}
		if err := store.Ledger().Insert(ctx, entry(t, w.State().ID, tx.State().ID, 2)); err != nil {
			return err
		}
		if _, err := store.Inbox().Register(ctx, "wager-consumer", "msg-1", "hash-1"); err != nil {
			return err
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want %v", err, errBoom)
	}
	if len(store.WalletStates()) != 0 || len(store.TransactionStates()) != 0 || len(store.Entries()) != 0 {
		t.Fatal("a failed transaction left records behind")
	}
	status, err := store.Inbox().Register(ctx, "wager-consumer", "msg-1", "hash-1")
	if err != nil || status != app.InboxNew {
		t.Fatalf("Register after the rollback = %s, %v, want %s", status, err, app.InboxNew)
	}
}

func TestNestedRunUndoesOnlyTheInnerBlock(t *testing.T) {
	ctx := context.Background()
	store := apptest.NewStore()
	w, _ := openWallet(t, 100000, "BRL")
	other, _ := openWallet(t, 0, "USD")

	err := store.Run(ctx, func(ctx context.Context) error {
		if err := store.Wallets().Insert(ctx, w); err != nil {
			return err
		}
		inner := store.Run(ctx, func(ctx context.Context) error {
			if err := store.Wallets().Insert(ctx, other); err != nil {
				return err
			}
			return errBoom
		})
		if !errors.Is(inner, errBoom) {
			t.Errorf("inner err = %v, want %v", inner, errBoom)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if states := store.WalletStates(); len(states) != 1 || states[0].ID != w.State().ID {
		t.Fatalf("wallets = %+v, want only the one written by the outer block", states)
	}
}

func TestUpdateBalanceStoresTheNewState(t *testing.T) {
	ctx := context.Background()
	store := apptest.NewStore()
	w, _ := openWallet(t, 100000, "BRL")
	if err := store.Wallets().Insert(ctx, w); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if _, err := w.Debit(amount(t, 2500, "BRL")); err != nil {
		t.Fatalf("Debit: %v", err)
	}
	if err := store.Wallets().UpdateBalance(ctx, w); err != nil {
		t.Fatalf("UpdateBalance: %v", err)
	}

	stored, err := store.Wallets().Get(ctx, w.State().ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.State() != w.State() {
		t.Fatalf("Get = %+v, want %+v", stored.State(), w.State())
	}
}

func TestFailNextInjectsOneFailure(t *testing.T) {
	ctx := context.Background()
	store := apptest.NewStore()
	w, _ := openWallet(t, 100000, "BRL")
	store.FailNext("wallets.Insert", errBoom)

	if err := store.Wallets().Insert(ctx, w); !errors.Is(err, errBoom) {
		t.Fatalf("first call: err = %v, want %v", err, errBoom)
	}
	if err := store.Wallets().Insert(ctx, w); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if len(store.Calls) != 2 || store.Calls[0] != "wallets.Insert" {
		t.Fatalf("Calls = %v, want the two inserts", store.Calls)
	}
}

func TestTransactionsAreUnique(t *testing.T) {
	ctx := context.Background()
	w, _ := openWallet(t, 100000, "BRL")
	walletID := w.State().ID

	tests := []struct {
		name    string
		second  *wager.Transaction
		wantErr error
	}{
		{"same key", transaction(t, walletID, wager.Bet, "provider-a", "tx-2", "key-1", ""), app.ErrUniqueViolation},
		{"same external id", transaction(t, walletID, wager.Bet, "provider-a", "tx-1", "key-2", ""), app.ErrUniqueViolation},
		{"another provider", transaction(t, walletID, wager.Bet, "provider-b", "tx-1", "key-1", ""), nil},
		{"another operation", transaction(t, walletID, wager.Bet, "provider-a", "tx-2", "key-2", ""), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := apptest.NewStore()
			first := transaction(t, walletID, wager.Bet, "provider-a", "tx-1", "key-1", "")
			if err := store.Transactions().Insert(ctx, first); err != nil {
				t.Fatalf("Insert: %v", err)
			}
			if err := store.Transactions().Insert(ctx, tt.second); !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestTransactionLookupsAreScopedByProvider(t *testing.T) {
	ctx := context.Background()
	store := apptest.NewStore()
	w, _ := openWallet(t, 100000, "BRL")
	tx := transaction(t, w.State().ID, wager.Bet, "provider-a", "tx-1", "key-1", "")
	if err := store.Transactions().Insert(ctx, tx); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	external := tx.State().External
	other := parsed(t, ids.ParseProviderID, "provider-b")

	byKey, err := store.Transactions().FindByIdempotencyKey(ctx, external.ProviderID, external.IdempotencyKey)
	if err != nil || byKey.State() != tx.State() {
		t.Fatalf("FindByIdempotencyKey = %+v, %v, want the transaction", byKey, err)
	}
	byExternalID, err := store.Transactions().FindByExternalID(ctx, external.ProviderID, external.ExternalID)
	if err != nil || byExternalID.State() != tx.State() {
		t.Fatalf("FindByExternalID = %+v, %v, want the transaction", byExternalID, err)
	}
	if _, err := store.Transactions().FindByIdempotencyKey(ctx, other, external.IdempotencyKey); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("key of another provider: err = %v, want %v", err, app.ErrNotFound)
	}
	if _, err := store.Transactions().FindByExternalID(ctx, other, external.ExternalID); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("external id of another provider: err = %v, want %v", err, app.ErrNotFound)
	}
}

func TestOneProcessedReversalPerReference(t *testing.T) {
	ctx := context.Background()
	store := apptest.NewStore()
	w, _ := openWallet(t, 100000, "BRL")
	walletID := w.State().ID
	bet := transaction(t, walletID, wager.Bet, "provider-a", "tx-1", "key-1", "")
	refund := transaction(t, walletID, wager.Refund, "provider-a", "tx-2", "key-2", "tx-1")
	rollback := transaction(t, walletID, wager.Rollback, "provider-a", "tx-3", "key-3", "tx-1")

	if taken, err := store.Transactions().HasProcessedReversal(ctx, bet.State().ID); err != nil || taken {
		t.Fatalf("HasProcessedReversal before any reversal = %t, %v, want false", taken, err)
	}
	if err := refund.MarkProcessed(amount(t, 100000, "BRL"), bet.State().ID); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	if err := store.Transactions().Insert(ctx, refund); err != nil {
		t.Fatalf("Insert refund: %v", err)
	}
	taken, err := store.Transactions().HasProcessedReversal(ctx, bet.State().ID)
	if err != nil || !taken {
		t.Fatalf("HasProcessedReversal = %t, %v, want true", taken, err)
	}

	if err := rollback.MarkProcessed(amount(t, 100000, "BRL"), bet.State().ID); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	if err := store.Transactions().Insert(ctx, rollback); !errors.Is(err, app.ErrUniqueViolation) {
		t.Fatalf("second processed reversal: err = %v, want %v", err, app.ErrUniqueViolation)
	}
}

func TestPendingReferencesAreListedWhenDue(t *testing.T) {
	ctx := context.Background()
	store := apptest.NewStore()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	w, _ := openWallet(t, 100000, "BRL")
	walletID := w.State().ID

	due := transaction(t, walletID, wager.Refund, "provider-a", "tx-1", "key-1", "tx-0")
	later := transaction(t, walletID, wager.Refund, "provider-a", "tx-2", "key-2", "tx-0")
	if err := due.MarkPendingReference(now.Add(5*time.Minute), now.Add(-time.Second)); err != nil {
		t.Fatalf("MarkPendingReference: %v", err)
	}
	if err := later.MarkPendingReference(now.Add(5*time.Minute), now.Add(time.Second)); err != nil {
		t.Fatalf("MarkPendingReference: %v", err)
	}
	for _, tx := range []*wager.Transaction{due, later} {
		if err := store.Transactions().Insert(ctx, tx); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	listed, err := store.Transactions().ListDuePendingReferences(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListDuePendingReferences: %v", err)
	}
	if len(listed) != 1 || listed[0].State().ID != due.State().ID {
		t.Fatalf("listed %d transactions, want only the due one", len(listed))
	}

	if err := due.MarkRejected(failure.ReferenceNotFound); err != nil {
		t.Fatalf("MarkRejected: %v", err)
	}
	if err := store.Transactions().Update(ctx, due); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := store.Transactions().Update(ctx, due); err == nil {
		t.Fatal("updating a terminal transaction succeeded")
	}
}

func TestLedgerIsUniqueAndPaged(t *testing.T) {
	ctx := context.Background()
	store := apptest.NewStore()
	w, _ := openWallet(t, 100000, "BRL")
	walletID := w.State().ID
	transactions := []ids.TransactionID{
		parsed(t, ids.ParseTransactionID, "0192f28f-5dc0-7d58-bdb2-814ad6a0f4b1"),
		parsed(t, ids.ParseTransactionID, "0192f28f-5dc0-7d58-bdb2-814ad6a0f4b2"),
		parsed(t, ids.ParseTransactionID, "0192f28f-5dc0-7d58-bdb2-814ad6a0f4b3"),
	}
	for i, transactionID := range transactions {
		if err := store.Ledger().Insert(ctx, entry(t, walletID, transactionID, int64(i+1))); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	if err := store.Ledger().Insert(ctx, entry(t, walletID, transactions[0], 9)); !errors.Is(err, app.ErrUniqueViolation) {
		t.Fatalf("same transaction: err = %v, want %v", err, app.ErrUniqueViolation)
	}
	fourth := parsed(t, ids.ParseTransactionID, "0192f28f-5dc0-7d58-bdb2-814ad6a0f4b4")
	if err := store.Ledger().Insert(ctx, entry(t, walletID, fourth, 3)); !errors.Is(err, app.ErrUniqueViolation) {
		t.Fatalf("same version: err = %v, want %v", err, app.ErrUniqueViolation)
	}

	page, err := store.Ledger().ListPage(ctx, walletID, 1, 1)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if len(page) != 1 || page[0].Fields().WalletVersion != 2 {
		t.Fatalf("page = %+v, want only version 2", page)
	}

	totals, err := store.Ledger().Totals(ctx, walletID)
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}
	if totals != (app.LedgerTotals{CreditUnits: 7500, Entries: 3}) {
		t.Fatalf("Totals = %+v, want 75.00 in credits over 3 entries", totals)
	}
}

func TestOutboxKeepsEventsUntilPublished(t *testing.T) {
	ctx := context.Background()
	store := apptest.NewStore()
	w, _ := openWallet(t, 100000, "BRL")
	event, err := events.NewWalletBalanceChanged(events.Origin{CorrelationID: "correlation-1"}, events.WalletBalanceChanged{
		WalletID:      w.State().ID,
		TransactionID: parsed(t, ids.ParseTransactionID, "0192f28f-5dc0-7d58-bdb2-814ad6a0f4b1"),
		Direction:     ledger.Credit,
		Money:         amount(t, 2500, "BRL"),
		BalanceBefore: amount(t, 0, "BRL"),
		BalanceAfter:  amount(t, 2500, "BRL"),
		WalletVersion: 2,
	})
	if err != nil {
		t.Fatalf("NewWalletBalanceChanged: %v", err)
	}
	if err := store.Outbox().Insert(ctx, event); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	claimed, err := store.Outbox().Claim(ctx, 10, time.Second)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].EventID != event.Header().EventID || claimed[0].GroupKey != w.State().ID.String() {
		t.Fatalf("Claim = %+v, want the event grouped by its wallet", claimed)
	}

	if err := store.Outbox().MarkFailed(ctx, event.Header().EventID, time.Now(), "broker down"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	claimed, err = store.Outbox().Claim(ctx, 10, time.Second)
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 1 {
		t.Fatalf("Claim after a failure = %+v, %v, want the event with one attempt", claimed, err)
	}

	if err := store.Outbox().MarkPublished(ctx, event.Header().EventID); err != nil {
		t.Fatalf("MarkPublished: %v", err)
	}
	claimed, err = store.Outbox().Claim(ctx, 10, time.Second)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("Claim after publishing = %+v, %v, want nothing", claimed, err)
	}
}

func TestInboxRegistersEachMessageOnce(t *testing.T) {
	ctx := context.Background()
	store := apptest.NewStore()

	steps := []struct {
		name      string
		messageID string
		hash      string
		want      app.InboxStatus
	}{
		{"first delivery", "msg-1", "hash-1", app.InboxNew},
		{"redelivery", "msg-1", "hash-1", app.InboxDuplicate},
		{"same id, other body", "msg-1", "hash-2", app.InboxHashMismatch},
		{"another message", "msg-2", "hash-1", app.InboxNew},
	}

	for _, step := range steps {
		got, err := store.Inbox().Register(ctx, "wager-consumer", step.messageID, step.hash)
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got != step.want {
			t.Fatalf("%s: Register = %s, want %s", step.name, got, step.want)
		}
	}

	if err := store.Inbox().Complete(ctx, "wager-consumer", "msg-1"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := store.Inbox().Complete(ctx, "wager-consumer", "msg-9"); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("Complete of an unknown message: err = %v, want %v", err, app.ErrNotFound)
	}
}
