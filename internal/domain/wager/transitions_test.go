package wager_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
)

func move(t *testing.T, tx *wager.Transaction, to wager.Status) error {
	t.Helper()

	switch to {
	case wager.PendingReference:
		return tx.MarkPendingReference(longAgo.Add(5*time.Minute), longAgo.Add(time.Second))
	case wager.Processed:
		return tx.MarkProcessed(amount(t, 100000, "BRL"), referenceID(t))
	case wager.Rejected:
		return tx.MarkRejected(failure.ReferenceMismatch)
	default:
		return tx.MarkFailed()
	}
}

func TestTransitions(t *testing.T) {
	allowed := map[wager.Status][]wager.Status{
		wager.Pending:          {wager.PendingReference, wager.Processed, wager.Rejected},
		wager.PendingReference: {wager.Processed, wager.Rejected, wager.Failed},
	}
	origins := []wager.Status{wager.Pending, wager.PendingReference, wager.Processed, wager.Rejected, wager.Failed}
	targets := []wager.Status{wager.PendingReference, wager.Processed, wager.Rejected, wager.Failed}

	for _, from := range origins {
		for _, to := range targets {
			t.Run(string(from)+" to "+string(to), func(t *testing.T) {
				tx := inStatus(t, from)
				before := tx.State()

				var wantErr error
				if !slices.Contains(allowed[from], to) {
					wantErr = wager.ErrInvalidTransition
				}

				err := move(t, tx, to)
				if !errors.Is(err, wantErr) {
					t.Fatalf("err = %v, want %v", err, wantErr)
				}
				if err != nil && tx.State() != before {
					t.Fatalf("State() = %+v, want it unchanged: %+v", tx.State(), before)
				}
				if err == nil && tx.State().Status != to {
					t.Fatalf("Status = %s, want %s", tx.State().Status, to)
				}
			})
		}
	}
}

func TestMarkProcessed(t *testing.T) {
	balance := amount(t, 100000, "BRL")

	for _, from := range []wager.Status{wager.Pending, wager.PendingReference} {
		t.Run(string(from), func(t *testing.T) {
			tx := inStatus(t, from)

			if err := tx.MarkProcessed(balance, referenceID(t)); err != nil {
				t.Fatalf("MarkProcessed: %v", err)
			}

			state := tx.State()
			if !state.ResultBalance.Equal(balance) || state.ReferenceID != referenceID(t) {
				t.Fatalf("State() = %+v, want the result balance and the resolved reference", state)
			}
			if state.CompletedAt.IsZero() || state.UpdatedAt != state.CompletedAt || state.FailureCode != "" {
				t.Fatalf("State() = %+v, want a completion time and no failure code", state)
			}
		})
	}
}

func TestMarkProcessedRejects(t *testing.T) {
	tests := []struct {
		name        string
		balance     money.Money
		referenceID ids.TransactionID
	}{
		{"uninitialized balance", money.Money{}, referenceID(t)},
		{"negative balance", amount(t, -1, "BRL"), referenceID(t)},
		{"reversal without resolved reference", amount(t, 100000, "BRL"), ids.TransactionID{}},
	}

	for _, tt := range tests {
		for _, kind := range []wager.Kind{wager.Refund, wager.Rollback} {
			t.Run(tt.name+" on "+string(kind), func(t *testing.T) {
				tx := created(t, argsFor(t, kind))
				before := tx.State()

				if err := tx.MarkProcessed(tt.balance, tt.referenceID); !errors.Is(err, wager.ErrInvalidTransaction) {
					t.Fatalf("err = %v, want %v", err, wager.ErrInvalidTransaction)
				}
				if tx.State() != before {
					t.Fatalf("State() = %+v, want it unchanged: %+v", tx.State(), before)
				}
			})
		}
	}
}

func TestMarkProcessedWithoutReference(t *testing.T) {
	tx := created(t, betArgs(t))

	if err := tx.MarkProcessed(amount(t, 0, "BRL"), ids.TransactionID{}); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}

	state := tx.State()
	if !state.ReferenceID.IsZero() || !state.ResultBalance.IsZero() {
		t.Fatalf("State() = %+v, want no reference and a result balance of 0.00", state)
	}
}

func TestMarkRejected(t *testing.T) {
	tx := inStatus(t, wager.PendingReference)

	if err := tx.MarkRejected(failure.ReferenceNotFound); err != nil {
		t.Fatalf("MarkRejected: %v", err)
	}

	state := tx.State()
	if state.FailureCode != failure.ReferenceNotFound || state.CompletedAt.IsZero() {
		t.Fatalf("State() = %+v, want the failure code and a completion time", state)
	}
}

