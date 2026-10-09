package payloadhash_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"jungle-gaming-challeng/internal/app/payloadhash"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
)

const canonicalBet = `{"externalTransactionId":"transaction-123","gameId":"fortune-chimp","kind":"BET",` +
	`"money":{"amount":"25.00","currency":"BRL"},"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",` +
	`"providerId":"provider-a","roundId":"round-987","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37"}`

const canonicalRefund = `{"externalTransactionId":"transaction-123","gameId":"fortune-chimp","kind":"REFUND",` +
	`"money":{"amount":"25.00","currency":"BRL"},"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",` +
	`"providerId":"provider-a","referenceExternalTransactionId":"transaction-100","roundId":"round-987",` +
	`"walletId":"0192f291-27dd-7d3f-8071-5f8685deef37"}`

func parsed[T any](t *testing.T, parse func(string) (T, error), text string) T {
	t.Helper()

	value, err := parse(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return value
}

func amount(t *testing.T, text, code string) money.Money {
	t.Helper()

	m, err := money.Parse(text, parsed(t, money.ParseCurrency, code))
	if err != nil {
		t.Fatalf("Parse(%q, %s): %v", text, code, err)
	}
	return m
}

func bet(t *testing.T) payloadhash.Fields {
	t.Helper()

	return payloadhash.Fields{
		ProviderID:            parsed(t, ids.ParseProviderID, "provider-a"),
		ExternalTransactionID: parsed(t, ids.ParseExternalTransactionID, "transaction-123"),
		PlayerID:              parsed(t, ids.ParsePlayerID, "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1"),
		WalletID:              parsed(t, ids.ParseWalletID, "0192f291-27dd-7d3f-8071-5f8685deef37"),
		RoundID:               parsed(t, ids.ParseRoundID, "round-987"),
		GameID:                parsed(t, ids.ParseGameID, "fortune-chimp"),
		Kind:                  wager.Bet,
		Money:                 amount(t, "25.00", "BRL"),
	}
}

func refund(t *testing.T) payloadhash.Fields {
	t.Helper()

	fields := bet(t)
	fields.Kind = wager.Refund
	fields.ReferenceExternalTransactionID = parsed(t, ids.ParseExternalTransactionID, "transaction-100")
	return fields
}

func TestCanonical(t *testing.T) {
	tests := []struct {
		name   string
		fields payloadhash.Fields
		want   string
	}{
		{"without reference", bet(t), canonicalBet},
		{"with reference", refund(t), canonicalRefund},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := payloadhash.Canonical(tt.fields)
			if err != nil {
				t.Fatalf("Canonical: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("Canonical =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestSum(t *testing.T) {
	want := sha256.Sum256([]byte(canonicalBet))

	got, err := payloadhash.Sum(bet(t))
	if err != nil {
		t.Fatalf("Sum: %v", err)
	}
	if got != hex.EncodeToString(want[:]) || len(got) != 64 {
		t.Fatalf("Sum = %q, want the SHA-256 of the canonical JSON in hexadecimal", got)
	}
}

func TestSumChangesWithEveryBusinessField(t *testing.T) {
	changes := map[string]func(*payloadhash.Fields){
		"provider": func(f *payloadhash.Fields) { f.ProviderID = parsed(t, ids.ParseProviderID, "provider-b") },
		"external id": func(f *payloadhash.Fields) {
			f.ExternalTransactionID = parsed(t, ids.ParseExternalTransactionID, "transaction-999")
		},
		"player": func(f *payloadhash.Fields) {
			f.PlayerID = parsed(t, ids.ParsePlayerID, "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a9")
		},
		"wallet": func(f *payloadhash.Fields) {
			f.WalletID = parsed(t, ids.ParseWalletID, "0192f291-27dd-7d3f-8071-5f8685deef39")
		},
		"round":    func(f *payloadhash.Fields) { f.RoundID = parsed(t, ids.ParseRoundID, "round-988") },
		"game":     func(f *payloadhash.Fields) { f.GameID = parsed(t, ids.ParseGameID, "other-game") },
		"kind":     func(f *payloadhash.Fields) { f.Kind = wager.Win },
		"amount":   func(f *payloadhash.Fields) { f.Money = amount(t, "30.00", "BRL") },
		"currency": func(f *payloadhash.Fields) { f.Money = amount(t, "25.00", "USD") },
		"reference": func(f *payloadhash.Fields) {
			f.ReferenceExternalTransactionID = parsed(t, ids.ParseExternalTransactionID, "transaction-100")
		},
	}

	original, err := payloadhash.Sum(bet(t))
	if err != nil {
		t.Fatalf("Sum: %v", err)
	}

	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			fields := bet(t)
			change(&fields)

			got, err := payloadhash.Sum(fields)
			if err != nil {
				t.Fatalf("Sum: %v", err)
			}
			if got == original {
				t.Fatalf("Sum did not change when the %s changed", name)
			}
		})
	}
}

func TestSumRejectsUninitializedMoney(t *testing.T) {
	fields := bet(t)
	fields.Money = money.Money{}

	if _, err := payloadhash.Sum(fields); !errors.Is(err, money.ErrUninitialized) {
		t.Fatalf("err = %v, want %v", err, money.ErrUninitialized)
	}
}
