package money_test

import (
	"errors"
	"math"
	"testing"

	"jungle-gaming-challeng/internal/domain/money"
)

func currency(t *testing.T, code string) money.Currency {
	t.Helper()

	c, err := money.ParseCurrency(code)
	if err != nil {
		t.Fatalf("ParseCurrency(%q): %v", code, err)
	}
	return c
}

func units(t *testing.T, n int64, code string) money.Money {
	t.Helper()

	m, err := money.FromMinorUnits(n, currency(t, code))
	if err != nil {
		t.Fatalf("FromMinorUnits(%d, %s): %v", n, code, err)
	}
	return m
}

func TestZero(t *testing.T) {
	got, err := money.Zero(currency(t, "BRL"))
	if err != nil {
		t.Fatalf("Zero: %v", err)
	}
	if got.MinorUnits() != 0 || got.Currency().Code() != "BRL" || got.Amount() != "0.00" {
		t.Fatalf("Zero = %s %s, want 0.00 BRL", got.Amount(), got.Currency().Code())
	}
	if !got.IsZero() || got.IsPositive() || got.IsNegative() {
		t.Fatalf("IsZero, IsPositive, IsNegative = %v, %v, %v, want true, false, false",
			got.IsZero(), got.IsPositive(), got.IsNegative())
	}
}

func TestFromMinorUnits(t *testing.T) {
	tests := []struct {
		name  string
		units int64
		code  string
	}{
		{"positive", 2500, "BRL"},
		{"negative", -500, "BRL"},
		{"zero", 0, "USD"},
		{"largest", math.MaxInt64, "EUR"},
		{"smallest", math.MinInt64, "EUR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := money.FromMinorUnits(tt.units, currency(t, tt.code))
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got.MinorUnits() != tt.units {
				t.Fatalf("MinorUnits() = %d, want %d", got.MinorUnits(), tt.units)
			}
			if got.Currency().Code() != tt.code {
				t.Fatalf("Currency() = %q, want %q", got.Currency().Code(), tt.code)
			}
		})
	}
}

func TestConstructorsRejectUninitializedCurrency(t *testing.T) {
	var none money.Currency

	if _, err := money.Zero(none); !errors.Is(err, money.ErrUninitialized) {
		t.Fatalf("Zero: err = %v, want %v", err, money.ErrUninitialized)
	}
	if _, err := money.FromMinorUnits(100, none); !errors.Is(err, money.ErrUninitialized) {
		t.Fatalf("FromMinorUnits: err = %v, want %v", err, money.ErrUninitialized)
	}
	if _, err := money.Parse("1.00", none); !errors.Is(err, money.ErrUninitialized) {
		t.Fatalf("Parse: err = %v, want %v", err, money.ErrUninitialized)
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		amount  string
		want    int64
		wantErr error
	}{
		{"25.00", 2500, nil},
		{"0.00", 0, nil},
		{"0.05", 5, nil},
		{"92233720368547758.07", math.MaxInt64, nil},

		{"25", 0, money.ErrInvalidAmount},
		{"25.0", 0, money.ErrInvalidAmount},
		{"25.", 0, money.ErrInvalidAmount},
		{".50", 0, money.ErrInvalidAmount},
		{"025.00", 0, money.ErrInvalidAmount},
		{"+25.00", 0, money.ErrInvalidAmount},
		{"25,00", 0, money.ErrInvalidAmount},
		{"1_000.00", 0, money.ErrInvalidAmount},
		{"25.000", 0, money.ErrInvalidAmount},
		{"25.001", 0, money.ErrInvalidAmount},
		{"", 0, money.ErrInvalidAmount},
		{" ", 0, money.ErrInvalidAmount},
		{" 25.00", 0, money.ErrInvalidAmount},
		{"25.00 ", 0, money.ErrInvalidAmount},
		{"NaN", 0, money.ErrInvalidAmount},
		{"Infinity", 0, money.ErrInvalidAmount},
		{"-Infinity", 0, money.ErrInvalidAmount},
		{"1e2", 0, money.ErrInvalidAmount},
		{"1E2", 0, money.ErrInvalidAmount},
		{"--1.00", 0, money.ErrInvalidAmount},

		{"-25.00", 0, money.ErrNegativeAmount},
		{"-0.00", 0, money.ErrNegativeAmount},

		{"92233720368547758.08", 0, money.ErrOverflow},
		{"100000000000000000000.00", 0, money.ErrOverflow},
	}

	brl := currency(t, "BRL")

	for _, tt := range tests {
		t.Run(tt.amount, func(t *testing.T) {
			got, err := money.Parse(tt.amount, brl)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got.MinorUnits() != tt.want {
				t.Fatalf("MinorUnits() = %d, want %d", got.MinorUnits(), tt.want)
			}
		})
	}
}

