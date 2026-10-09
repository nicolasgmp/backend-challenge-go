package events

import (
	"time"

	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
)

type Transaction struct {
	TransactionID                  ids.TransactionID         `json:"transactionId"`
	WalletID                       ids.WalletID              `json:"walletId"`
	PlayerID                       ids.PlayerID              `json:"playerId"`
	Kind                           string                    `json:"kind"`
	Money                          money.Money               `json:"money"`
	ProviderID                     ids.ProviderID            `json:"providerId,omitzero"`
	ExternalTransactionID          ids.ExternalTransactionID `json:"externalTransactionId,omitzero"`
	RoundID                        ids.RoundID               `json:"roundId,omitzero"`
	GameID                         ids.GameID                `json:"gameId,omitzero"`
	ReferenceExternalTransactionID ids.ExternalTransactionID `json:"referenceExternalTransactionId,omitzero"`
}

type WagerTransactionProcessed struct {
	Transaction
	Balance money.Money `json:"balance"`
}

type WagerTransactionRejected struct {
	Transaction
	FailureCode failure.Code `json:"failureCode"`
}

type WagerTransactionPendingReference struct {
	Transaction
	ReferenceExpiresAt time.Time `json:"referenceExpiresAt"`
}

func NewWagerTransactionProcessed(origin Origin, tx Transaction, balance money.Money) (Event, error) {
	if !tx.valid() || !notNegative(balance) {
		return Event{}, ErrInvalidEvent
	}
	return tx.event(TypeWagerTransactionProcessed, origin, WagerTransactionProcessed{tx, balance})
}

func NewWagerTransactionRejected(origin Origin, tx Transaction, code failure.Code) (Event, error) {
	if !tx.valid() || !code.IsRejection() {
		return Event{}, ErrInvalidEvent
	}
	return tx.event(TypeWagerTransactionRejected, origin, WagerTransactionRejected{tx, code})
}

func NewWagerTransactionPendingReference(origin Origin, tx Transaction, expiresAt time.Time) (Event, error) {
	if !tx.valid() || tx.ReferenceExternalTransactionID.IsZero() || expiresAt.IsZero() {
		return Event{}, ErrInvalidEvent
	}
	return tx.event(TypeWagerTransactionPendingReference, origin, WagerTransactionPendingReference{tx, expiresAt.UTC()})
}

func (t Transaction) valid() bool {
	if t.TransactionID.IsZero() || t.WalletID.IsZero() || t.PlayerID.IsZero() {
		return false
	}
	return t.Kind != "" && notNegative(t.Money)
}

func (t Transaction) event(eventType string, origin Origin, data any) (Event, error) {
	return newEvent(eventType, t.TransactionID.String(), t.WalletID, origin, data)
}