func TestMarkRejectedRejects(t *testing.T) {
	codes := []failure.Code{"", "NOT_A_CODE", failure.InvalidMoney, failure.ProcessingFailed}

	for _, code := range codes {
		t.Run(string(code), func(t *testing.T) {
			tx := inStatus(t, wager.Pending)
			before := tx.State()

			if err := tx.MarkRejected(code); !errors.Is(err, wager.ErrInvalidTransaction) {
				t.Fatalf("err = %v, want %v", err, wager.ErrInvalidTransaction)
			}
			if tx.State() != before {
				t.Fatalf("State() = %+v, want it unchanged: %+v", tx.State(), before)
			}
		})
	}
}

func TestMarkPendingReference(t *testing.T) {
	expiresAt := longAgo.Add(5 * time.Minute)
	nextAttemptAt := longAgo.Add(time.Second)
	tx := inStatus(t, wager.Pending)

	if err := tx.MarkPendingReference(expiresAt, nextAttemptAt); err != nil {
		t.Fatalf("MarkPendingReference: %v", err)
	}

	state := tx.State()
	if !state.ReferenceExpiresAt.Equal(expiresAt) || !state.NextAttemptAt.Equal(nextAttemptAt) {
		t.Fatalf("State() = %+v, want the deadline and the next attempt", state)
	}
	if !state.CompletedAt.IsZero() || state.Attempts != 0 {
		t.Fatalf("State() = %+v, want no completion time and no attempts", state)
	}
}

func TestMarkPendingReferenceRejectsMissingTimes(t *testing.T) {
	tests := []struct {
		name          string
		expiresAt     time.Time
		nextAttemptAt time.Time
	}{
		{"missing deadline", time.Time{}, longAgo},
		{"missing next attempt", longAgo, time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := inStatus(t, wager.Pending)

			err := tx.MarkPendingReference(tt.expiresAt, tt.nextAttemptAt)
			if !errors.Is(err, wager.ErrInvalidTransaction) {
				t.Fatalf("err = %v, want %v", err, wager.ErrInvalidTransaction)
			}
			if tx.State().Status != wager.Pending {
				t.Fatalf("Status = %s, want %s", tx.State().Status, wager.Pending)
			}
		})
	}
}

func TestMarkFailed(t *testing.T) {
	tx := inStatus(t, wager.PendingReference)

	if err := tx.MarkFailed(); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	state := tx.State()
	if state.FailureCode != failure.ProcessingFailed || state.CompletedAt.IsZero() {
		t.Fatalf("State() = %+v, want PROCESSING_FAILED and a completion time", state)
	}
}

func TestRetryCounters(t *testing.T) {
	tx := inStatus(t, wager.PendingReference)
	nextAttemptAt := longAgo.Add(time.Minute)

	for i := 1; i < wager.MaxUnexpectedErrors; i++ {
		reached, err := tx.RecordUnexpectedError(nextAttemptAt)
		if err != nil || reached {
			t.Fatalf("error %d: reached, err = %t, %v, want false, nil", i, reached, err)
		}
	}

	if err := tx.Reschedule(nextAttemptAt); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}
	state := tx.State()
	if state.ErrorCount != 0 || state.Attempts != wager.MaxUnexpectedErrors || !state.NextAttemptAt.Equal(nextAttemptAt) {
		t.Fatalf("State() = %+v, want no errors, %d attempts and the new next attempt", state, wager.MaxUnexpectedErrors)
	}

	for i := 1; i < wager.MaxUnexpectedErrors; i++ {
		if reached, _ := tx.RecordUnexpectedError(nextAttemptAt); reached {
			t.Fatalf("error %d after the reset reached the limit", i)
		}
	}
	reached, err := tx.RecordUnexpectedError(nextAttemptAt)
	if err != nil || !reached {
		t.Fatalf("last error: reached, err = %t, %v, want true, nil", reached, err)
	}
}

func TestRetryRejects(t *testing.T) {
	tests := []struct {
		name          string
		status        wager.Status
		nextAttemptAt time.Time
		wantErr       error
	}{
		{"pending", wager.Pending, longAgo, wager.ErrInvalidTransition},
		{"processed", wager.Processed, longAgo, wager.ErrInvalidTransition},
		{"missing next attempt", wager.PendingReference, time.Time{}, wager.ErrInvalidTransaction},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := inStatus(t, tt.status)
			before := tx.State()

			if err := tx.Reschedule(tt.nextAttemptAt); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Reschedule: err = %v, want %v", err, tt.wantErr)
			}
			if _, err := tx.RecordUnexpectedError(tt.nextAttemptAt); !errors.Is(err, tt.wantErr) {
				t.Fatalf("RecordUnexpectedError: err = %v, want %v", err, tt.wantErr)
			}
			if tx.State() != before {
				t.Fatalf("State() = %+v, want it unchanged: %+v", tx.State(), before)
			}
		})
	}
}
