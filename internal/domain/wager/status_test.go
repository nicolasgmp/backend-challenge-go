package wager_test

import (
	"errors"
	"testing"

	"jungle-gaming-challeng/internal/domain/wager"
)

func TestParseStatus(t *testing.T) {
	tests := []struct {
		input    string
		want     wager.Status
		terminal bool
		wantErr  error
	}{
		{"PENDING", wager.Pending, false, nil},
		{"PENDING_REFERENCE", wager.PendingReference, false, nil},
		{"PROCESSED", wager.Processed, true, nil},
		{"REJECTED", wager.Rejected, true, nil},
		{"FAILED", wager.Failed, true, nil},
		{"DONE", "", false, wager.ErrInvalidStatus},
		{"", "", false, wager.ErrInvalidStatus},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := wager.ParseStatus(tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ParseStatus() = %q, want %q", got, tt.want)
			}
			if got.IsTerminal() != tt.terminal {
				t.Fatalf("IsTerminal() = %t, want %t", got.IsTerminal(), tt.terminal)
			}
		})
	}
}
