package wager

import "time"

const (
	firstRetryDelay = time.Second
	maxRetryDelay   = 30 * time.Second
)

func NextAttempt(attempts int, now, expiresAt time.Time) time.Time {
	delay := firstRetryDelay
	for i := 0; i < attempts && delay < maxRetryDelay; i++ {
		delay *= 2
	}

	next := now.Add(min(delay, maxRetryDelay))
	if next.After(expiresAt) {
		return expiresAt
	}
	return next
}
