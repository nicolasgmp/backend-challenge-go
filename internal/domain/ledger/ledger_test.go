package ledger_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/money"
)

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

func validFields(t *testing.T) ledger.Fields {
	t.Helper()

	walletID, err := ids.NewWalletID()
	if err != nil {
		t.Fatalf("NewWalletID: %v", err)
	}
	transactionID, err := ids.NewTransactionID()
	if err != nil {
		t.Fatalf("NewTransactionID: %v", err)
	}
	return ledger.Fields{
		WalletID:      walletID,
		TransactionID: transactionID,
		Direction:     ledger.Debit,
		Amount:        amount(t, 2500, "BRL"),
		BalanceBefore: amount(t, 100000, "BRL"),
		BalanceAfter:  amount(t, 97500, "BRL"),
		WalletVersion: 2,
	}
}

type brokenFields struct {
	name  string
	apply func(*ledger.Fields)
}

func invalidFields(t *testing.T) []brokenFields {
	t.Helper()

	return []brokenFields{
		{"zero amount", func(f *ledger.Fields) {
			f.Amount = amount(t, 0, "BRL")
			f.BalanceAfter = f.BalanceBefore
		}},
		{"negative amount", func(f *ledger.Fields) {
			f.Amount = amount(t, -2500, "BRL")
			f.BalanceAfter = amount(t, 102500, "BRL")
		}},
		{"uninitialized amount", func(f *ledger.Fields) { f.Amount = money.Money{} }},
		{"amount in another currency", func(f *ledger.Fields) { f.Amount = amount(t, 2500, "USD") }},
		{"negative balance before", func(f *ledger.Fields) {
			f.Direction = ledger.Credit
			f.BalanceBefore = amount(t, -100, "BRL")
			f.BalanceAfter = amount(t, 2400, "BRL")
		}},
		{"negative balance after", func(f *ledger.Fields) {
			f.BalanceBefore = amount(t, 1000, "BRL")
			f.BalanceAfter = amount(t, -1500, "BRL")
		}},
		{"balance after in another currency", func(f *ledger.Fields) { f.BalanceAfter = amount(t, 97500, "USD") }},
		{"wallet version zero", func(f *ledger.Fields) { f.WalletVersion = 0 }},
		{"debit that does not add up", func(f *ledger.Fields) { f.BalanceAfter = amount(t, 98000, "BRL") }},
		{"credit that does not add up", func(f *ledger.Fields) { f.Direction = ledger.Credit }},
		{"credit above the limit", func(f *ledger.Fields) {
			f.Direction = ledger.Credit
			f.BalanceBefore = amount(t, math.MaxInt64, "BRL")
		}},
		{"unknown direction", func(f *ledger.Fields) { f.Direction = ledger.Direction("TRANSFER") }},
		{"missing wallet", func(f *ledger.Fields) { f.WalletID = ids.WalletID{} }},
		{"missing transaction", func(f *ledger.Fields) { f.TransactionID = ids.TransactionID{} }},
	}
}

func TestParseDirection(t *testing.T) {
	tests := []struct {
		input   string
		want    ledger.Direction
		wantErr error
	}{
		{"DEBIT", ledger.Debit, nil},
		{"CREDIT", ledger.Credit, nil},
		{"debit", "", ledger.ErrInvalidDirection},
		{"", "", ledger.ErrInvalidDirection},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ledger.ParseDirection(tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ParseDirection() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewEntry(t *testing.T) {
	debit := validFields(t)

	credit := validFields(t)
	credit.Direction = ledger.Credit
	credit.BalanceBefore = amount(t, 97500, "BRL")
	credit.BalanceAfter = amount(t, 100000, "BRL")
	credit.WalletVersion = 3

	tests := []struct {
		name   string
		fields ledger.Fields
	}{
		{"debit", debit},
		{"credit", credit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, err := ledger.NewEntry(tt.fields)
			if err != nil {
				t.Fatalf("NewEntry: %v", err)
			}
			if entry.ID().IsZero() || entry.CreatedAt().IsZero() {
				t.Fatal("NewEntry did not set the id or the creation time")
			}
			if !entry.CreatedAt().Equal(entry.CreatedAt().Truncate(time.Microsecond)) {
				t.Fatalf("CreatedAt() = %s, want microsecond precision", entry.CreatedAt())
			}
			if entry.Fields() != tt.fields {
				t.Fatalf("Fields() = %+v, want %+v", entry.Fields(), tt.fields)
			}
		})
	}
}

func TestNewEntryRejects(t *testing.T) {
	for _, tt := range invalidFields(t) {
		t.Run(tt.name, func(t *testing.T) {
			fields := validFields(t)
			tt.apply(&fields)

			if _, err := ledger.NewEntry(fields); !errors.Is(err, ledger.ErrInvalidEntry) {
				t.Fatalf("err = %v, want %v", err, ledger.ErrInvalidEntry)
			}
		})
	}
}

func TestRehydrate(t *testing.T) {
	id, err := ids.NewEntryID()
	if err != nil {
		t.Fatalf("NewEntryID: %v", err)
	}
	createdAt := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	fields := validFields(t)

	entry, err := ledger.Rehydrate(id, fields, createdAt)
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if entry.ID() != id || !entry.CreatedAt().Equal(createdAt) || entry.Fields() != fields {
		t.Fatalf("Rehydrate changed the stored values: %+v", entry)
	}

	if _, err := ledger.Rehydrate(ids.EntryID{}, fields, createdAt); !errors.Is(err, ledger.ErrInvalidEntry) {
		t.Fatalf("zero id: err = %v, want %v", err, ledger.ErrInvalidEntry)
	}
	if _, err := ledger.Rehydrate(id, fields, time.Time{}); !errors.Is(err, ledger.ErrInvalidEntry) {
		t.Fatalf("zero creation time: err = %v, want %v", err, ledger.ErrInvalidEntry)
	}
}

func TestRehydrateRejects(t *testing.T) {
	id, err := ids.NewEntryID()
	if err != nil {
		t.Fatalf("NewEntryID: %v", err)
	}
	createdAt := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

	for _, tt := range invalidFields(t) {
		t.Run(tt.name, func(t *testing.T) {
			fields := validFields(t)
			tt.apply(&fields)

			if _, err := ledger.Rehydrate(id, fields, createdAt); !errors.Is(err, ledger.ErrInvalidEntry) {
				t.Fatalf("err = %v, want %v", err, ledger.ErrInvalidEntry)
			}
		})
	}
}
