package wager_test

import (
	"errors"
	"testing"

	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/wager"
)

func TestEffectOf(t *testing.T) {
	type pair struct {
		kind      wager.Kind
		reference wager.Kind
	}
	type effect struct {
		direction ledger.Direction
		moves     bool
	}

	allowed := map[pair]effect{
		{wager.Bet, ""}:                {ledger.Debit, true},
		{wager.Loss, ""}:               {"", false},
		{wager.Win, ""}:                {ledger.Credit, true},
		{wager.Win, wager.Bet}:         {ledger.Credit, true},
		{wager.Refund, wager.Bet}:      {ledger.Credit, true},
		{wager.Rollback, wager.Bet}:    {ledger.Credit, true},
		{wager.Rollback, wager.Win}:    {ledger.Debit, true},
		{wager.Rollback, wager.Refund}: {ledger.Debit, true},
	}
	kinds := []wager.Kind{wager.Bet, wager.Win, wager.Loss, wager.Refund, wager.Rollback}
	references := []wager.Kind{"", wager.Opening, wager.Bet, wager.Win, wager.Loss, wager.Refund, wager.Rollback}

	for _, kind := range kinds {
		for _, reference := range references {
			t.Run(string(kind)+" of "+string(reference), func(t *testing.T) {
				want, isAllowed := allowed[pair{kind, reference}]
				var wantErr error
				if !isAllowed {
					wantErr = failure.RejectionError{Code: failure.ReferenceKindNotAllowed}
				}

				direction, moves, err := wager.EffectOf(kind, reference)
				if !errors.Is(err, wantErr) {
					t.Fatalf("err = %v, want %v", err, wantErr)
				}
				if direction != want.direction || moves != want.moves {
					t.Fatalf("EffectOf() = %q, %t, want %q, %t", direction, moves, want.direction, want.moves)
				}
			})
		}
	}
}

func TestValidateReference(t *testing.T) {
	same := func(*args) {}
	mismatch := failure.RejectionError{Code: failure.ReferenceMismatch}

	tests := []struct {
		name            string
		operation       func(*args)
		reference       func(*args)
		referenceStatus wager.Status
		wantErr         error
	}{
		{"refund of a processed bet", same, same, wager.Processed, nil},
		{"reference still pending", same, same, wager.PendingReference, wager.ErrReferencePending},
		{"reference rejected", same, same, wager.Rejected, failure.RejectionError{Code: failure.ReferenceNotProcessed}},
		{"reference failed", same, same, wager.Failed, failure.RejectionError{Code: failure.ReferenceNotProcessed}},
		{"refund of a win", same, func(a *args) { a.kind = wager.Win }, wager.Processed,
			failure.RejectionError{Code: failure.ReferenceKindNotAllowed}},
		{"another provider", same, func(a *args) {
			a.external.ProviderID = parsed(t, ids.ParseProviderID, "provider-b")
		}, wager.Processed, mismatch},
		{"another player", same, func(a *args) {
			a.playerID = parsed(t, ids.ParsePlayerID, "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a9")
		}, wager.Processed, mismatch},
		{"another wallet", same, func(a *args) {
			a.walletID = parsed(t, ids.ParseWalletID, "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a9")
		}, wager.Processed, mismatch},
		{"win in another currency", func(a *args) { a.kind = wager.Win }, func(a *args) {
			a.amount = amount(t, 2500, "USD")
		}, wager.Processed, mismatch},
		{"another round", same, func(a *args) {
			a.external.RoundID = parsed(t, ids.ParseRoundID, "round-2")
		}, wager.Processed, mismatch},
		{"another amount", same, func(a *args) { a.amount = amount(t, 1000, "BRL") }, wager.Processed, mismatch},
		{"win with another amount", func(a *args) {
			a.kind = wager.Win
			a.amount = amount(t, 5000, "BRL")
		}, same, wager.Processed, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operationArgs := refundArgs(t)
			tt.operation(&operationArgs)
			referenceArgs := betArgs(t)
			tt.reference(&referenceArgs)

			operation := created(t, operationArgs)
			reference := created(t, referenceArgs)
			settle(t, reference, tt.referenceStatus)

			if err := wager.ValidateReference(operation, reference); !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
