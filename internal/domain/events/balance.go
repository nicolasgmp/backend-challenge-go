package events

import (
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/money"
)

type WalletBalanceChanged struct {
	WalletID      ids.WalletID      `json:"walletId"`
	TransactionID ids.TransactionID `json:"transactionId"`
	Direction     ledger.Direction  `json:"direction"`
	Money         money.Money       `json:"money"`
	BalanceBefore money.Money       `json:"balanceBefore"`
	BalanceAfter  money.Money       `json:"balanceAfter"`
	WalletVersion int64             `json:"walletVersion"`
}

func NewWalletBalanceChanged(origin Origin, change WalletBalanceChanged) (Event, error) {
	if !change.valid() {
		return Event{}, ErrInvalidEvent
	}
	return newEvent(TypeWalletBalanceChanged, change.WalletID.String(), change.WalletID, origin, change)
}

func (c WalletBalanceChanged) valid() bool {
	if c.WalletID.IsZero() || c.TransactionID.IsZero() {
		return false
	}
	return notNegative(c.Money) && notNegative(c.BalanceBefore) && notNegative(c.BalanceAfter)
}
