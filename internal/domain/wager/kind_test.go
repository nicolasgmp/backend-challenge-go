package wager_test

import (
	"errors"
	"testing"

	"jungle-gaming-challeng/internal/domain/wager"
)

func TestParseKind(t *testing.T) {
	tests := []struct {
		input    string
		want     wager.Kind
		external bool
		wantErr  error
	}{
		{"OPENING", wager.Opening, false, nil},
		{"BET", wager.Bet, true, nil},
		{"WIN", wager.Win, true, nil},
		{"LOSS", wager.Loss, true, nil},
		{"REFUND", wager.Refund, true, nil},
		{"ROLLBACK", wager.Rollback, true, nil},
		{"BONUS", "", false, wager.ErrInvalidKind},
		{"bet", "", false, wager.ErrInvalidKind},
		{"", "", false, wager.ErrInvalidKind},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := wager.ParseKind(tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ParseKind() = %q, want %q", got, tt.want)
			}
			if got.IsExternal() != tt.external {
				t.Fatalf("IsExternal() = %t, want %t", got.IsExternal(), tt.external)
			}
		})
	}
}
