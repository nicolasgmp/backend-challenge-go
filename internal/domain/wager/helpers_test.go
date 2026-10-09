package wager_test

import (
	"testing"
	"time"

	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
)

var longAgo = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type args struct {
	kind          wager.Kind
	walletID      ids.WalletID
	playerID      ids.PlayerID
	amount        money.Money
	external      wager.External
	correlationID string
}

func (a args) create() (*wager.Transaction, error) {
	return wager.NewExternal(a.kind, a.walletID, a.playerID, a.amount, a.external, a.correlationID)
}

func parsed[T any](t *testing.T, parse func(string) (T, error), text string) T {
	t.Helper()

	value, err := parse(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return value
}

func amount(t *testing.T, units int64, code string) money.Money {
	t.Helper()

	m, err := money.FromMinorUnits(units, parsed(t, money.ParseCurrency, code))
	if err != nil {
		t.Fatalf("FromMinorUnits(%d, %s): %v", units, code, err)
	}
	return m
}

func betArgs(t *testing.T) args {
	t.Helper()

	return args{
		kind:     wager.Bet,
		walletID: parsed(t, ids.ParseWalletID, "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1"),
		playerID: parsed(t, ids.ParsePlayerID, "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a2"),
		amount:   amount(t, 2500, "BRL"),
		external: wager.External{
			ProviderID:     parsed(t, ids.ParseProviderID, "provider-a"),
			ExternalID:     parsed(t, ids.ParseExternalTransactionID, "tx-0"),
			IdempotencyKey: parsed(t, ids.ParseIdempotencyKey, "key-0"),
			PayloadHash:    "hash-0",
			RoundID:        parsed(t, ids.ParseRoundID, "round-1"),
			GameID:         parsed(t, ids.ParseGameID, "game-1"),
		},
		correlationID: "correlation-1",
	}
}

func refundArgs(t *testing.T) args {
	t.Helper()

	a := betArgs(t)
	a.kind = wager.Refund
	a.external.ExternalID = parsed(t, ids.ParseExternalTransactionID, "tx-1")
	a.external.IdempotencyKey = parsed(t, ids.ParseIdempotencyKey, "key-1")
	a.external.ReferenceExternalID = parsed(t, ids.ParseExternalTransactionID, "tx-0")
	return a
}

func argsFor(t *testing.T, kind wager.Kind) args {
	t.Helper()

	a := betArgs(t)
	if kind == wager.Refund || kind == wager.Rollback {
		a = refundArgs(t)
	}
	if kind == wager.Loss {
		a.amount = amount(t, 0, "BRL")
	}
	a.kind = kind
	return a
}

func created(t *testing.T, a args) *wager.Transaction {
	t.Helper()

	tx, err := a.create()
	if err != nil {
		t.Fatalf("NewExternal: %v", err)
	}
	return tx
}

func referenceID(t *testing.T) ids.TransactionID {
	t.Helper()

	return parsed(t, ids.ParseTransactionID, "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a3")
}

func settle(t *testing.T, tx *wager.Transaction, status wager.Status) {
	t.Helper()

	var err error
	switch status {
	case wager.PendingReference:
		err = tx.MarkPendingReference(longAgo.Add(5*time.Minute), longAgo.Add(time.Second))
	case wager.Processed:
		err = tx.MarkProcessed(amount(t, 100000, "BRL"), referenceID(t))
	case wager.Rejected:
		err = tx.MarkRejected(failure.ReferenceMismatch)
	case wager.Failed:
		settle(t, tx, wager.PendingReference)
		err = tx.MarkFailed()
	}
	if err != nil {
		t.Fatalf("settle as %s: %v", status, err)
	}
}

func inStatus(t *testing.T, status wager.Status) *wager.Transaction {
	t.Helper()

	tx := created(t, refundArgs(t))
	settle(t, tx, status)
	return tx
}
