package money_test

import (
	"errors"
	"fmt"
	"testing"

	"jungle-gaming-challeng/internal/domain/money"
)

func TestParseCurrency(t *testing.T) {
	tests := []struct {
		code     string
		wantCode string
		wantErr  error
	}{
		{"BRL", "BRL", nil},
		{"USD", "USD", nil},
		{"EUR", "EUR", nil},
		{"JPY", "", money.ErrInvalidCurrency},
		{"brl", "", money.ErrInvalidCurrency},
		{"BR", "", money.ErrInvalidCurrency},
		{"", "", money.ErrInvalidCurrency},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.code), func(t *testing.T) {
			got, err := money.ParseCurrency(tt.code)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got.Code() != tt.wantCode {
				t.Fatalf("Code() = %q, want %q", got.Code(), tt.wantCode)
			}
		})
	}
}

func TestCurrencyZeroValue(t *testing.T) {
	var c money.Currency
	if c.Code() != "" {
		t.Fatalf("Code() = %q, want empty", c.Code())
	}
}
