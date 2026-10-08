package money_test

import (
	"errors"
	"fmt"
	"testing"

	"jungle-gaming-challeng/internal/domain/money"
)

var sentinels = []struct {
	name string
	err  error
}{
	{"ErrInvalidAmount", money.ErrInvalidAmount},
	{"ErrNegativeAmount", money.ErrNegativeAmount},
	{"ErrInvalidCurrency", money.ErrInvalidCurrency},
	{"ErrCurrencyMismatch", money.ErrCurrencyMismatch},
	{"ErrOverflow", money.ErrOverflow},
	{"ErrUninitialized", money.ErrUninitialized},
}

func TestSentinelsMatchWhenWrapped(t *testing.T) {
	for _, tt := range sentinels {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := fmt.Errorf("parse request: %w", tt.err)
			if !errors.Is(wrapped, tt.err) {
				t.Fatalf("errors.Is(%v, %s) = false, want true", wrapped, tt.name)
			}
		})
	}
}

func TestSentinelsAreDistinct(t *testing.T) {
	for i := range sentinels {
		for _, other := range sentinels[i+1:] {
			if errors.Is(sentinels[i].err, other.err) {
				t.Fatalf("%s matches %s", sentinels[i].name, other.name)
			}
		}
	}
}
