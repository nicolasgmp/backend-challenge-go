package app

import (
	"context"
	"errors"
	"time"

	"jungle-gaming-challeng/internal/app/payloadhash"
	"jungle-gaming-challeng/internal/domain/events"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
)

type SubmitInput struct {
	Channel       string
	Kind          wager.Kind
	WalletID      ids.WalletID
	PlayerID      ids.PlayerID
	Money         money.Money
	External      wager.External
	CorrelationID string
	CausationID   string
}

type SubmitResult struct {
	Transaction      wager.State
	IdempotentReplay bool
}

func (s *Service) SubmitTransaction(ctx context.Context, in SubmitInput) (SubmitResult, error) {
	started := s.Clock.Now()

	hash, err := payloadhash.Sum(in.hashFields())
	if err != nil {
		return SubmitResult{}, failure.InvalidInputError{Code: failure.InvalidMoney}
	}
	in.External.PayloadHash = hash
	if _, err := in.newTransaction(); err != nil {
		return SubmitResult{}, err
	}

	result, err := s.submit(ctx, in)
	if errors.Is(err, ErrUniqueViolation) {
		s.Metrics.ConcurrencyConflict(ConflictUniqueViolation)
	}
	s.observeSubmit(ctx, in, result, err, s.Clock.Now().Sub(started))
	if err != nil {
		return SubmitResult{}, err
	}
	return result, nil
}

func (s *Service) submit(ctx context.Context, in SubmitInput) (SubmitResult, error) {
	var result SubmitResult
	err := s.Tx.Run(ctx, func(ctx context.Context) error {
		w, err := s.Wallets.GetForUpdate(ctx, in.WalletID)
		if errors.Is(err, ErrNotFound) {
			return failure.InvalidInputError{Code: failure.WalletNotFound}
		}
		if err != nil {
			return err
		}

		replay, err := s.findReplay(ctx, in.External)
		if err != nil {
			return err
		}
		if replay != nil {
			result = SubmitResult{Transaction: replay.State(), IdempotentReplay: true}
			return nil
		}

		tx, err := in.newTransaction()
		if err != nil {
			return err
		}
		d, err := s.evaluate(ctx, w, tx)
		if err != nil {
			return err
		}
		if err := s.settleNew(tx, d, w.State().Balance); err != nil {
			return err
		}
		if err := s.Transactions.Insert(ctx, tx); err != nil {
			return err
		}
		origin := events.Origin{CorrelationID: in.CorrelationID, CausationID: in.CausationID}
		if err := s.applyOutcome(ctx, w, tx.State(), d, origin); err != nil {
			return err
		}
		result = SubmitResult{Transaction: tx.State()}
		return nil
	})
	return result, err
}

func (s *Service) findReplay(ctx context.Context, external wager.External) (*wager.Transaction, error) {
	byKey, err := found(s.Transactions.FindByIdempotencyKey(ctx, external.ProviderID, external.IdempotencyKey))
	if err != nil {
		return nil, err
	}
	byExternalID, err := found(s.Transactions.FindByExternalID(ctx, external.ProviderID, external.ExternalID))
	if err != nil {
		return nil, err
	}
	return decideReplay(byKey, byExternalID, external.PayloadHash)
}

func decideReplay(byKey, byExternalID *wager.Transaction, payloadHash string) (*wager.Transaction, error) {
	switch {
	case byKey != nil && byKey.State().External.PayloadHash == payloadHash:
		return byKey, nil
	case byKey != nil:
		return nil, ConflictError{Code: ConflictIdempotencyKeyReused}
	case byExternalID != nil:
		return nil, ConflictError{Code: ConflictTransactionAlreadyRegistered}
	default:
		return nil, nil
	}
}

func (s *Service) settleNew(tx *wager.Transaction, d decision, balance money.Money) error {
	switch {
	case d.waiting:
		now := s.Clock.Now()
		expiresAt := now.Add(s.ReferenceTTL)
		return tx.MarkPendingReference(expiresAt, wager.NextAttempt(0, now, expiresAt))
	case d.rejection != "":
		return tx.MarkRejected(d.rejection)
	default:
		return tx.MarkProcessed(balance, d.referenceID)
	}
}

func (s *Service) observeSubmit(ctx context.Context, in SubmitInput, result SubmitResult, err error, elapsed time.Duration) {
	s.Metrics.ProcessingDuration(in.Channel, elapsed)
	if errors.Is(err, ErrLockTimeout) {
		s.Metrics.ConcurrencyConflict(ConflictLockTimeout)
	}
	if err != nil {
		return
	}

	state := result.Transaction
	if result.IdempotentReplay {
		s.Metrics.Duplicate(DuplicateReplay)
	} else {
		s.Metrics.TransactionResult(in.Channel, string(state.Kind), string(state.Status), string(state.FailureCode))
	}
	s.Logger.InfoContext(ctx, "wager transaction handled",
		"transactionId", state.ID.String(),
		"walletId", state.WalletID.String(),
		"providerId", state.External.ProviderID.String(),
		"kind", string(state.Kind),
		"status", string(state.Status),
		"failureCode", string(state.FailureCode),
		"idempotentReplay", result.IdempotentReplay,
	)
}

func (in SubmitInput) newTransaction() (*wager.Transaction, error) {
	return wager.NewExternal(in.Kind, in.WalletID, in.PlayerID, in.Money, in.External, in.CorrelationID)
}

func (in SubmitInput) hashFields() payloadhash.Fields {
	return payloadhash.Fields{
		ExternalTransactionID:          in.External.ExternalID,
		GameID:                         in.External.GameID,
		Kind:                           in.Kind,
		Money:                          in.Money,
		PlayerID:                       in.PlayerID,
		ProviderID:                     in.External.ProviderID,
		ReferenceExternalTransactionID: in.External.ReferenceExternalID,
		RoundID:                        in.External.RoundID,
		WalletID:                       in.WalletID,
	}
}

func found(tx *wager.Transaction, err error) (*wager.Transaction, error) {
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return tx, err
}