func TestAmount(t *testing.T) {
	tests := []struct {
		units int64
		want  string
	}{
		{0, "0.00"},
		{5, "0.05"},
		{-5, "-0.05"},
		{-500, "-5.00"},
		{2500, "25.00"},
		{math.MaxInt64, "92233720368547758.07"},
		{math.MinInt64, "-92233720368547758.08"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := units(t, tt.units, "BRL").Amount(); got != tt.want {
				t.Fatalf("Amount() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAdd(t *testing.T) {
	tests := []struct {
		name    string
		a, b    money.Money
		want    int64
		wantErr error
	}{
		{"same currency", units(t, 1000, "BRL"), units(t, 500, "BRL"), 1500, nil},
		{"negative operand", units(t, 1000, "BRL"), units(t, -2500, "BRL"), -1500, nil},
		{"limits cancel out", units(t, math.MaxInt64, "BRL"), units(t, math.MinInt64, "BRL"), -1, nil},
		{"different currencies", units(t, 1000, "BRL"), units(t, 1000, "USD"), 0, money.ErrCurrencyMismatch},
		{"above the largest", units(t, math.MaxInt64, "BRL"), units(t, 1, "BRL"), 0, money.ErrOverflow},
		{"below the smallest", units(t, math.MinInt64, "BRL"), units(t, -1, "BRL"), 0, money.ErrOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.a.Add(tt.b)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got.MinorUnits() != tt.want {
				t.Fatalf("MinorUnits() = %d, want %d", got.MinorUnits(), tt.want)
			}
		})
	}
}

func TestSub(t *testing.T) {
	tests := []struct {
		name    string
		a, b    money.Money
		want    int64
		wantErr error
	}{
		{"negative result", units(t, 1000, "BRL"), units(t, 2500, "BRL"), -1500, nil},
		{"up to the largest", units(t, -1, "BRL"), units(t, math.MinInt64, "BRL"), math.MaxInt64, nil},
		{"different currencies", units(t, 1000, "BRL"), units(t, 1000, "USD"), 0, money.ErrCurrencyMismatch},
		{"below the smallest", units(t, math.MinInt64, "BRL"), units(t, 1, "BRL"), 0, money.ErrOverflow},
		{"above the largest", units(t, math.MaxInt64, "BRL"), units(t, -1, "BRL"), 0, money.ErrOverflow},
		{"zero minus the smallest", units(t, 0, "BRL"), units(t, math.MinInt64, "BRL"), 0, money.ErrOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.a.Sub(tt.b)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got.MinorUnits() != tt.want {
				t.Fatalf("MinorUnits() = %d, want %d", got.MinorUnits(), tt.want)
			}
		})
	}
}

func TestNeg(t *testing.T) {
	tests := []struct {
		name    string
		units   int64
		want    int64
		wantErr error
	}{
		{"positive", 500, -500, nil},
		{"negative", -500, 500, nil},
		{"zero", 0, 0, nil},
		{"largest", math.MaxInt64, -math.MaxInt64, nil},
		{"smallest", math.MinInt64, 0, money.ErrOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := units(t, tt.units, "BRL").Neg()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got.MinorUnits() != tt.want {
				t.Fatalf("MinorUnits() = %d, want %d", got.MinorUnits(), tt.want)
			}
		})
	}
}

func TestCmp(t *testing.T) {
	tests := []struct {
		name    string
		a, b    money.Money
		want    int
		wantErr error
	}{
		{"less", units(t, 500, "BRL"), units(t, 1000, "BRL"), -1, nil},
		{"equal", units(t, 1000, "BRL"), units(t, 1000, "BRL"), 0, nil},
		{"greater", units(t, 1000, "BRL"), units(t, 500, "BRL"), 1, nil},
		{"different currencies", units(t, 1000, "BRL"), units(t, 1000, "USD"), 0, money.ErrCurrencyMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.a.Cmp(tt.b)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("Cmp() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b money.Money
		want bool
	}{
		{"same value and currency", units(t, 1000, "BRL"), units(t, 1000, "BRL"), true},
		{"different value", units(t, 1000, "BRL"), units(t, 999, "BRL"), false},
		{"different currencies", units(t, 1000, "BRL"), units(t, 1000, "USD"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Equal(tt.b); got != tt.want {
				t.Fatalf("Equal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPredicates(t *testing.T) {
	tests := []struct {
		name                     string
		units                    int64
		zero, positive, negative bool
	}{
		{"zero", 0, true, false, false},
		{"positive", 1, false, true, false},
		{"negative", -1, false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := units(t, tt.units, "BRL")
			if m.IsZero() != tt.zero || m.IsPositive() != tt.positive || m.IsNegative() != tt.negative {
				t.Fatalf("IsZero, IsPositive, IsNegative = %v, %v, %v, want %v, %v, %v",
					m.IsZero(), m.IsPositive(), m.IsNegative(), tt.zero, tt.positive, tt.negative)
			}
		})
	}
}

func TestOperandsAreNotChanged(t *testing.T) {
	a, b := units(t, 1000, "BRL"), units(t, 500, "BRL")

	sum, err := a.Add(b)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if sum.Amount() != "15.00" || a.Amount() != "10.00" || b.Amount() != "5.00" {
		t.Fatalf("sum, a, b = %s, %s, %s, want 15.00, 10.00, 5.00", sum.Amount(), a.Amount(), b.Amount())
	}
}

func TestUninitialized(t *testing.T) {
	var none money.Money
	brl := units(t, 100, "BRL")

	tests := []struct {
		name string
		op   func() error
	}{
		{"Add on it", func() error { _, err := none.Add(brl); return err }},
		{"Add with it", func() error { _, err := brl.Add(none); return err }},
		{"Sub on it", func() error { _, err := none.Sub(brl); return err }},
		{"Sub with it", func() error { _, err := brl.Sub(none); return err }},
		{"Neg", func() error { _, err := none.Neg(); return err }},
		{"Cmp on it", func() error { _, err := none.Cmp(brl); return err }},
		{"Cmp with it", func() error { _, err := brl.Cmp(none); return err }},
		{"MarshalJSON", func() error { _, err := none.MarshalJSON(); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.op(); !errors.Is(err, money.ErrUninitialized) {
				t.Fatalf("err = %v, want %v", err, money.ErrUninitialized)
			}
		})
	}

	if none.Equal(none) || none.Equal(brl) || brl.Equal(none) {
		t.Fatal("Equal with an uninitialized value = true, want false")
	}
	if none.IsZero() || none.IsPositive() || none.IsNegative() {
		t.Fatal("a predicate of an uninitialized value = true, want false")
	}
}

func TestParseAmountRoundTrip(t *testing.T) {
	brl := currency(t, "BRL")

	for _, amount := range []string{"0.00", "0.05", "25.00", "1000000.10", "92233720368547758.07"} {
		t.Run(amount, func(t *testing.T) {
			m, err := money.Parse(amount, brl)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := m.Amount(); got != amount {
				t.Fatalf("Amount() = %q, want %q", got, amount)
			}
		})
	}
}
