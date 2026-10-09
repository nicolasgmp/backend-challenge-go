package app

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"

	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
	"jungle-gaming-challeng/internal/domain/wallet"
)

const (
	DefaultLedgerPageSize = 50
	MaxLedgerPageSize     = 200
)

type LedgerPage struct {
	Entries    []ledger.Entry
	NextCursor string
}

type Reconciliation struct {
	WalletID          ids.WalletID
	StoredBalance     money.Money
	CalculatedBalance money.Money
	Difference        money.Money
	Consistent        bool
	CheckedEntries    int
}

func (s *Service) GetWallet(ctx context.Context, id ids.WalletID) (wallet.State, error) {
	w, err := s.Wallets.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return wallet.State{}, failure.InvalidInputError{Code: failure.WalletNotFound}
	}
	if err != nil {
		return wallet.State{}, err
	}
	return w.State(), nil
}

func (s *Service) ListLedger(ctx context.Context, walletID ids.WalletID, cursor string, limit int) (LedgerPage, error) {
	if limit < 1 || limit > MaxLedgerPageSize {
		return LedgerPage{}, failure.InvalidInputError{Code: failure.MalformedRequest}
	}
	afterVersion, err := decodeCursor(cursor)
	if err != nil {
		return LedgerPage{}, err
	}
	if _, err := s.GetWallet(ctx, walletID); err != nil {
		return LedgerPage{}, err
	}

	entries, err := s.Ledger.ListPage(ctx, walletID, afterVersion, limit+1)
	if err != nil {
		return LedgerPage{}, err
	}
	if len(entries) <= limit {
		return LedgerPage{Entries: entries}, nil
	}
	last := entries[limit-1]
	return LedgerPage{Entries: entries[:limit], NextCursor: encodeCursor(last.Fields().WalletVersion)}, nil
}

func (s *Service) GetTransaction(ctx context.Context, caller Caller, id ids.TransactionID) (wager.State, error) {
	tx, err := s.Transactions.Get(ctx, id)
	return visible(caller, tx, err)
}

func (s *Service) GetProviderTransaction(ctx context.Context, caller Caller, providerID ids.ProviderID, externalID ids.ExternalTransactionID) (wager.State, error) {
	if !caller.CanReadProvider(providerID) {
		return wager.State{}, ErrForbidden
	}
	tx, err := s.Transactions.FindByExternalID(ctx, providerID, externalID)
	return visible(caller, tx, err)
}

func (s *Service) ReconcileWallet(ctx context.Context, walletID ids.WalletID) (Reconciliation, error) {
	var result Reconciliation
	err := s.Tx.RunReadOnly(ctx, func(ctx context.Context) error {
		stored, err := s.GetWallet(ctx, walletID)
		if err != nil {
			return err
		}
		totals, err := s.Ledger.Totals(ctx, walletID)
		if err != nil {
			return err
		}
		result, err = reconcile(stored, totals)
		return err
	})
	if err != nil {
		return Reconciliation{}, err
	}

	if !result.Consistent {
		s.Metrics.ReconciliationDivergence()
		s.Logger.WarnContext(ctx, "wallet balance diverges from the ledger", "walletId", walletID.String())
	}
	return result, nil
}

func reconcile(stored wallet.State, totals LedgerTotals) (Reconciliation, error) {
	currency := stored.Balance.Currency()
	credits, err := money.FromMinorUnits(totals.CreditUnits, currency)
	if err != nil {
		return Reconciliation{}, err
	}
	debits, err := money.FromMinorUnits(totals.DebitUnits, currency)
	if err != nil {
		return Reconciliation{}, err
	}
	calculated, err := credits.Sub(debits)
	if err != nil {
		return Reconciliation{}, err
	}
	difference, err := stored.Balance.Sub(calculated)
	if err != nil {
		return Reconciliation{}, err
	}
	return Reconciliation{
		WalletID:          stored.ID,
		StoredBalance:     stored.Balance,
		CalculatedBalance: calculated,
		Difference:        difference,
		Consistent:        difference.IsZero(),
		CheckedEntries:    totals.Entries,
	}, nil
}

func visible(caller Caller, tx *wager.Transaction, err error) (wager.State, error) {
	notFound := failure.InvalidInputError{Code: failure.TransactionNotFound}
	if errors.Is(err, ErrNotFound) {
		return wager.State{}, notFound
	}
	if err != nil {
		return wager.State{}, err
	}
	state := tx.State()
	if !caller.CanReadProvider(state.External.ProviderID) {
		return wager.State{}, notFound
	}
	return state, nil
}

func encodeCursor(version int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(version, 10)))
}

func decodeCursor(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	invalid := failure.InvalidInputError{Code: failure.InvalidCursor}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, invalid
	}
	version, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return 0, invalid
	}
	return version, nil
}
