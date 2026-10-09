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

type OpenWalletInput struct {
	PlayerID       ids.PlayerID
	InitialBalance money.Money
	CorrelationID  string
}

func (s *Service) OpenWallet(ctx context.Context, in OpenWalletInput) (wallet.State, error) {
	w, movement, err := wallet.Open(in.PlayerID, in.InitialBalance)
	if errors.Is(err, wallet.ErrInvalidAmount) {
		return wallet.State{}, failure.InvalidInputError{Code: failure.InvalidMoney}
	}
	if err != nil {
		return wallet.State{}, err
	}

	err = s.Tx.Run(ctx, func(ctx context.Context) error {
		if err := s.Wallets.Insert(ctx, w); err != nil {
			return err
		}
		if movement == nil {
			return nil
		}
		return s.recordOpening(ctx, w.State(), *movement, in.CorrelationID)
	})
	if errors.Is(err, ErrUniqueViolation) {
		return wallet.State{}, ConflictError{Code: ConflictWalletAlreadyExists}
	}
	if err != nil {
		return wallet.State{}, err
	}

	if movement != nil {
		s.Metrics.TransactionResult(ChannelInternal, string(wager.Opening), string(wager.Processed), "")
	}
	s.Logger.InfoContext(ctx, "wallet opened", "walletId", w.State().ID.String())
	return w.State(), nil
}

func (s *Service) recordOpening(ctx context.Context, opened wallet.State, movement wallet.Movement, correlationID string) error {
	opening, err := wager.NewOpening(opened.ID, opened.PlayerID, movement.Amount, correlationID)
	if err != nil {
		return err
	}
	if err := s.Transactions.Insert(ctx, opening); err != nil {
		return err
	}
	origin := events.Origin{CorrelationID: correlationID}
	if err := s.recordEntry(ctx, opened.ID, opening.State().ID, movement, origin); err != nil {
		return err
	}
	return s.emitOutcome(ctx, opening.State(), origin)
}
