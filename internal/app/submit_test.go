package app_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/events"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/wager"
)

func TestSubmitLocksTheWalletFirst(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)
	f.store.Calls = nil

	f.submit(t, operation(t, w, wager.Bet, 2500, "tx-1", ""))

	want := []string{
		"wallets.GetForUpdate",
		"transactions.FindByIdempotencyKey",
		"transactions.FindByExternalID",
		"transactions.Insert",
		"wallets.UpdateBalance",
		"ledger.Insert",
		"outbox.Insert",
		"outbox.Insert",
	}
	if !slices.Equal(f.store.Calls, want) {
		t.Fatalf("calls =\n%v\nwant\n%v", f.store.Calls, want)
	}
}

func TestSubmitInvalidInputWritesNothing(t *testing.T) {
	ctx := context.Background()
	f := newFixture()
	w := f.open(t, 100000)
	before := f.snapshot(t, w.ID)

	unknownWallet := operation(t, w, wager.Bet, 2500, "tx-1", "")
	unknownWallet.WalletID = parsed(t, ids.ParseWalletID, otherUUID)

	tests := []struct {
		name string
		in   app.SubmitInput
		code failure.Code
	}{
		{"unknown wallet", unknownWallet, failure.WalletNotFound},
		{"bet of zero", operation(t, w, wager.Bet, 0, "tx-1", ""), failure.InvalidAmountForKind},
		{"loss above zero", operation(t, w, wager.Loss, 2500, "tx-1", ""), failure.InvalidAmountForKind},
		{"refund without reference", operation(t, w, wager.Refund, 2500, "tx-1", ""), failure.MissingReference},
		{"bet with reference", operation(t, w, wager.Bet, 2500, "tx-1", "tx-0"), failure.UnexpectedReference},
		{"opening sent by a provider", operation(t, w, wager.Opening, 2500, "tx-1", ""), failure.UnsupportedKind},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.service.SubmitTransaction(ctx, tt.in)
			wantInvalidInput(t, err, tt.code)
			if after := f.snapshot(t, w.ID); after != before {
				t.Fatalf("state = %+v, want it unchanged: %+v", after, before)
			}
		})
	}

	corrected := f.submit(t, operation(t, w, wager.Bet, 2500, "tx-1", ""))
	if corrected.Status != wager.Processed {
		t.Fatalf("resend after an invalid input = %s, want PROCESSED", corrected.Status)
	}
}

func TestSubmitBet(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)

	in := operation(t, w, wager.Bet, 2500, "tx-1", "")
	in.CausationID = "message-1"
	bet := f.submit(t, in)
	if bet.Status != wager.Processed || bet.ResultBalance.Amount() != "975.00" || bet.External.PayloadHash == "" {
		t.Fatalf("bet = %+v, want PROCESSED with 975.00 and a payload hash", bet)
	}
	if got := f.wallet(t, w.ID); got.Balance.Amount() != "975.00" || got.Version != 2 {
		t.Fatalf("wallet = %+v, want 975.00 at version 2", got)
	}

	entries := f.store.Entries()
	debit := entries[len(entries)-1].Fields()
	if len(entries) != 2 || debit.Direction != ledger.Debit || debit.TransactionID != bet.ID || debit.WalletVersion != 2 {
		t.Fatalf("last entry = %+v, want the debit of the bet at version 2", debit)
	}
	if debit.BalanceBefore.Amount() != "1000.00" || debit.BalanceAfter.Amount() != "975.00" {
		t.Fatalf("last entry = %+v, want 1000.00 to 975.00", debit)
	}
	if got := f.eventTypes()[2:]; !slices.Equal(got, []string{"WalletBalanceChanged", "WagerTransactionProcessed"}) {
		t.Fatalf("events of the bet = %v", got)
	}

	changed := f.store.Events()[2]
	wantChange := events.WalletBalanceChanged{
		WalletID:      w.ID,
		TransactionID: bet.ID,
		Direction:     ledger.Debit,
		Money:         amount(t, 2500, "BRL"),
		BalanceBefore: amount(t, 100000, "BRL"),
		BalanceAfter:  amount(t, 97500, "BRL"),
		WalletVersion: 2,
	}
	if changed.Data() != wantChange {
		t.Fatalf("balance change = %+v, want %+v", changed.Data(), wantChange)
	}
	for _, event := range f.store.Events()[2:] {
		if header := event.Header(); header.CorrelationID != "correlation-tx-1" || header.CausationID != "message-1" {
			t.Fatalf("event header = %+v, want the correlation and the causation of the request", header)
		}
	}
}

func TestSubmitBetWithoutFunds(t *testing.T) {
	f := newFixture()
	w := f.open(t, 2000)
	before := f.snapshot(t, w.ID)

	bet := f.submit(t, operation(t, w, wager.Bet, 8000, "tx-1", ""))
	wantRejection(t, bet, failure.InsufficientFunds)

	after := f.snapshot(t, w.ID)
	if after.balance != "20.00" || after.version != before.version || after.entries != before.entries {
		t.Fatalf("state = %+v, want the balance, the version and the ledger unchanged", after)
	}
	if got := f.eventTypes()[before.events:]; !slices.Equal(got, []string{"WagerTransactionRejected"}) {
		t.Fatalf("events of the rejection = %v", got)
	}
}

