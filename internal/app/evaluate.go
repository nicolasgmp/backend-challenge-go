package app

import (
	"context"
	"errors"

	"jungle-gaming-challeng/internal/domain/events"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
	"jungle-gaming-challeng/internal/domain/wallet"
)

type decision struct {
	waiting     bool
	rejection   failure.Code
	referenceID ids.TransactionID
	movement    *wallet.Movement
}

func rejected(code failure.Code) decision {
	return decision{rejection: code}
}

func (d decision) settled() bool {
	return d.waiting || d.rejection != ""
}

func (s *Service) evaluate(ctx context.Context, w *wallet.Wallet, tx *wager.Transaction) (decision, error) {
	state, owner := tx.State(), w.State()
	if state.PlayerID != owner.PlayerID {
		return rejected(failure.PlayerWalletMismatch), nil
	}
	if state.Amount.Currency() != owner.Balance.Currency() {
		return rejected(failure.CurrencyMismatch), nil
	}

	reference, d, err := s.resolveReference(ctx, tx)
	if err != nil || d.settled() {
		return d, err
	}
	var referenceKind wager.Kind
	if reference != nil {
		referenceKind = reference.State().Kind
		d.referenceID = reference.State().ID
	}

	direction, moves, err := wager.EffectOf(state.Kind, referenceKind)
	if err != nil || !moves {
		return d, err
	}
	movement, err := move(w, direction, state.Amount)
	switch {
	case errors.Is(err, wallet.ErrInsufficientFunds):
		return rejected(insufficientFundsCode(state.Kind)), nil
	case errors.Is(err, wallet.ErrBalanceOverflow):
		return rejected(failure.BalanceOverflow), nil
	case err != nil:
		return decision{}, err
	}
	d.movement = &movement
	return d, nil
}

func (s *Service) resolveReference(ctx context.Context, tx *wager.Transaction) (*wager.Transaction, decision, error) {
	state := tx.State()
	if state.External.ReferenceExternalID.IsZero() {
		return nil, decision{}, nil
	}
	reference, err := s.Transactions.FindByExternalID(ctx, state.External.ProviderID, state.External.ReferenceExternalID)
	if errors.Is(err, ErrNotFound) {
		return nil, decision{waiting: true}, nil
	}
	if err != nil {
		return nil, decision{}, err
	}

	err = wager.ValidateReference(tx, reference)
	var rejection failure.RejectionError
	switch {
	case errors.Is(err, wager.ErrReferencePending):
		return nil, decision{waiting: true}, nil
	case errors.As(err, &rejection):
		return nil, rejected(rejection.Code), nil
	case err != nil:
		return nil, decision{}, err
	}

	if !isReversal(state.Kind) {
		return reference, decision{}, nil
	}
	taken, err := s.Transactions.HasProcessedReversal(ctx, reference.State().ID)
	if err != nil {
		return nil, decision{}, err
	}
	if taken {
		return nil, rejected(failure.ReferenceAlreadyReversed), nil
	}
	return reference, decision{}, nil
}

func (s *Service) applyOutcome(ctx context.Context, w *wallet.Wallet, state wager.State, d decision, origin events.Origin) error {
	if d.movement != nil {
		if err := s.Wallets.UpdateBalance(ctx, w); err != nil {
			return err
		}
		if err := s.recordEntry(ctx, state.WalletID, state.ID, *d.movement, origin); err != nil {
			return err
		}
	}
	return s.emitOutcome(ctx, state, origin)
}

func move(w *wallet.Wallet, direction ledger.Direction, amount money.Money) (wallet.Movement, error) {
	if direction == ledger.Debit {
		return w.Debit(amount)
	}
	return w.Credit(amount)
}

func insufficientFundsCode(kind wager.Kind) failure.Code {
	if isReversal(kind) {
		return failure.ReversalInsufficientFunds
	}
	return failure.InsufficientFunds
}

func isReversal(kind wager.Kind) bool {
	return kind == wager.Refund || kind == wager.Rollback
}
