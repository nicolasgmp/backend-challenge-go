package app

import (
	"context"
	"errors"

	"jungle-gaming-challeng/internal/domain/events"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
	"jungle-gaming-challeng/internal/domain/wallet"
)

func (s *Service) ResolveDuePendingReferences(ctx context.Context, limit int) (int, error) {
	due, err := s.Transactions.ListDuePendingReferences(ctx, s.Clock.Now(), limit)
	if err != nil {
		return 0, err
	}
	for _, tx := range due {
		state := tx.State()
		if err := s.ResolvePendingReference(ctx, state.ID, state.WalletID); err != nil {
			s.Logger.WarnContext(ctx, "pending reference not resolved",
				"transactionId", state.ID.String(), "walletId", state.WalletID.String(),
				"correlationId", state.CorrelationID, "error", err.Error())
		}
	}
	return len(due), nil
}

func (s *Service) ResolvePendingReference(ctx context.Context, id ids.TransactionID, walletID ids.WalletID) error {
	var resolved wager.State
	err := s.onPending(ctx, id, walletID, func(ctx context.Context, w *wallet.Wallet, tx *wager.Transaction) error {
		d, err := s.evaluate(ctx, w, tx)
		if err != nil {
			return err
		}
		if err := s.settlePending(tx, d, w.State().Balance); err != nil {
			return err
		}
		if err := s.Transactions.Update(ctx, tx); err != nil {
			return err
		}
		resolved = tx.State()
		if !resolved.Status.IsTerminal() {
			return nil
		}
		return s.applyOutcome(ctx, w, resolved, d, events.Origin{CorrelationID: resolved.CorrelationID})
	})

	switch {
	case err == nil:
		s.observeResolved(ctx, resolved)
		return nil
	case errors.Is(err, ErrTransient):
		s.Metrics.Retry(RetryPendingReference)
		return err
	default:
		return errors.Join(err, s.recordUnexpectedError(ctx, id, walletID))
	}
}

func (s *Service) onPending(ctx context.Context, id ids.TransactionID, walletID ids.WalletID, fn func(context.Context, *wallet.Wallet, *wager.Transaction) error) error {
	return s.Tx.Run(ctx, func(ctx context.Context) error {
		w, err := s.Wallets.GetForUpdate(ctx, walletID)
		if err != nil {
			return err
		}
		tx, err := s.Transactions.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		state := tx.State()
		if state.Status != wager.PendingReference || state.NextAttemptAt.After(s.Clock.Now()) {
			return nil
		}
		return fn(ctx, w, tx)
	})
}

func (s *Service) settlePending(tx *wager.Transaction, d decision, balance money.Money) error {
	state, now := tx.State(), s.Clock.Now()
	switch {
	case d.waiting && now.Before(state.ReferenceExpiresAt):
		return tx.Reschedule(wager.NextAttempt(state.Attempts+1, now, state.ReferenceExpiresAt))
	case d.waiting:
		return tx.MarkRejected(failure.ReferenceNotFound)
	case d.rejection != "":
		return tx.MarkRejected(d.rejection)
	default:
		return tx.MarkProcessed(balance, d.referenceID)
	}
}

func (s *Service) recordUnexpectedError(ctx context.Context, id ids.TransactionID, walletID ids.WalletID) error {
	var failed *wager.State
	err := s.onPending(ctx, id, walletID, func(ctx context.Context, _ *wallet.Wallet, tx *wager.Transaction) error {
		state, now := tx.State(), s.Clock.Now()
		limitReached, err := tx.RecordUnexpectedError(wager.NextAttempt(state.Attempts+1, now, state.ReferenceExpiresAt))
		if err != nil {
			return err
		}
		if limitReached {
			if err := tx.MarkFailed(); err != nil {
				return err
			}
			state := tx.State()
			failed = &state
		}
		return s.Transactions.Update(ctx, tx)
	})
	if err == nil && failed != nil {
		s.observeResolved(ctx, *failed)
	}
	return err
}

func (s *Service) observeResolved(ctx context.Context, state wager.State) {
	if !state.Status.IsTerminal() {
		return
	}
	s.Metrics.TransactionResult(ChannelWorker, string(state.Kind), string(state.Status), string(state.FailureCode))
	s.Logger.InfoContext(ctx, "pending reference concluded",
		"transactionId", state.ID.String(),
		"walletId", state.WalletID.String(),
		"providerId", state.External.ProviderID.String(),
		"correlationId", state.CorrelationID,
		"status", string(state.Status),
		"failureCode", string(state.FailureCode),
	)
}
