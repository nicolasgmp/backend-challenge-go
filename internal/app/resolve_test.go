package app_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/wager"
	"jungle-gaming-challeng/internal/domain/wallet"
)

func (f fixture) pendingRefund(t *testing.T, units int64) (wallet.State, wager.State) {
	t.Helper()

	w := f.open(t, units)
	refund := f.submit(t, operation(t, w, wager.Refund, 2500, "tx-refund", "tx-bet"))
	if refund.Status != wager.PendingReference {
		t.Fatalf("refund = %s, want PENDING_REFERENCE", refund.Status)
	}
	return w, refund
}

func (f fixture) resolveDue(t *testing.T) int {
	t.Helper()

	resolved, err := f.service.ResolveDuePendingReferences(t.Context(), 10)
	if err != nil {
		t.Fatalf("ResolveDuePendingReferences: %v", err)
	}
	return resolved
}

func (f fixture) transaction(t *testing.T, id ids.TransactionID) wager.State {
	t.Helper()

	state, err := f.service.GetTransaction(t.Context(), app.Caller{}, id)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	return state
}

func TestPendingReferenceIsNotTouchedBeforeItsTime(t *testing.T) {
	f := newFixture()
	_, refund := f.pendingRefund(t, 100000)

	if got := f.resolveDue(t); got != 0 {
		t.Fatalf("resolved %d pending references before the next attempt time", got)
	}
	if got := f.transaction(t, refund.ID); got != refund {
		t.Fatalf("refund = %+v, want it untouched", got)
	}
}

func TestPendingReferenceResolvesWhenTheReferenceArrives(t *testing.T) {
	f := newFixture()
	w, refund := f.pendingRefund(t, 100000)
	bet := f.submit(t, operation(t, w, wager.Bet, 2500, "tx-bet", ""))
	events := len(f.store.Events())
	f.clock.Current = start.Add(time.Second)
	f.store.Calls = nil

	if got := f.resolveDue(t); got != 1 {
		t.Fatalf("resolved %d pending references, want 1", got)
	}
	wantFirst := []string{"transactions.ListDuePendingReferences", "wallets.GetForUpdate", "transactions.GetForUpdate"}
	if len(f.store.Calls) < 3 || !slices.Equal(f.store.Calls[:3], wantFirst) {
		t.Fatalf("calls = %v, want the wallet locked first and then the pending row", f.store.Calls)
	}

	resolved := f.transaction(t, refund.ID)
	if resolved.Status != wager.Processed || resolved.ReferenceID != bet.ID || resolved.ResultBalance.Amount() != "1000.00" {
		t.Fatalf("refund = %+v, want PROCESSED against the bet with 1000.00", resolved)
	}
	if got := f.wallet(t, w.ID); got.Balance.Amount() != "1000.00" || got.Version != 3 {
		t.Fatalf("wallet = %+v, want 1000.00 at version 3", got)
	}
	if got := f.eventTypes()[events:]; !slices.Equal(got, []string{"WalletBalanceChanged", "WagerTransactionProcessed"}) {
		t.Fatalf("events of the resolution = %v", got)
	}
	last := f.store.Events()[len(f.store.Events())-1].Header()
	if last.CorrelationID != "correlation-tx-refund" {
		t.Fatalf("correlation id = %q, want the one of the original request", last.CorrelationID)
	}
	if !slices.Contains(f.metrics.Calls, "TransactionResult worker REFUND PROCESSED ") {
		t.Fatalf("metrics = %v, want the worker result", f.metrics.Calls)
	}
}

func TestPendingReferenceIsRescheduledWithBackoff(t *testing.T) {
	f := newFixture()
	_, refund := f.pendingRefund(t, 100000)
	events := len(f.store.Events())

	now := start
	for attempt, wait := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		now = now.Add(wait)
		f.clock.Current = now
		f.resolveDue(t)

		got := f.transaction(t, refund.ID)
		wantNext := now.Add(2 * wait)
		if got.Status != wager.PendingReference || got.Attempts != attempt+1 || !got.NextAttemptAt.Equal(wantNext) {
			t.Fatalf("attempt %d: status, attempts, next = %s, %d, %s, want PENDING_REFERENCE, %d, %s",
				attempt+1, got.Status, got.Attempts, got.NextAttemptAt, attempt+1, wantNext)
		}
	}
	if len(f.store.Events()) != events {
		t.Fatal("rescheduling produced events")
	}
}