func TestSubmitWin(t *testing.T) {
	f := newFixture()
	w := f.open(t, 97500)

	win := f.submit(t, operation(t, w, wager.Win, 5000, "tx-1", ""))
	if win.Status != wager.Processed || win.ResultBalance.Amount() != "1025.00" {
		t.Fatalf("win = %+v, want PROCESSED with 1025.00", win)
	}
	if entry := f.store.Entries()[1].Fields(); entry.Direction != ledger.Credit || entry.Amount.Amount() != "50.00" {
		t.Fatalf("entry = %+v, want a credit of 50.00", entry)
	}
}

func TestSubmitWinAboveTheLimit(t *testing.T) {
	f := newFixture()
	w := f.open(t, math.MaxInt64)
	before := f.snapshot(t, w.ID)

	win := f.submit(t, operation(t, w, wager.Win, 1, "tx-1", ""))
	wantRejection(t, win, failure.BalanceOverflow)
	if after := f.snapshot(t, w.ID); after.balance != before.balance || after.entries != before.entries {
		t.Fatalf("state = %+v, want the balance and the ledger unchanged", after)
	}
}

func TestSubmitLoss(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)
	before := f.snapshot(t, w.ID)

	loss := f.submit(t, operation(t, w, wager.Loss, 0, "tx-1", ""))
	if loss.Status != wager.Processed || loss.ResultBalance.Amount() != "1000.00" {
		t.Fatalf("loss = %+v, want PROCESSED with the unchanged balance", loss)
	}

	after := f.snapshot(t, w.ID)
	if after.version != before.version || after.entries != before.entries || after.balance != before.balance {
		t.Fatalf("state = %+v, want balance, version and ledger unchanged", after)
	}
	if got := f.eventTypes()[before.events:]; !slices.Equal(got, []string{"WagerTransactionProcessed"}) {
		t.Fatalf("events of the loss = %v, want only the processed one", got)
	}
}

func TestSubmitDisagreesWithTheWallet(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)

	otherPlayer := operation(t, w, wager.Bet, 2500, "tx-1", "")
	otherPlayer.PlayerID = parsed(t, ids.ParsePlayerID, otherUUID)
	otherCurrency := operation(t, w, wager.Loss, 0, "tx-2", "")
	otherCurrency.Money = amount(t, 0, "USD")

	tests := []struct {
		name string
		in   app.SubmitInput
		code failure.Code
	}{
		{"another player", otherPlayer, failure.PlayerWalletMismatch},
		{"another currency", otherCurrency, failure.CurrencyMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := f.snapshot(t, w.ID)

			wantRejection(t, f.submit(t, tt.in), tt.code)

			after := f.snapshot(t, w.ID)
			if after.version != before.version || after.entries != before.entries || after.balance != before.balance {
				t.Fatalf("state = %+v, want balance, version and ledger unchanged", after)
			}
			if got := f.eventTypes()[before.events:]; !slices.Equal(got, []string{"WagerTransactionRejected"}) {
				t.Fatalf("events = %v, want only the rejection", got)
			}
		})
	}
}

func TestSubmitReplayReturnsTheOriginalResult(t *testing.T) {
	ctx := context.Background()
	f := newFixture()
	w := f.open(t, 100000)
	in := operation(t, w, wager.Bet, 2500, "tx-1", "")
	original := f.submit(t, in)
	f.submit(t, operation(t, w, wager.Win, 10000, "tx-2", ""))
	before := f.snapshot(t, w.ID)

	replay, err := f.service.SubmitTransaction(ctx, in)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.IdempotentReplay || replay.Transaction != original {
		t.Fatalf("replay = %+v, want the original transaction marked as replay", replay)
	}
	if replay.Transaction.ResultBalance.Amount() != "975.00" {
		t.Fatalf("replay balance = %s, want the 975.00 seen originally", replay.Transaction.ResultBalance.Amount())
	}
	if after := f.snapshot(t, w.ID); after != before {
		t.Fatalf("state = %+v, want it unchanged by the replay: %+v", after, before)
	}
}

func TestSubmitReplayOfARejection(t *testing.T) {
	ctx := context.Background()
	f := newFixture()
	w := f.open(t, 2000)
	in := operation(t, w, wager.Bet, 8000, "tx-1", "")
	f.submit(t, in)
	f.submit(t, operation(t, w, wager.Win, 100000, "tx-2", ""))

	replay, err := f.service.SubmitTransaction(ctx, in)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	wantRejection(t, replay.Transaction, failure.InsufficientFunds)
	if !replay.IdempotentReplay || f.wallet(t, w.ID).Balance.Amount() != "1020.00" {
		t.Fatalf("replay = %+v, want the original rejection and no debit", replay)
	}
}

