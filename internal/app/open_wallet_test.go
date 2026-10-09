package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/wager"
)

func TestOpenWalletWithBalance(t *testing.T) {
	f := newFixture()

	opened := f.open(t, 100000)
	if opened.Balance.Amount() != "1000.00" || opened.Version != 1 {
		t.Fatalf("wallet = %+v, want 1000.00 at version 1", opened)
	}

	transactions := f.store.TransactionStates()
	if len(transactions) != 1 {
		t.Fatalf("transactions = %d, want the opening", len(transactions))
	}
	opening := transactions[0]
	if opening.Kind != wager.Opening || opening.Status != wager.Processed || opening.External != (wager.External{}) {
		t.Fatalf("opening = %+v, want a processed OPENING without provider data", opening)
	}
	if opening.WalletID != opened.ID || opening.ResultBalance.Amount() != "1000.00" || opening.CorrelationID != "correlation-open" {
		t.Fatalf("opening = %+v, want the wallet, the balance and the correlation id", opening)
	}

	entries := f.store.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	fields := entries[0].Fields()
	if fields.Direction != ledger.Credit || fields.TransactionID != opening.ID || fields.WalletVersion != 1 {
		t.Fatalf("entry = %+v, want a credit of the opening at version 1", fields)
	}
	if fields.BalanceBefore.Amount() != "0.00" || fields.BalanceAfter.Amount() != "1000.00" || fields.Amount.Amount() != "1000.00" {
		t.Fatalf("entry = %+v, want 0.00 to 1000.00", fields)
	}

	if got := f.eventTypes(); !slices.Equal(got, []string{"WalletBalanceChanged", "WagerTransactionProcessed"}) {
		t.Fatalf("events = %v, want the balance change and the processed opening", got)
	}
}

func TestOpenWalletWithZeroBalance(t *testing.T) {
	f := newFixture()

	opened := f.open(t, 0)
	if opened.Balance.Amount() != "0.00" || opened.Version != 1 {
		t.Fatalf("wallet = %+v, want 0.00 at version 1", opened)
	}
	if len(f.store.TransactionStates()) != 0 || len(f.store.Entries()) != 0 || len(f.store.Events()) != 0 {
		t.Fatal("opening with zero created a transaction, an entry or an event")
	}
}

func TestOpenWalletRejects(t *testing.T) {
	ctx := context.Background()
	f := newFixture()
	f.open(t, 100000)
	player := parsed(t, ids.ParsePlayerID, playerUUID)

	_, err := f.service.OpenWallet(ctx, app.OpenWalletInput{PlayerID: player, InitialBalance: amount(t, 5000, "BRL"), CorrelationID: "c"})
	wantConflict(t, err, app.ConflictWalletAlreadyExists)
	if len(f.store.WalletStates()) != 1 || len(f.store.Entries()) != 1 {
		t.Fatal("a duplicate opening left records behind")
	}

	_, err = f.service.OpenWallet(ctx, app.OpenWalletInput{PlayerID: player, InitialBalance: amount(t, -1, "USD"), CorrelationID: "c"})
	wantInvalidInput(t, err, failure.InvalidMoney)

	if _, err := f.service.OpenWallet(ctx, app.OpenWalletInput{PlayerID: player, InitialBalance: amount(t, 0, "USD"), CorrelationID: "c"}); err != nil {
		t.Fatalf("same player in another currency: %v", err)
	}
}

func TestOpenWalletIsAtomic(t *testing.T) {
	writes := []string{"wallets.Insert", "transactions.Insert", "ledger.Insert", "outbox.Insert"}

	for _, write := range writes {
		t.Run(write, func(t *testing.T) {
			f := newFixture()
			f.store.FailNext(write, errBoom)

			_, err := f.service.OpenWallet(context.Background(), app.OpenWalletInput{
				PlayerID:       parsed(t, ids.ParsePlayerID, playerUUID),
				InitialBalance: amount(t, 100000, "BRL"),
				CorrelationID:  "c",
			})
			if !errors.Is(err, errBoom) {
				t.Fatalf("err = %v, want %v", err, errBoom)
			}
			if len(f.store.WalletStates())+len(f.store.TransactionStates())+len(f.store.Entries())+len(f.store.Events()) != 0 {
				t.Fatal("a failed opening left records behind")
			}
		})
	}
}