func TestPendingReferenceExpires(t *testing.T) {
	f := newFixture()
	w, refund := f.pendingRefund(t, 100000)
	events := len(f.store.Events())
	f.clock.Current = start.Add(ttl)

	f.resolveDue(t)

	wantRejection(t, f.transaction(t, refund.ID), failure.ReferenceNotFound)
	if got := f.eventTypes()[events:]; !slices.Equal(got, []string{"WagerTransactionRejected"}) {
		t.Fatalf("events = %v, want only the rejection", got)
	}
	if got := f.wallet(t, w.ID); got.Balance.Amount() != "1000.00" || got.Version != 1 {
		t.Fatalf("wallet = %+v, want it untouched", got)
	}
}

func TestPendingReferenceLastAttemptFindsTheReference(t *testing.T) {
	f := newFixture()
	w, refund := f.pendingRefund(t, 100000)
	f.submit(t, operation(t, w, wager.Bet, 2500, "tx-bet", ""))
	f.clock.Current = start.Add(ttl + time.Hour)

	f.resolveDue(t)

	if got := f.transaction(t, refund.ID); got.Status != wager.Processed {
		t.Fatalf("refund = %s, want PROCESSED: the last attempt found the reference", got.Status)
	}
}

func TestPendingReferenceWithARejectedReference(t *testing.T) {
	f := newFixture()
	w, refund := f.pendingRefund(t, 1000)
	f.submit(t, operation(t, w, wager.Bet, 2500, "tx-bet", ""))
	f.clock.Current = start.Add(time.Second)

	f.resolveDue(t)

	wantRejection(t, f.transaction(t, refund.ID), failure.ReferenceNotProcessed)
}

func TestTwoPendingReversalsOfTheSameReference(t *testing.T) {
	f := newFixture()
	w, refund := f.pendingRefund(t, 100000)
	rollback := f.submit(t, operation(t, w, wager.Rollback, 2500, "tx-rollback", "tx-bet"))
	f.submit(t, operation(t, w, wager.Bet, 2500, "tx-bet", ""))
	f.clock.Current = start.Add(time.Second)

	f.resolveDue(t)

	statuses := []wager.Status{f.transaction(t, refund.ID).Status, f.transaction(t, rollback.ID).Status}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []wager.Status{wager.Processed, wager.Rejected}) {
		t.Fatalf("statuses = %v, want exactly one PROCESSED and one REJECTED", statuses)
	}
	if got := f.wallet(t, w.ID).Balance.Amount(); got != "1000.00" {
		t.Fatalf("balance = %s, want the bet returned exactly once", got)
	}
}

func TestPendingReferenceFailsAfterFiveUnexpectedErrors(t *testing.T) {
	f := newFixture()
	_, refund := f.pendingRefund(t, 100000)
	events := len(f.store.Events())

	now := start
	for attempt := 1; attempt <= wager.MaxUnexpectedErrors; attempt++ {
		now = now.Add(time.Minute)
		f.clock.Current = now
		f.store.FailNext("transactions.FindByExternalID", errBoom)

		err := f.service.ResolvePendingReference(t.Context(), refund.ID, refund.WalletID)
		if !errors.Is(err, errBoom) {
			t.Fatalf("attempt %d: err = %v, want %v", attempt, err, errBoom)
		}
		got := f.transaction(t, refund.ID)
		if attempt < wager.MaxUnexpectedErrors && (got.Status != wager.PendingReference || got.ErrorCount != attempt) {
			t.Fatalf("attempt %d: status, errors = %s, %d, want PENDING_REFERENCE, %d", attempt, got.Status, got.ErrorCount, attempt)
		}
	}

	failed := f.transaction(t, refund.ID)
	if failed.Status != wager.Failed || failed.FailureCode != failure.ProcessingFailed {
		t.Fatalf("refund = %+v, want FAILED with PROCESSING_FAILED", failed)
	}
	if len(f.store.Events()) != events {
		t.Fatalf("events = %v, want none for a failed transaction", f.eventTypes()[events:])
	}
	if !slices.Contains(f.metrics.Calls, "TransactionResult worker REFUND FAILED PROCESSING_FAILED") {
		t.Fatalf("metrics = %v, want the failure counted", f.metrics.Calls)
	}
}

