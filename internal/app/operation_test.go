package app_test

import (
	"testing"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
)

func rawBet(t *testing.T) app.RawOperation {
	t.Helper()

	value := amount(t, 2500, "BRL")
	return app.RawOperation{
		ProviderID:            "provider-a",
		ExternalTransactionID: "transaction-123",
		IdempotencyKey:        "provider-a:transaction-123",
		PlayerID:              playerUUID,
		WalletID:              otherUUID,
		RoundID:               "round-987",
		GameID:                "fortune-chimp",
		Kind:                  "BET",
		Money:                 &value,
	}
}

func TestRawOperationInput(t *testing.T) {
	raw := rawBet(t)
	raw.Kind = "REFUND"
	raw.ReferenceExternalTransactionID = "transaction-100"

	in, err := raw.Input()
	if err != nil {
		t.Fatalf("Input: %v", err)
	}
	if in.Kind != wager.Refund || in.Money.Amount() != "25.00" || in.WalletID.String() != otherUUID || in.PlayerID.String() != playerUUID {
		t.Fatalf("input = %+v, want the refund of 25.00 for the wallet and the player", in)
	}
	external := in.External
	if external.ProviderID.String() != "provider-a" || external.ExternalID.String() != "transaction-123" || external.IdempotencyKey.String() != "provider-a:transaction-123" {
		t.Fatalf("external = %+v, want the provider, the external id and the key exactly as received", external)
	}
	if external.RoundID.String() != "round-987" || external.GameID.String() != "fortune-chimp" || external.ReferenceExternalID.String() != "transaction-100" {
		t.Fatalf("external = %+v, want the round, the game and the reference", external)
	}
}

func TestRawOperationInputRejects(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*app.RawOperation)
		code  failure.Code
	}{
		{"missing provider", func(r *app.RawOperation) { r.ProviderID = "" }, failure.MalformedRequest},
		{"missing external id", func(r *app.RawOperation) { r.ExternalTransactionID = "" }, failure.MalformedRequest},
		{"missing player", func(r *app.RawOperation) { r.PlayerID = "" }, failure.MalformedRequest},
		{"missing wallet", func(r *app.RawOperation) { r.WalletID = "" }, failure.MalformedRequest},
		{"missing round", func(r *app.RawOperation) { r.RoundID = "" }, failure.MalformedRequest},
		{"missing game", func(r *app.RawOperation) { r.GameID = "" }, failure.MalformedRequest},
		{"missing kind", func(r *app.RawOperation) { r.Kind = "" }, failure.MalformedRequest},
		{"missing money", func(r *app.RawOperation) { r.Money = nil }, failure.MalformedRequest},
		{"malformed wallet id", func(r *app.RawOperation) { r.WalletID = "not-a-uuid" }, failure.InvalidIdentifier},
		{"malformed player id", func(r *app.RawOperation) { r.PlayerID = "not-a-uuid" }, failure.InvalidIdentifier},
		{"reference with surrounding spaces", func(r *app.RawOperation) { r.ReferenceExternalTransactionID = " tx " }, failure.InvalidIdentifier},
		{"unknown kind", func(r *app.RawOperation) { r.Kind = "BONUS" }, failure.UnsupportedKind},
		{"missing idempotency key", func(r *app.RawOperation) { r.IdempotencyKey = "" }, failure.MissingIdempotencyKey},
		{"uninitialized money", func(r *app.RawOperation) { r.Money = &money.Money{} }, failure.InvalidMoney},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := rawBet(t)
			tt.apply(&raw)

			in, err := raw.Input()
			if err == nil {
				_, err = newFixture().service.SubmitTransaction(t.Context(), in)
			}
			wantInvalidInput(t, err, tt.code)
		})
	}
}
