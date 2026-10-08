package wallet_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wallet"
)

var longAgo = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func amount(t *testing.T, units int64, code string) money.Money {
	t.Helper()

	currency, err := money.ParseCurrency(code)
	if err != nil {
		t.Fatalf("ParseCurrency(%q): %v", code, err)
	}
	m, err := money.FromMinorUnits(units, currency)
	if err != nil {
		t.Fatalf("FromMinorUnits(%d, %s): %v", units, code, err)
	}
	return m
}

func player(t *testing.T) ids.PlayerID {
	t.Helper()

	id, err := ids.ParsePlayerID("0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1")
	if err != nil {
		t.Fatalf("ParsePlayerID: %v", err)
	}
	return id
}

func storedState(t *testing.T, balance money.Money) wallet.State {
	t.Helper()

	id, err := ids.NewWalletID()
	if err != nil {
		t.Fatalf("NewWalletID: %v", err)
	}
	return wallet.State{
		ID:        id,
		PlayerID:  player(t),
		Balance:   balance,
		Version:   2,
		CreatedAt: longAgo,
		UpdatedAt: longAgo,
	}
}

func stored(t *testing.T, balance money.Money) *wallet.Wallet {
	t.Helper()

	w, err := wallet.Rehydrate(storedState(t, balance))
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	return w
}

func TestOpenWithBalance(t *testing.T) {
	initial := amount(t, 100000, "BRL")

	w, movement, err := wallet.Open(player(t), initial)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	state := w.State()
	if state.ID.IsZero() || state.PlayerID != player(t) || !state.Balance.Equal(initial) || state.Version != 1 {
		t.Fatalf("State() = %+v, want a new wallet with 1000.00 BRL at version 1", state)
	}
	if state.CreatedAt.IsZero() || !state.UpdatedAt.Equal(state.CreatedAt) {
		t.Fatalf("CreatedAt, UpdatedAt = %s, %s, want the same instant", state.CreatedAt, state.UpdatedAt)
	}
	if state.CreatedAt != state.CreatedAt.UTC().Truncate(time.Microsecond) {
		t.Fatalf("CreatedAt = %s, want UTC with microsecond precision", state.CreatedAt)
	}

	want := wallet.Movement{
		Direction:     ledger.Credit,
		Amount:        initial,
		BalanceBefore: amount(t, 0, "BRL"),
		BalanceAfter:  initial,
		WalletVersion: 1,
	}
	if movement == nil || *movement != want {
		t.Fatalf("movement = %+v, want %+v", movement, want)
	}
}

func TestOpenWithZeroBalance(t *testing.T) {
	w, movement, err := wallet.Open(player(t), amount(t, 0, "BRL"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if movement != nil {
		t.Fatalf("movement = %+v, want none", movement)
	}
	if state := w.State(); !state.Balance.IsZero() || state.Version != 1 {
		t.Fatalf("State() = %+v, want 0.00 at version 1", state)
	}
}

func TestOpenRejects(t *testing.T) {
	tests := []struct {
		name    string
		player  ids.PlayerID
		initial money.Money
		wantErr error
	}{
		{"negative balance", player(t), amount(t, -1, "BRL"), wallet.ErrInvalidAmount},
		{"uninitialized balance", player(t), money.Money{}, wallet.ErrInvalidAmount},
		{"missing player", ids.PlayerID{}, amount(t, 100000, "BRL"), wallet.ErrInvalidWallet},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, movement, err := wallet.Open(tt.player, tt.initial)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if w != nil || movement != nil {
				t.Fatalf("Open returned %+v and %+v with an error", w, movement)
			}
		})
	}
}

func TestRehydrate(t *testing.T) {
	state := storedState(t, amount(t, 97500, "BRL"))

	w, err := wallet.Rehydrate(state)
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if w.State() != state {
		t.Fatalf("State() = %+v, want %+v", w.State(), state)
	}
}

func TestRehydrateRejects(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*wallet.State)
	}{
		{"missing id", func(s *wallet.State) { s.ID = ids.WalletID{} }},
		{"missing player", func(s *wallet.State) { s.PlayerID = ids.PlayerID{} }},
		{"negative balance", func(s *wallet.State) { s.Balance = amount(t, -1, "BRL") }},
		{"uninitialized balance", func(s *wallet.State) { s.Balance = money.Money{} }},
		{"version zero", func(s *wallet.State) { s.Version = 0 }},
		{"missing creation time", func(s *wallet.State) { s.CreatedAt = time.Time{} }},
		{"missing update time", func(s *wallet.State) { s.UpdatedAt = time.Time{} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := storedState(t, amount(t, 97500, "BRL"))
			tt.apply(&state)

			if _, err := wallet.Rehydrate(state); !errors.Is(err, wallet.ErrInvalidWallet) {
				t.Fatalf("err = %v, want %v", err, wallet.ErrInvalidWallet)
			}
		})
	}
}