func TestPendingReferenceTransientErrorDoesNotCount(t *testing.T) {
	f := newFixture()
	_, refund := f.pendingRefund(t, 100000)
	f.clock.Current = start.Add(time.Second)
	f.store.FailNext("transactions.FindByExternalID", app.ErrTransient)

	err := f.service.ResolvePendingReference(t.Context(), refund.ID, refund.WalletID)
	if !errors.Is(err, app.ErrTransient) {
		t.Fatalf("err = %v, want %v", err, app.ErrTransient)
	}
	if got := f.transaction(t, refund.ID); got != refund {
		t.Fatalf("refund = %+v, want it untouched by a transient error", got)
	}
	if !slices.Contains(f.metrics.Calls, "Retry pending_reference") {
		t.Fatalf("metrics = %v, want the retry counted", f.metrics.Calls)
	}

	f.resolveDue(t)
	if got := f.transaction(t, refund.ID); got.Attempts != 1 || got.ErrorCount != 0 {
		t.Fatalf("attempts, errors = %d, %d, want the next cycle to try again", got.Attempts, got.ErrorCount)
	}
}

func TestOneFailingPendingReferenceDoesNotStopTheOthers(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)
	first := f.submit(t, operation(t, w, wager.Refund, 2500, "tx-refund-1", "tx-bet-1"))
	second := f.submit(t, operation(t, w, wager.Refund, 2500, "tx-refund-2", "tx-bet-2"))
	f.submit(t, operation(t, w, wager.Bet, 2500, "tx-bet-1", ""))
	f.submit(t, operation(t, w, wager.Bet, 2500, "tx-bet-2", ""))
	f.clock.Current = start.Add(time.Second)
	f.store.FailNext("transactions.FindByExternalID", errBoom)

	if got := f.resolveDue(t); got != 2 {
		t.Fatalf("handled %d pending references, want 2", got)
	}

	statuses := []wager.Status{f.transaction(t, first.ID).Status, f.transaction(t, second.ID).Status}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []wager.Status{wager.PendingReference, wager.Processed}) {
		t.Fatalf("statuses = %v, want one resolved and the failing one still pending", statuses)
	}
}

func TestPendingReferenceIsResolvedOnlyOnce(t *testing.T) {
	f := newFixture()
	w, refund := f.pendingRefund(t, 100000)
	f.submit(t, operation(t, w, wager.Bet, 2500, "tx-bet", ""))
	f.clock.Current = start.Add(time.Second)

	for attempt := 1; attempt <= 3; attempt++ {
		if err := f.service.ResolvePendingReference(t.Context(), refund.ID, refund.WalletID); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}

	if got := f.wallet(t, w.ID); got.Balance.Amount() != "1000.00" || got.Version != 3 || len(f.store.Entries()) != 3 {
		t.Fatalf("wallet = %+v with %d entries, want the refund applied exactly once", got, len(f.store.Entries()))
	}
}

func TestReferenceOfAnotherProviderIsNotFound(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)
	f.submit(t, operation(t, w, wager.Bet, 2500, "tx-bet", ""))

	refund := operation(t, w, wager.Refund, 2500, "tx-refund", "tx-bet")
	refund.External.ProviderID = parsed(t, ids.ParseProviderID, "provider-b")

	if got := f.submit(t, refund); got.Status != wager.PendingReference {
		t.Fatalf("refund of another provider's bet = %s, want PENDING_REFERENCE: the reference does not exist for this provider", got.Status)
	}
}
