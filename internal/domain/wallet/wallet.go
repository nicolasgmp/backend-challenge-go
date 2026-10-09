package wallet

import (
	"errors"
	"time"

	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/money"
)

var (
	ErrInvalidWallet     = errors.New("wallet: invalid wallet")
	ErrInvalidAmount     = errors.New("wallet: invalid amount")
	ErrCurrencyMismatch  = errors.New("wallet: currency mismatch")
	ErrInsufficientFunds = errors.New("wallet: insufficient funds")
	ErrBalanceOverflow   = errors.New("wallet: balance overflow")
)

type State struct {
	ID        ids.WalletID
	PlayerID  ids.PlayerID
	Balance   money.Money
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Movement struct {
	Direction     ledger.Direction
	Amount        money.Money
	BalanceBefore money.Money
	BalanceAfter  money.Money
	WalletVersion int64
}

type Wallet struct {
	state State
}

func Open(playerID ids.PlayerID, initial money.Money) (*Wallet, *Movement, error) {
	if playerID.IsZero() {
		return nil, nil, ErrInvalidWallet
	}
	if !notNegative(initial) {
		return nil, nil, ErrInvalidAmount
	}

	id, err := ids.NewWalletID()
	if err != nil {
		return nil, nil, err
	}
	zero, err := money.Zero(initial.Currency())
	if err != nil {
		return nil, nil, err
	}

	openedAt := now()
	w := &Wallet{state: State{
		ID:        id,
		PlayerID:  playerID,
		Balance:   initial,
		Version:   1,
		CreatedAt: openedAt,
		UpdatedAt: openedAt,
	}}
	if initial.IsZero() {
		return w, nil, nil
	}
	return w, &Movement{
		Direction:     ledger.Credit,
		Amount:        initial,
		BalanceBefore: zero,
		BalanceAfter:  initial,
		WalletVersion: 1,
	}, nil
}

func Rehydrate(state State) (*Wallet, error) {
	if state.ID.IsZero() || state.PlayerID.IsZero() {
		return nil, ErrInvalidWallet
	}
	if !notNegative(state.Balance) || state.Version < 1 {
		return nil, ErrInvalidWallet
	}
	if state.CreatedAt.IsZero() || state.UpdatedAt.IsZero() {
		return nil, ErrInvalidWallet
	}
	return &Wallet{state: state}, nil
}

func (w *Wallet) State() State {
	return w.state
}

func (w *Wallet) Credit(amount money.Money) (Movement, error) {
	if err := w.accepts(amount); err != nil {
		return Movement{}, err
	}
	after, err := w.state.Balance.Add(amount)
	if err != nil {
		return Movement{}, ErrBalanceOverflow
	}
	return w.move(ledger.Credit, amount, after), nil
}

func (w *Wallet) Debit(amount money.Money) (Movement, error) {
	if err := w.accepts(amount); err != nil {
		return Movement{}, err
	}
	after, err := w.state.Balance.Sub(amount)
	if err != nil || after.IsNegative() {
		return Movement{}, ErrInsufficientFunds
	}
	return w.move(ledger.Debit, amount, after), nil
}

func (w *Wallet) accepts(amount money.Money) error {
	if !amount.IsPositive() {
		return ErrInvalidAmount
	}
	if amount.Currency() != w.state.Balance.Currency() {
		return ErrCurrencyMismatch
	}
	return nil
}

func (w *Wallet) move(direction ledger.Direction, amount, after money.Money) Movement {
	movement := Movement{
		Direction:     direction,
		Amount:        amount,
		BalanceBefore: w.state.Balance,
		BalanceAfter:  after,
		WalletVersion: w.state.Version + 1,
	}
	w.state.Balance = after
	w.state.Version = movement.WalletVersion
	w.state.UpdatedAt = now()
	return movement
}

func notNegative(m money.Money) bool {
	return m.IsZero() || m.IsPositive()
}

func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
