package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
	"jungle-gaming-challeng/internal/domain/wallet"
)

type openWalletRequest struct {
	PlayerID       string       `json:"playerId"`
	InitialBalance *money.Money `json:"initialBalance"`
}

type submitRequest struct {
	ProviderID                     string       `json:"providerId"`
	ExternalTransactionID          string       `json:"externalTransactionId"`
	PlayerID                       string       `json:"playerId"`
	WalletID                       string       `json:"walletId"`
	RoundID                        string       `json:"roundId"`
	GameID                         string       `json:"gameId"`
	Kind                           string       `json:"kind"`
	Money                          *money.Money `json:"money"`
	ReferenceExternalTransactionID string       `json:"referenceExternalTransactionId"`
}

type walletResponse struct {
	ID       ids.WalletID `json:"id"`
	PlayerID ids.PlayerID `json:"playerId"`
	Balance  money.Money  `json:"balance"`
	Version  int64        `json:"version"`
}

type submitResponse struct {
	TransactionID      ids.TransactionID `json:"transactionId"`
	Status             wager.Status      `json:"status"`
	Balance            *money.Money      `json:"balance,omitempty"`
	FailureCode        failure.Code      `json:"failureCode,omitempty"`
	ReferenceExpiresAt *time.Time        `json:"referenceExpiresAt,omitempty"`
	IdempotentReplay   bool              `json:"idempotentReplay"`
}

type transactionResponse struct {
	TransactionID                  ids.TransactionID         `json:"transactionId"`
	Kind                           wager.Kind                `json:"kind"`
	Status                         wager.Status              `json:"status"`
	WalletID                       ids.WalletID              `json:"walletId"`
	PlayerID                       ids.PlayerID              `json:"playerId"`
	Money                          money.Money               `json:"money"`
	ProviderID                     ids.ProviderID            `json:"providerId,omitzero"`
	ExternalTransactionID          ids.ExternalTransactionID `json:"externalTransactionId,omitzero"`
	RoundID                        ids.RoundID               `json:"roundId,omitzero"`
	GameID                         ids.GameID                `json:"gameId,omitzero"`
	ReferenceExternalTransactionID ids.ExternalTransactionID `json:"referenceExternalTransactionId,omitzero"`
	Balance                        *money.Money              `json:"balance,omitempty"`
	FailureCode                    failure.Code              `json:"failureCode,omitempty"`
	ReferenceExpiresAt             *time.Time                `json:"referenceExpiresAt,omitempty"`
	CreatedAt                      time.Time                 `json:"createdAt"`
	CompletedAt                    *time.Time                `json:"completedAt,omitempty"`
}

type ledgerEntryResponse struct {
	ID            ids.EntryID       `json:"id"`
	TransactionID ids.TransactionID `json:"transactionId"`
	Direction     ledger.Direction  `json:"direction"`
	Money         money.Money       `json:"money"`
	BalanceBefore money.Money       `json:"balanceBefore"`
	BalanceAfter  money.Money       `json:"balanceAfter"`
	WalletVersion int64             `json:"walletVersion"`
	CreatedAt     time.Time         `json:"createdAt"`
}

type ledgerResponse struct {
	Entries    []ledgerEntryResponse `json:"entries"`
	NextCursor string                `json:"nextCursor,omitempty"`
}

type reconciliationResponse struct {
	WalletID          ids.WalletID `json:"walletId"`
	StoredBalance     money.Money  `json:"storedBalance"`
	CalculatedBalance money.Money  `json:"calculatedBalance"`
	Difference        money.Money  `json:"difference"`
	Consistent        bool         `json:"consistent"`
	CheckedEntries    int          `json:"checkedEntries"`
}

type readinessResponse struct {
	Status string   `json:"status"`
	Failed []string `json:"failed,omitempty"`
}

func (a *API) decode(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, a.deps.MaxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return decodeError(err)
	}
	if decoder.More() {
		return invalidInput(failure.MalformedRequest)
	}
	return nil
}