func TestCredit(t *testing.T) {
	w := stored(t, amount(t, 97500, "BRL"))

	movement, err := w.Credit(amount(t, 2500, "BRL"))
	if err != nil {
		t.Fatalf("Credit: %v", err)
	}

	want := wallet.Movement{
		Direction:     ledger.Credit,
		Amount:        amount(t, 2500, "BRL"),
		BalanceBefore: amount(t, 97500, "BRL"),
		BalanceAfter:  amount(t, 100000, "BRL"),
		WalletVersion: 3,
	}
	if movement != want {
		t.Fatalf("movement = %+v, want %+v", movement, want)
	}

	state := w.State()
	if !state.Balance.Equal(want.BalanceAfter) || state.Version != 3 {
		t.Fatalf("State() = %+v, want 1000.00 at version 3", state)
	}
	if !state.UpdatedAt.After(longAgo) || !state.CreatedAt.Equal(longAgo) {
		t.Fatalf("CreatedAt, UpdatedAt = %s, %s, want only UpdatedAt to move", state.CreatedAt, state.UpdatedAt)
	}
}

func TestCreditRejects(t *testing.T) {
	tests := []struct {
		name    string
		balance money.Money
		amount  money.Money
		wantErr error
	}{
		{"another currency", amount(t, 97500, "BRL"), amount(t, 2500, "USD"), wallet.ErrCurrencyMismatch},
		{"zero amount", amount(t, 97500, "BRL"), amount(t, 0, "BRL"), wallet.ErrInvalidAmount},
		{"negative amount", amount(t, 97500, "BRL"), amount(t, -2500, "BRL"), wallet.ErrInvalidAmount},
		{"uninitialized amount", amount(t, 97500, "BRL"), money.Money{}, wallet.ErrInvalidAmount},
		{"above the limit", amount(t, math.MaxInt64, "BRL"), amount(t, 1, "BRL"), wallet.ErrBalanceOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := stored(t, tt.balance)
			before := w.State()

			if _, err := w.Credit(tt.amount); !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if w.State() != before {
				t.Fatalf("State() = %+v, want it unchanged: %+v", w.State(), before)
			}
		})
	}
}

func TestDebit(t *testing.T) {
	tests := []struct {
		name      string
		amount    money.Money
		wantAfter money.Money
	}{
		{"part of the balance", amount(t, 2500, "BRL"), amount(t, 97500, "BRL")},
		{"the whole balance", amount(t, 100000, "BRL"), amount(t, 0, "BRL")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := stored(t, amount(t, 100000, "BRL"))

			movement, err := w.Debit(tt.amount)
			if err != nil {
				t.Fatalf("Debit: %v", err)
			}

			want := wallet.Movement{
				Direction:     ledger.Debit,
				Amount:        tt.amount,
				BalanceBefore: amount(t, 100000, "BRL"),
				BalanceAfter:  tt.wantAfter,
				WalletVersion: 3,
			}
			if movement != want {
				t.Fatalf("movement = %+v, want %+v", movement, want)
			}

			state := w.State()
			if !state.Balance.Equal(tt.wantAfter) || state.Version != 3 || !state.UpdatedAt.After(longAgo) {
				t.Fatalf("State() = %+v, want %s at version 3 with a new UpdatedAt", state, tt.wantAfter.Amount())
			}
		})
	}
}

func TestDebitRejects(t *testing.T) {
	tests := []struct {
		name    string
		amount  money.Money
		wantErr error
	}{
		{"more than the balance", amount(t, 100001, "BRL"), wallet.ErrInsufficientFunds},
		{"another currency", amount(t, 2500, "USD"), wallet.ErrCurrencyMismatch},
		{"zero amount", amount(t, 0, "BRL"), wallet.ErrInvalidAmount},
		{"negative amount", amount(t, -2500, "BRL"), wallet.ErrInvalidAmount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := stored(t, amount(t, 100000, "BRL"))
			before := w.State()

			if _, err := w.Debit(tt.amount); !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if w.State() != before {
				t.Fatalf("State() = %+v, want it unchanged: %+v", w.State(), before)
			}
		})
	}
}

func TestMovementsMakeValidLedgerEntries(t *testing.T) {
	w, opening, err := wallet.Open(player(t), amount(t, 100000, "BRL"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	debit, err := w.Debit(amount(t, 2500, "BRL"))
	if err != nil {
		t.Fatalf("Debit: %v", err)
	}
	credit, err := w.Credit(amount(t, 1000, "BRL"))
	if err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if opening.WalletVersion != 1 || debit.WalletVersion != 2 || credit.WalletVersion != 3 {
		t.Fatalf("versions = %d, %d, %d, want 1, 2, 3", opening.WalletVersion, debit.WalletVersion, credit.WalletVersion)
	}

	for _, movement := range []wallet.Movement{*opening, debit, credit} {
		transactionID, err := ids.NewTransactionID()
		if err != nil {
			t.Fatalf("NewTransactionID: %v", err)
		}
		_, err = ledger.NewEntry(ledger.Fields{
			WalletID:      w.State().ID,
			TransactionID: transactionID,
			Direction:     movement.Direction,
			Amount:        movement.Amount,
			BalanceBefore: movement.BalanceBefore,
			BalanceAfter:  movement.BalanceAfter,
			WalletVersion: movement.WalletVersion,
		})
		if err != nil {
			t.Fatalf("NewEntry for version %d: %v", movement.WalletVersion, err)
		}
	}
}
