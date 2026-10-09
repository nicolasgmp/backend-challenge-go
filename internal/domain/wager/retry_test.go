package wager_test

import (
	"testing"
	"time"

	"jungle-gaming-challeng/internal/domain/wager"
)

func TestNextAttempt(t *testing.T) {
	expiresAt := longAgo.Add(5 * time.Minute)

	tests := []struct {
		name     string
		attempts int
		now      time.Time
		want     time.Time
	}{
		{"first", 0, longAgo, longAgo.Add(time.Second)},
		{"second", 1, longAgo, longAgo.Add(2 * time.Second)},
		{"third", 2, longAgo, longAgo.Add(4 * time.Second)},
		{"fourth", 3, longAgo, longAgo.Add(8 * time.Second)},
		{"fifth", 4, longAgo, longAgo.Add(16 * time.Second)},
		{"sixth is capped", 5, longAgo, longAgo.Add(30 * time.Second)},
		{"many attempts stay capped", 500, longAgo, longAgo.Add(30 * time.Second)},
		{"lands exactly on the deadline", 5, expiresAt.Add(-30 * time.Second), expiresAt},
		{"never after the deadline", 5, expiresAt.Add(-10 * time.Second), expiresAt},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wager.NextAttempt(tt.attempts, tt.now, expiresAt)
			if !got.Equal(tt.want) {
				t.Fatalf("NextAttempt() = %s, want %s", got, tt.want)
			}
		})
	}
}