func decodeError(err error) error {
	for _, moneyErr := range []error{money.ErrInvalidAmount, money.ErrNegativeAmount, money.ErrInvalidCurrency, money.ErrOverflow} {
		if errors.Is(err, moneyErr) {
			return invalidInput(failure.InvalidMoney)
		}
	}
	return invalidInput(failure.MalformedRequest)
}

func invalidInput(code failure.Code) error {
	return failure.InvalidInputError{Code: code}
}

func (req submitRequest) toInput(idempotencyKey string) (app.SubmitInput, error) {
	in, err := app.RawOperation{
		ProviderID:                     req.ProviderID,
		ExternalTransactionID:          req.ExternalTransactionID,
		IdempotencyKey:                 idempotencyKey,
		PlayerID:                       req.PlayerID,
		WalletID:                       req.WalletID,
		RoundID:                        req.RoundID,
		GameID:                         req.GameID,
		Kind:                           req.Kind,
		Money:                          req.Money,
		ReferenceExternalTransactionID: req.ReferenceExternalTransactionID,
	}.Input()
	in.Channel = app.ChannelHTTP
	return in, err
}

func toWalletResponse(state wallet.State) walletResponse {
	return walletResponse{ID: state.ID, PlayerID: state.PlayerID, Balance: state.Balance, Version: state.Version}
}

func toSubmitResponse(result app.SubmitResult) submitResponse {
	state := result.Transaction
	return submitResponse{
		TransactionID:      state.ID,
		Status:             state.Status,
		Balance:            optionalMoney(state.ResultBalance),
		FailureCode:        state.FailureCode,
		ReferenceExpiresAt: pendingDeadline(state),
		IdempotentReplay:   result.IdempotentReplay,
	}
}

func toTransactionResponse(state wager.State) transactionResponse {
	return transactionResponse{
		TransactionID:                  state.ID,
		Kind:                           state.Kind,
		Status:                         state.Status,
		WalletID:                       state.WalletID,
		PlayerID:                       state.PlayerID,
		Money:                          state.Amount,
		ProviderID:                     state.External.ProviderID,
		ExternalTransactionID:          state.External.ExternalID,
		RoundID:                        state.External.RoundID,
		GameID:                         state.External.GameID,
		ReferenceExternalTransactionID: state.External.ReferenceExternalID,
		Balance:                        optionalMoney(state.ResultBalance),
		FailureCode:                    state.FailureCode,
		ReferenceExpiresAt:             pendingDeadline(state),
		CreatedAt:                      state.CreatedAt,
		CompletedAt:                    optionalTime(state.CompletedAt),
	}
}

func toLedgerResponse(page app.LedgerPage) ledgerResponse {
	entries := make([]ledgerEntryResponse, 0, len(page.Entries))
	for _, entry := range page.Entries {
		fields := entry.Fields()
		entries = append(entries, ledgerEntryResponse{
			ID:            entry.ID(),
			TransactionID: fields.TransactionID,
			Direction:     fields.Direction,
			Money:         fields.Amount,
			BalanceBefore: fields.BalanceBefore,
			BalanceAfter:  fields.BalanceAfter,
			WalletVersion: fields.WalletVersion,
			CreatedAt:     entry.CreatedAt(),
		})
	}
	return ledgerResponse{Entries: entries, NextCursor: page.NextCursor}
}

func toReconciliationResponse(result app.Reconciliation) reconciliationResponse {
	return reconciliationResponse{
		WalletID:          result.WalletID,
		StoredBalance:     result.StoredBalance,
		CalculatedBalance: result.CalculatedBalance,
		Difference:        result.Difference,
		Consistent:        result.Consistent,
		CheckedEntries:    result.CheckedEntries,
	}
}

func submitStatus(status wager.Status) int {
	switch status {
	case wager.Processed:
		return http.StatusOK
	case wager.PendingReference:
		return http.StatusAccepted
	case wager.Rejected:
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}

func pendingDeadline(state wager.State) *time.Time {
	if state.Status != wager.PendingReference {
		return nil
	}
	return optionalTime(state.ReferenceExpiresAt)
}

func optionalMoney(m money.Money) *money.Money {
	if m.Currency().Code() == "" {
		return nil
	}
	return &m
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
