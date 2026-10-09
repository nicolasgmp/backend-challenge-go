package app_test

import (
	"slices"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/wager"
)

func TestReversalsMoveTheBalance(t *testing.T) {
	tests := []struct {
		name          string
		referenceKind wager.Kind
		reversalKind  wager.Kind
		wantDirection ledger.Direction
		wantBalance   string
	}{
		{"refund of a bet", wager.Bet, wager.Refund, ledger.Credit, "1000.00"},
		{"rollback of a bet", wager.Bet, wager.Rollback, ledger.Credit, "1000.00"},
		{"rollback of a win", wager.Win, wager.Rollback, ledger.Debit, "1000.00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			w := f.open(t, 100000)
			reference := f.submit(t, operation(t, w, tt.referenceKind, 2500, "tx-1", ""))

			reversal := f.submit(t, operation(t, w, tt.reversalKind, 2500, "tx-2", "tx-1"))
			if reversal.Status != wager.Processed || reversal.ReferenceID != reference.ID {
				t.Fatalf("reversal = %+v, want PROCESSED pointing at the reference", reversal)
			}
			if reversal.ResultBalance.Amount() != tt.wantBalance || f.wallet(t, w.ID).Version != 3 {
				t.Fatalf("balance, version = %s, %d, want %s at version 3", reversal.ResultBalance.Amount(), f.wallet(t, w.ID).Version, tt.wantBalance)
			}
			entries := f.store.Entries()
			if last := entries[len(entries)-1].Fields(); len(entries) != 3 || last.Direction != tt.wantDirection {
				t.Fatalf("last entry = %+v, want a new %s entry and the original untouched", last, tt.wantDirection)
			}
		})
	}
}

func TestRollbackOfARefundDebits(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)
	f.submit(t, operation(t, w, wager.Bet, 2500, "tx-1", ""))
	f.submit(t, operation(t, w, wager.Refund, 2500, "tx-2", "tx-1"))

	rollback := f.submit(t, operation(t, w, wager.Rollback, 2500, "tx-3", "tx-2"))
	if rollback.Status != wager.Processed || rollback.ResultBalance.Amount() != "975.00" {
		t.Fatalf("rollback = %+v, want PROCESSED with 975.00", rollback)
	}

	again := f.submit(t, operation(t, w, wager.Refund, 2500, "tx-4", "tx-1"))
	wantRejection(t, again, failure.ReferenceAlreadyReversed)
}

func TestWinWithReference(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)
	bet := f.submit(t, operation(t, w, wager.Bet, 2500, "tx-1", ""))

	win := f.submit(t, operation(t, w, wager.Win, 9000, "tx-2", "tx-1"))
	if win.Status != wager.Processed || win.ReferenceID != bet.ID || win.ResultBalance.Amount() != "1065.00" {
		t.Fatalf("win = %+v, want PROCESSED with another amount than the bet", win)
	}

	refund := f.submit(t, operation(t, w, wager.Refund, 2500, "tx-3", "tx-1"))
	if refund.Status != wager.Processed {
		t.Fatalf("refund after a win of the same bet = %s, want PROCESSED: a win does not take the reversal slot", refund.Status)
	}

	otherRound := operation(t, w, wager.Win, 9000, "tx-4", "tx-1")
	otherRound.External.RoundID = parsed(t, ids.ParseRoundID, "round-2")
	wantRejection(t, f.submit(t, otherRound), failure.ReferenceMismatch)
}

func TestReferenceRejections(t *testing.T) {
	tests := []struct {
		name           string
		referenceKind  wager.Kind
		referenceUnits int64
		reversalKind   wager.Kind
		reversalUnits  int64
		code           failure.Code
	}{
		{"refund of a win", wager.Win, 2500, wager.Refund, 2500, failure.ReferenceKindNotAllowed},
		{"rollback of a loss", wager.Loss, 0, wager.Rollback, 2500, failure.ReferenceKindNotAllowed},
		{"partial refund", wager.Bet, 2500, wager.Refund, 1000, failure.ReferenceMismatch},
		{"refund of a rejected bet", wager.Bet, 500000, wager.Refund, 500000, failure.ReferenceNotProcessed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			w := f.open(t, 100000)
			f.submit(t, operation(t, w, tt.referenceKind, tt.referenceUnits, "tx-1", ""))
			before := f.snapshot(t, w.ID)

			reversal := f.submit(t, operation(t, w, tt.reversalKind, tt.reversalUnits, "tx-2", "tx-1"))
			wantRejection(t, reversal, tt.code)

			after := f.snapshot(t, w.ID)
			if after.balance != before.balance || after.version != before.version || after.entries != before.entries {
				t.Fatalf("state = %+v, want balance, version and ledger unchanged: %+v", after, before)
			}
			if got := f.eventTypes()[before.events:]; !slices.Equal(got, []string{"WagerTransactionRejected"}) {
				t.Fatalf("events = %v, want only the rejection", got)
			}
		})
	}
}

