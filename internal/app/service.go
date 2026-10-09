package app

import (
	"context"
	"log/slog"
	"time"

	"jungle-gaming-challeng/internal/domain/events"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/wager"
	"jungle-gaming-challeng/internal/domain/wallet"
)

const (
	ChannelHTTP     = "http"
	ChannelSQS      = "sqs"
	ChannelInternal = "internal"
	ChannelWorker   = "worker"

	DuplicateReplay = "replay"
	DuplicateInbox  = "inbox"

	ConflictUniqueViolation = "unique_violation"
	ConflictLockTimeout     = "lock_timeout"

	RetryPendingReference = "pending_reference"
)

type Service struct {
	Tx           TxRunner
	Wallets      WalletRepository
	Transactions TransactionRepository
	Ledger       LedgerRepository
	Outbox       OutboxStore
	Clock        Clock
	Metrics      Metrics
	Logger       *slog.Logger
	ReferenceTTL time.Duration
}

func (s *Service) recordEntry(ctx context.Context, walletID ids.WalletID, transactionID ids.TransactionID, movement wallet.Movement, origin events.Origin) error {
	entry, err := ledger.NewEntry(ledger.Fields{
		WalletID:      walletID,
		TransactionID: transactionID,
		Direction:     movement.Direction,
		Amount:        movement.Amount,
		BalanceBefore: movement.BalanceBefore,
		BalanceAfter:  movement.BalanceAfter,
		WalletVersion: movement.WalletVersion,
	})
	if err != nil {
		return err
	}
	if err := s.Ledger.Insert(ctx, entry); err != nil {
		return err
	}
	event, err := events.NewWalletBalanceChanged(origin, events.WalletBalanceChanged{
		WalletID:      walletID,
		TransactionID: transactionID,
		Direction:     movement.Direction,
		Money:         movement.Amount,
		BalanceBefore: movement.BalanceBefore,
		BalanceAfter:  movement.BalanceAfter,
		WalletVersion: movement.WalletVersion,
	})
	return s.emit(ctx, event, err)
}

func (s *Service) emitOutcome(ctx context.Context, state wager.State, origin events.Origin) error {
	switch state.Status {
	case wager.Processed:
		event, err := events.NewWagerTransactionProcessed(origin, eventTransaction(state), state.ResultBalance)
		return s.emit(ctx, event, err)
	case wager.Rejected:
		event, err := events.NewWagerTransactionRejected(origin, eventTransaction(state), state.FailureCode)
		return s.emit(ctx, event, err)
	case wager.PendingReference:
		event, err := events.NewWagerTransactionPendingReference(origin, eventTransaction(state), state.ReferenceExpiresAt)
		return s.emit(ctx, event, err)
	default:
		return nil
	}
}

func (s *Service) emit(ctx context.Context, event events.Event, err error) error {
	if err != nil {
		return err
	}
	return s.Outbox.Insert(ctx, event)
}

func eventTransaction(state wager.State) events.Transaction {
	return events.Transaction{
		TransactionID:                  state.ID,
		WalletID:                       state.WalletID,
		PlayerID:                       state.PlayerID,
		Kind:                           string(state.Kind),
		Money:                          state.Amount,
		ProviderID:                     state.External.ProviderID,
		ExternalTransactionID:          state.External.ExternalID,
		RoundID:                        state.External.RoundID,
		GameID:                         state.External.GameID,
		ReferenceExternalTransactionID: state.External.ReferenceExternalID,
	}
}
