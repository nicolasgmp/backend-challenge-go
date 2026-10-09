package events

import (
	"encoding/json"
	"errors"
	"time"

	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
)

const (
	TypeWagerTransactionProcessed        = "WagerTransactionProcessed"
	TypeWagerTransactionRejected         = "WagerTransactionRejected"
	TypeWalletBalanceChanged             = "WalletBalanceChanged"
	TypeWagerTransactionPendingReference = "WagerTransactionPendingReference"
)

const version = 1

var ErrInvalidEvent = errors.New("events: invalid event")

type Origin struct {
	CorrelationID string
	CausationID   string
}

type Header struct {
	EventID       ids.EventID  `json:"eventId"`
	EventType     string       `json:"eventType"`
	AggregateID   string       `json:"aggregateId"`
	CorrelationID string       `json:"correlationId"`
	CausationID   string       `json:"causationId,omitempty"`
	OccurredAt    time.Time    `json:"occurredAt"`
	Version       int          `json:"version"`
	WalletID      ids.WalletID `json:"-"`
}

type Event struct {
	header Header
	data   any
}

var _ json.Marshaler = Event{}

func (e Event) Header() Header {
	return e.header
}

func (e Event) Data() any {
	return e.data
}

func (e Event) MarshalJSON() ([]byte, error) {
	if e.header.EventID.IsZero() {
		return nil, ErrInvalidEvent
	}
	return json.Marshal(struct {
		Header
		Data any `json:"data"`
	}{e.header, e.data})
}

func newEvent(eventType, aggregateID string, walletID ids.WalletID, origin Origin, data any) (Event, error) {
	if origin.CorrelationID == "" {
		return Event{}, ErrInvalidEvent
	}
	id, err := ids.NewEventID()
	if err != nil {
		return Event{}, err
	}
	return Event{
		header: Header{
			EventID:       id,
			EventType:     eventType,
			AggregateID:   aggregateID,
			CorrelationID: origin.CorrelationID,
			CausationID:   origin.CausationID,
			OccurredAt:    time.Now().UTC().Truncate(time.Microsecond),
			Version:       version,
			WalletID:      walletID,
		},
		data: data,
	}, nil
}

func notNegative(m money.Money) bool {
	return m.IsZero() || m.IsPositive()
}