func TestOneProcessedReversalPerReference(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)
	f.submit(t, operation(t, w, wager.Bet, 2500, "tx-1", ""))
	if refund := f.submit(t, operation(t, w, wager.Refund, 2500, "tx-2", "tx-1")); refund.Status != wager.Processed {
		t.Fatalf("refund = %s, want PROCESSED", refund.Status)
	}

	wantRejection(t, f.submit(t, operation(t, w, wager.Rollback, 2500, "tx-3", "tx-1")), failure.ReferenceAlreadyReversed)
	wantRejection(t, f.submit(t, operation(t, w, wager.Refund, 2500, "tx-4", "tx-1")), failure.ReferenceAlreadyReversed)

	if got := f.wallet(t, w.ID); got.Balance.Amount() != "1000.00" || len(f.store.Entries()) != 3 {
		t.Fatalf("wallet = %+v with %d entries, want the bet refunded exactly once", got, len(f.store.Entries()))
	}
}

func TestReversalWithoutFunds(t *testing.T) {
	f := newFixture()
	w := f.open(t, 0)
	f.submit(t, operation(t, w, wager.Win, 5000, "tx-win", ""))
	f.submit(t, operation(t, w, wager.Bet, 2000, "tx-bet", ""))

	rollback := f.submit(t, operation(t, w, wager.Rollback, 5000, "tx-rollback-1", "tx-win"))
	wantRejection(t, rollback, failure.ReversalInsufficientFunds)
	if got := f.wallet(t, w.ID).Balance.Amount(); got != "30.00" {
		t.Fatalf("balance = %s, want 30.00", got)
	}

	f.submit(t, operation(t, w, wager.Win, 10000, "tx-win-2", ""))
	retry := f.submit(t, operation(t, w, wager.Rollback, 5000, "tx-rollback-2", "tx-win"))
	if retry.Status != wager.Processed || retry.ResultBalance.Amount() != "80.00" {
		t.Fatalf("second rollback = %+v, want PROCESSED with 80.00: a rejected reversal does not take the slot", retry)
	}
}

func TestReversalBeforeItsReferenceWaits(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)
	before := f.snapshot(t, w.ID)
	in := operation(t, w, wager.Refund, 2500, "tx-refund", "tx-bet")

	refund := f.submit(t, in)
	if refund.Status != wager.PendingReference || !refund.CompletedAt.IsZero() {
		t.Fatalf("refund = %+v, want PENDING_REFERENCE", refund)
	}
	if !refund.ReferenceExpiresAt.Equal(start.Add(ttl)) || !refund.NextAttemptAt.Equal(start.Add(time.Second)) {
		t.Fatalf("deadline, next attempt = %s, %s, want %s and one second from now", refund.ReferenceExpiresAt, refund.NextAttemptAt, start.Add(ttl))
	}

	after := f.snapshot(t, w.ID)
	if after.balance != before.balance || after.version != before.version || after.entries != before.entries {
		t.Fatalf("state = %+v, want no movement while waiting", after)
	}
	if got := f.eventTypes()[before.events:]; !slices.Equal(got, []string{"WagerTransactionPendingReference"}) {
		t.Fatalf("events = %v, want only the pending reference", got)
	}

	rollback := f.submit(t, operation(t, w, wager.Rollback, 2500, "tx-rollback", "tx-refund"))
	if rollback.Status != wager.PendingReference {
		t.Fatalf("rollback of a pending refund = %s, want PENDING_REFERENCE", rollback.Status)
	}

	replay, err := f.service.SubmitTransaction(t.Context(), in)
	if err != nil || !replay.IdempotentReplay || replay.Transaction.Status != wager.PendingReference {
		t.Fatalf("replay = %+v, %v, want the pending transaction as a replay", replay, err)
	}
	if got := len(f.store.TransactionStates()); got != after.transactions+1 {
		t.Fatalf("transactions = %d, want no new pending row from the replay", got)
	}
}
