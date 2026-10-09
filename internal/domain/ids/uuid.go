package ids

import (
	"encoding"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var ErrInvalid = errors.New("ids: invalid identifier")

var _ encoding.TextMarshaler = id{}

type id struct {
	value uuid.UUID
}

func (i id) String() string {
	return i.value.String()
}

func (i id) IsZero() bool {
	return i.value == uuid.Nil
}

func (i id) MarshalText() ([]byte, error) {
	return []byte(i.value.String()), nil
}

type (
	WalletID      struct{ id }
	PlayerID      struct{ id }
	TransactionID struct{ id }
	EntryID       struct{ id }
	EventID       struct{ id }
)

func ParseWalletID(s string) (WalletID, error) {
	v, err := parse(s)
	return WalletID{v}, err
}

func ParsePlayerID(s string) (PlayerID, error) {
	v, err := parse(s)
	return PlayerID{v}, err
}

func ParseTransactionID(s string) (TransactionID, error) {
	v, err := parse(s)
	return TransactionID{v}, err
}

func ParseEntryID(s string) (EntryID, error) {
	v, err := parse(s)
	return EntryID{v}, err
}

func ParseEventID(s string) (EventID, error) {
	v, err := parse(s)
	return EventID{v}, err
}

func NewWalletID() (WalletID, error) {
	v, err := generate()
	return WalletID{v}, err
}

func NewTransactionID() (TransactionID, error) {
	v, err := generate()
	return TransactionID{v}, err
}

func NewEntryID() (EntryID, error) {
	v, err := generate()
	return EntryID{v}, err
}

func NewEventID() (EventID, error) {
	v, err := generate()
	return EventID{v}, err
}

func parse(s string) (id, error) {
	value, err := uuid.Parse(s)
	if err != nil || value == uuid.Nil {
		return id{}, ErrInvalid
	}
	return id{value: value}, nil
}

func generate() (id, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return id{}, fmt.Errorf("ids: generate: %w", err)
	}
	return id{value: value}, nil
}