func TestSubmitIdempotencyConflicts(t *testing.T) {
	ctx := context.Background()
	f := newFixture()
	w := f.open(t, 100000)
	original := operation(t, w, wager.Bet, 2500, "tx-1", "")
	f.submit(t, original)
	before := f.snapshot(t, w.ID)

	otherAmount := operation(t, w, wager.Bet, 3000, "tx-1", "")

	otherOperation := operation(t, w, wager.Bet, 2500, "tx-2", "")
	otherOperation.External.IdempotencyKey = original.External.IdempotencyKey

	otherKey := operation(t, w, wager.Bet, 2500, "tx-1", "")
	otherKey.External.IdempotencyKey = parsed(t, ids.ParseIdempotencyKey, "key-new")

	tests := []struct {
		name string
		in   app.SubmitInput
		code string
	}{
		{"same key, another amount", otherAmount, app.ConflictIdempotencyKeyReused},
		{"same key, another operation", otherOperation, app.ConflictIdempotencyKeyReused},
		{"same operation, another key", otherKey, app.ConflictTransactionAlreadyRegistered},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.service.SubmitTransaction(ctx, tt.in)
			wantConflict(t, err, tt.code)
			if after := f.snapshot(t, w.ID); after != before {
				t.Fatalf("state = %+v, want it unchanged: %+v", after, before)
			}
		})
	}

	otherProvider := operation(t, w, wager.Bet, 2500, "tx-1", "")
	otherProvider.External.ProviderID = parsed(t, ids.ParseProviderID, "provider-b")
	if got := f.submit(t, otherProvider); got.Status != wager.Processed {
		t.Fatalf("same key and external id from another provider = %s, want PROCESSED as a new operation", got.Status)
	}
}

func TestSubmitRerunsOnceAfterLosingARace(t *testing.T) {
	ctx := context.Background()
	f := newFixture()
	w := f.open(t, 100000)
	in := operation(t, w, wager.Bet, 2500, "tx-1", "")
	winner := f.submit(t, in)
	f.store.FailNext("transactions.FindByIdempotencyKey", app.ErrNotFound)
	f.store.FailNext("transactions.FindByExternalID", app.ErrNotFound)

	result, err := f.service.SubmitTransaction(ctx, in)
	if err != nil {
		t.Fatalf("SubmitTransaction: %v", err)
	}
	if !result.IdempotentReplay || result.Transaction != winner {
		t.Fatalf("result = %+v, want the winning row as a replay", result)
	}
	if got := f.wallet(t, w.ID); got.Balance.Amount() != "975.00" || len(f.store.Entries()) != 2 {
		t.Fatalf("wallet = %+v with %d entries, want a single debit", got, len(f.store.Entries()))
	}
	if !slices.Contains(f.metrics.Calls, "ConcurrencyConflict unique_violation") {
		t.Fatalf("metrics = %v, want the conflict counted", f.metrics.Calls)
	}

	f.store.FailNext("transactions.Insert", app.ErrUniqueViolation)
	f.store.FailNext("transactions.Insert", app.ErrUniqueViolation)
	_, err = f.service.SubmitTransaction(ctx, operation(t, w, wager.Bet, 2500, "tx-2", ""))
	if !errors.Is(err, app.ErrUniqueViolation) {
		t.Fatalf("second lost race in a row: err = %v, want %v", err, app.ErrUniqueViolation)
	}
}

func TestSubmitMetricsAndLogs(t *testing.T) {
	ctx := context.Background()
	f := newFixture()
	w := f.open(t, 100000)
	in := operation(t, w, wager.Bet, 2500, "tx-1", "")
	in.Channel = app.ChannelSQS
	bet := f.submit(t, in)
	if _, err := f.service.SubmitTransaction(ctx, in); err != nil {
		t.Fatalf("replay: %v", err)
	}
	rejected := operation(t, w, wager.Bet, 500000, "tx-2", "")
	f.submit(t, rejected)

	lockTimeout := errors.Join(app.ErrTransient, app.ErrLockTimeout)
	f.store.FailNext("wallets.GetForUpdate", lockTimeout)
	if _, err := f.service.SubmitTransaction(ctx, operation(t, w, wager.Bet, 100, "tx-3", "")); !errors.Is(err, app.ErrTransient) {
		t.Fatalf("lock timeout: err = %v, want %v", err, app.ErrTransient)
	}

	for _, want := range []string{
		"TransactionResult internal OPENING PROCESSED ",
		"TransactionResult sqs BET PROCESSED ",
		"ProcessingDuration sqs",
		"Duplicate replay",
		"TransactionResult http BET REJECTED INSUFFICIENT_FUNDS",
		"ConcurrencyConflict lock_timeout",
	} {
		if !slices.Contains(f.metrics.Calls, want) {
			t.Errorf("metrics = %v, want %q among them", f.metrics.Calls, want)
		}
	}

	logs := f.logs.String()
	for _, want := range []string{bet.ID.String(), w.ID.String(), "provider-a", "INSUFFICIENT_FUNDS"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs do not contain %q", want)
		}
	}
	for _, money := range []string{"25.00", "975.00", "1000.00", "5000.00", "2500", "97500"} {
		if strings.Contains(logs, money) {
			t.Errorf("logs contain the monetary value %q:\n%s", money, logs)
		}
	}
}
