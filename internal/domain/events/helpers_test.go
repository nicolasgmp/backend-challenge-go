package events

import (
	"testing"
	"time"

	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/money"
)

const (
	transactionUUID = "0192f298-345e-7e38-af88-e43f851a819d"
	walletUUID      = "0192f291-27dd-7d3f-8071-5f8685deef37"
	playerUUID      = "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1"
)

var (
	origin   = Origin{CorrelationID: "correlation-1", CausationID: "message-1"}
	deadline = time.Date(2026, 1, 1, 12, 5, 0, 0, time.UTC)
)

func parsed[T any](t *testing.T, parse func(string) (T, error), text string) T {
	t.Helper()

	value, err := parse(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return value
}

func amount(t *testing.T, units int64) money.Money {
	t.Helper()

	m, err := money.FromMinorUnits(units, parsed(t, money.ParseCurrency, "BRL"))
	if err != nil {
		t.Fatalf("FromMinorUnits(%d): %v", units, err)
	}
	return m
}

func opening(t *testing.T) Transaction {
	t.Helper()

	return Transaction{
		TransactionID: parsed(t, ids.ParseTransactionID, transactionUUID),
		WalletID:      parsed(t, ids.ParseWalletID, walletUUID),
		PlayerID:      parsed(t, ids.ParsePlayerID, playerUUID),
		Kind:          "OPENING",
		Money:         amount(t, 100000),
	}
}

func refund(t *testing.T) Transaction {
	t.Helper()

	tx := opening(t)
	tx.Kind = "REFUND"
	tx.Money = amount(t, 2500)
	tx.ProviderID = parsed(t, ids.ParseProviderID, "provider-a")
	tx.ExternalTransactionID = parsed(t, ids.ParseExternalTransactionID, "transaction-456")
	tx.RoundID = parsed(t, ids.ParseRoundID, "round-987")
	tx.GameID = parsed(t, ids.ParseGameID, "fortune-chimp")
	tx.ReferenceExternalTransactionID = parsed(t, ids.ParseExternalTransactionID, "transaction-123")
	return tx
}

func debit(t *testing.T) WalletBalanceChanged {
	t.Helper()

	return WalletBalanceChanged{
		WalletID:      parsed(t, ids.ParseWalletID, walletUUID),
		TransactionID: parsed(t, ids.ParseTransactionID, transactionUUID),
		Direction:     ledger.Debit,
		Money:         amount(t, 2500),
		BalanceBefore: amount(t, 100000),
		BalanceAfter:  amount(t, 97500),
		WalletVersion: 2,
	}
}
