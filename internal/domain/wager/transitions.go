package wager

import (
	"time"

	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
)

const MaxUnexpectedErrors = 5

func (t *Transaction) MarkProcessed(resultBalance money.Money, referenceID ids.TransactionID) error {
	if !t.isOpen() {
		return ErrInvalidTransition
	}
	if !notNegative(resultBalance) {
		return ErrInvalidTransaction
	}
	if t.isReversal() && referenceID.IsZero() {
		return ErrInvalidTransaction
	}
	t.state.ResultBalance = resultBalance
	t.state.ReferenceID = referenceID
	t.complete(Processed)
	return nil
}

func (t *Transaction) MarkRejected(code failure.Code) error {
	if !t.isOpen() {
		return ErrInvalidTransition
	}
	if !code.IsRejection() {
		return ErrInvalidTransaction
	}
	t.state.FailureCode = code
	t.complete(Rejected)
	return nil
}

func (t *Transaction) MarkPendingReference(expiresAt, nextAttemptAt time.Time) error {
	if t.state.Status != Pending {
		return ErrInvalidTransition
	}
	t.state.Status = PendingReference
	t.state.ReferenceExpiresAt = expiresAt
	t.state.NextAttemptAt = nextAttemptAt
	t.state.UpdatedAt = utcNow()
	return nil
}

func (t *Transaction) MarkFailed() error {
	if t.state.Status != PendingReference {
		return ErrInvalidTransition
	}
	t.state.FailureCode = failure.ProcessingFailed
	t.complete(Failed)
	return nil
}

func (t *Transaction) Reschedule(nextAttemptAt time.Time) error {
	if err := t.retryAt(nextAttemptAt); err != nil {
		return err
	}
	t.state.ErrorCount = 0
	return nil
}

func (t *Transaction) RecordUnexpectedError(nextAttemptAt time.Time) (bool, error) {
	if err := t.retryAt(nextAttemptAt); err != nil {
		return false, err
	}
	t.state.ErrorCount++
	return t.state.ErrorCount >= MaxUnexpectedErrors, nil
}

func (t *Transaction) retryAt(nextAttemptAt time.Time) error {
	if t.state.Status != PendingReference {
		return ErrInvalidTransition
	}
	t.state.Attempts++
	t.state.NextAttemptAt = nextAttemptAt
	t.state.UpdatedAt = utcNow()
	return nil
}

func (t *Transaction) complete(status Status) {
	completedAt := utcNow()
	t.state.Status = status
	t.state.UpdatedAt = completedAt
	t.state.CompletedAt = completedAt
}

func (t *Transaction) isOpen() bool {
	return t.state.Status == Pending || t.state.Status == PendingReference
}

func (t *Transaction) isReversal() bool {
	return t.state.Kind == Refund || t.state.Kind == Rollback
}
