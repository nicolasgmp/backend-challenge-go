package failure_test

import (
	"errors"
	"fmt"
	"testing"

	"jungle-gaming-challeng/internal/domain/failure"
)

func TestParseCode(t *testing.T) {
	known := []string{
		"MALFORMED_REQUEST",
		"INVALID_IDENTIFIER",
		"INVALID_MONEY",
		"MISSING_IDEMPOTENCY_KEY",
		"UNSUPPORTED_KIND",
		"INVALID_AMOUNT_FOR_KIND",
		"MISSING_REFERENCE",
		"UNEXPECTED_REFERENCE",
		"INVALID_CURSOR",
		"WALLET_NOT_FOUND",
		"TRANSACTION_NOT_FOUND",
		"INSUFFICIENT_FUNDS",
		"REVERSAL_INSUFFICIENT_FUNDS",
		"BALANCE_OVERFLOW",
		"PLAYER_WALLET_MISMATCH",
		"CURRENCY_MISMATCH",
		"REFERENCE_NOT_FOUND",
		"REFERENCE_NOT_PROCESSED",
		"REFERENCE_MISMATCH",
		"REFERENCE_KIND_NOT_ALLOWED",
		"REFERENCE_ALREADY_REVERSED",
		"PROCESSING_FAILED",
	}

	for _, text := range known {
		t.Run(text, func(t *testing.T) {
			got, err := failure.ParseCode(text)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if string(got) != text {
				t.Fatalf("ParseCode() = %q, want %q", got, text)
			}
		})
	}

	for _, text := range []string{"NOT_A_CODE", "insufficient_funds", ""} {
		t.Run("unknown "+text, func(t *testing.T) {
			if _, err := failure.ParseCode(text); !errors.Is(err, failure.ErrUnknownCode) {
				t.Fatalf("err = %v, want %v", err, failure.ErrUnknownCode)
			}
		})
	}
}

func TestRejectionError(t *testing.T) {
	wrapped := fmt.Errorf("apply bet: %w", failure.RejectionError{Code: failure.InsufficientFunds})

	var rejection failure.RejectionError
	if !errors.As(wrapped, &rejection) {
		t.Fatal("errors.As to RejectionError = false, want true")
	}
	if rejection.Code != failure.InsufficientFunds {
		t.Fatalf("Code = %q, want %q", rejection.Code, failure.InsufficientFunds)
	}

	var invalid failure.InvalidInputError
	if errors.As(wrapped, &invalid) {
		t.Fatal("errors.As to InvalidInputError = true, want false")
	}
}

func TestInvalidInputError(t *testing.T) {
	wrapped := fmt.Errorf("read request: %w", failure.InvalidInputError{Code: failure.InvalidMoney})

	var invalid failure.InvalidInputError
	if !errors.As(wrapped, &invalid) {
		t.Fatal("errors.As to InvalidInputError = false, want true")
	}
	if invalid.Code != failure.InvalidMoney {
		t.Fatalf("Code = %q, want %q", invalid.Code, failure.InvalidMoney)
	}

	var rejection failure.RejectionError
	if errors.As(wrapped, &rejection) {
		t.Fatal("errors.As to RejectionError = true, want false")
	}
}
