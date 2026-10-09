package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
)

const envelopeStart = `{
	"eventId": "0192f2a0-0000-7000-8000-000000000001",`

const refundJSON = `
		"transactionId": "0192f298-345e-7e38-af88-e43f851a819d",
		"walletId": "0192f291-27dd-7d3f-8071-5f8685deef37",
		"playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
		"kind": "REFUND",
		"money": {"amount": "25.00", "currency": "BRL"},
		"providerId": "provider-a",
		"externalTransactionId": "transaction-456",
		"roundId": "round-987",
		"gameId": "fortune-chimp",
		"referenceExternalTransactionId": "transaction-123",`

func fixed(t *testing.T, event Event, err error) Event {
	t.Helper()

	if err != nil {
		t.Fatalf("build event: %v", err)
	}
	event.header.EventID = parsed(t, ids.ParseEventID, "0192f2a0-0000-7000-8000-000000000001")
	event.header.OccurredAt = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	return event
}

func TestEventJSON(t *testing.T) {
	processed, err := NewWagerTransactionProcessed(origin, refund(t), amount(t, 100000))
	processed = fixed(t, processed, err)

	openingProcessed, err := NewWagerTransactionProcessed(Origin{CorrelationID: "correlation-1"}, opening(t), amount(t, 100000))
	openingProcessed = fixed(t, openingProcessed, err)

	rejected, err := NewWagerTransactionRejected(origin, refund(t), failure.ReferenceMismatch)
	rejected = fixed(t, rejected, err)

	pending, err := NewWagerTransactionPendingReference(origin, refund(t), deadline)
	pending = fixed(t, pending, err)

	changed, err := NewWalletBalanceChanged(origin, debit(t))
	changed = fixed(t, changed, err)

	tests := []struct {
		name  string
		event Event
		want  string
	}{
		{"processed", processed, envelopeStart + `
	"eventType": "WagerTransactionProcessed",
	"aggregateId": "0192f298-345e-7e38-af88-e43f851a819d",
	"correlationId": "correlation-1",
	"causationId": "message-1",
	"occurredAt": "2026-01-01T12:00:00Z",
	"version": 1,
	"data": {` + refundJSON + `
		"balance": {"amount": "1000.00", "currency": "BRL"}
	}
}`},
		{"opening without provider data and without causation", openingProcessed, envelopeStart + `
	"eventType": "WagerTransactionProcessed",
	"aggregateId": "0192f298-345e-7e38-af88-e43f851a819d",
	"correlationId": "correlation-1",
	"occurredAt": "2026-01-01T12:00:00Z",
	"version": 1,
	"data": {
		"transactionId": "0192f298-345e-7e38-af88-e43f851a819d",
		"walletId": "0192f291-27dd-7d3f-8071-5f8685deef37",
		"playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
		"kind": "OPENING",
		"money": {"amount": "1000.00", "currency": "BRL"},
		"balance": {"amount": "1000.00", "currency": "BRL"}
	}
}`},
		{"rejected", rejected, envelopeStart + `
	"eventType": "WagerTransactionRejected",
	"aggregateId": "0192f298-345e-7e38-af88-e43f851a819d",
	"correlationId": "correlation-1",
	"causationId": "message-1",
	"occurredAt": "2026-01-01T12:00:00Z",
	"version": 1,
	"data": {` + refundJSON + `
		"failureCode": "REFERENCE_MISMATCH"
	}
}`},
		{"pending reference", pending, envelopeStart + `
	"eventType": "WagerTransactionPendingReference",
	"aggregateId": "0192f298-345e-7e38-af88-e43f851a819d",
	"correlationId": "correlation-1",
	"causationId": "message-1",
	"occurredAt": "2026-01-01T12:00:00Z",
	"version": 1,
	"data": {` + refundJSON + `
		"referenceExpiresAt": "2026-01-01T12:05:00Z"
	}
}`},
		{"balance changed", changed, envelopeStart + `
	"eventType": "WalletBalanceChanged",
	"aggregateId": "0192f291-27dd-7d3f-8071-5f8685deef37",
	"correlationId": "correlation-1",
	"causationId": "message-1",
	"occurredAt": "2026-01-01T12:00:00Z",
	"version": 1,
	"data": {
		"walletId": "0192f291-27dd-7d3f-8071-5f8685deef37",
		"transactionId": "0192f298-345e-7e38-af88-e43f851a819d",
		"direction": "DEBIT",
		"money": {"amount": "25.00", "currency": "BRL"},
		"balanceBefore": {"amount": "1000.00", "currency": "BRL"},
		"balanceAfter": {"amount": "975.00", "currency": "BRL"},
		"walletVersion": 2
	}
}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.event)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			var want bytes.Buffer
			if err := json.Compact(&want, []byte(tt.want)); err != nil {
				t.Fatalf("Compact: %v", err)
			}
			if string(got) != want.String() {
				t.Fatalf("Marshal =\n%s\nwant\n%s", got, want.String())
			}
		})
	}
}

func TestZeroEventDoesNotMarshal(t *testing.T) {
	if _, err := json.Marshal(Event{}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("err = %v, want %v", err, ErrInvalidEvent)
	}
}
