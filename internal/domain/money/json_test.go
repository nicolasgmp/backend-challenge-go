package money_test

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"jungle-gaming-challeng/internal/domain/money"
)

func TestMarshalJSON(t *testing.T) {
	got, err := json.Marshal(units(t, 2500, "BRL"))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := `{"amount":"25.00","currency":"BRL"}`; string(got) != want {
		t.Fatalf("Marshal = %s, want %s", got, want)
	}
}

func TestMarshalJSONUninitialized(t *testing.T) {
	if _, err := json.Marshal(money.Money{}); !errors.Is(err, money.ErrUninitialized) {
		t.Fatalf("err = %v, want %v", err, money.ErrUninitialized)
	}
}

func TestUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantUnits int64
		wantCode  string
		wantErr   error
	}{
		{"valid", `{"amount":"25.00","currency":"BRL"}`, 2500, "BRL", nil},
		{"fields in another order", `{"currency":"USD","amount":"0.05"}`, 5, "USD", nil},
		{"amount as a number", `{"amount":25.00,"currency":"BRL"}`, 0, "", money.ErrInvalidAmount},
		{"amount missing", `{"currency":"BRL"}`, 0, "", money.ErrInvalidAmount},
		{"currency missing", `{"amount":"25.00"}`, 0, "", money.ErrInvalidCurrency},
		{"unknown field", `{"amount":"25.00","currency":"BRL","scale":2}`, 0, "", money.ErrInvalidAmount},
		{"negative amount", `{"amount":"-25.00","currency":"BRL"}`, 0, "", money.ErrNegativeAmount},
		{"loose format", `{"amount":"25","currency":"BRL"}`, 0, "", money.ErrInvalidAmount},
		{"unsupported currency", `{"amount":"25.00","currency":"JPY"}`, 0, "", money.ErrInvalidCurrency},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got money.Money
			err := json.Unmarshal([]byte(tt.input), &got)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got.MinorUnits() != tt.wantUnits || got.Currency().Code() != tt.wantCode {
				t.Fatalf("got %d %q, want %d %q", got.MinorUnits(), got.Currency().Code(), tt.wantUnits, tt.wantCode)
			}
		})
	}
}

func TestJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		value money.Money
	}{
		{"zero", units(t, 0, "BRL")},
		{"cents", units(t, 5, "USD")},
		{"whole", units(t, 2500, "EUR")},
		{"largest", units(t, math.MaxInt64, "BRL")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got money.Money
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !got.Equal(tt.value) {
				t.Fatalf("got %s %s, want %s %s",
					got.Amount(), got.Currency().Code(), tt.value.Amount(), tt.value.Currency().Code())
			}
		})
	}
}
