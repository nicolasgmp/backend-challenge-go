package ledger

import (
	"errors"
	"fmt"
	"time"

	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
)

type Direction string

const (
	Debit  Direction = "DEBIT"
	Credit Direction = "CREDIT"
)

var (
	ErrInvalidDirection = errors.New("ledger: invalid direction")
	ErrInvalidEntry     = errors.New("ledger: invalid entry")
)

func ParseDirection(s string) (Direction, error) {
	switch direction := Direction(s); direction {
	case Debit, Credit:
		return direction, nil
	default:
		return "", ErrInvalidDirection
	}
}

type Fields struct {
	WalletID      ids.WalletID
	TransactionID ids.TransactionID
	Direction     Direction
	Amount        money.Money
	BalanceBefore money.Money
	BalanceAfter  money.Money
	WalletVersion int64
}

type Entry struct {
	id        ids.EntryID
	fields    Fields
	createdAt time.Time
}

func NewEntry(fields Fields) (Entry, error) {
	id, err := ids.NewEntryID()
	if err != nil {
		return Entry{}, err
	}
	return Rehydrate(id, fields, time.Now().UTC().Truncate(time.Microsecond))
}

func Rehydrate(id ids.EntryID, fields Fields, createdAt time.Time) (Entry, error) {
	if id.IsZero() || createdAt.IsZero() {
		return Entry{}, fmt.Errorf("%w: missing id or creation time", ErrInvalidEntry)
	}
	if err := fields.validate(); err != nil {
		return Entry{}, err
	}
	return Entry{id: id, fields: fields, createdAt: createdAt}, nil
}

func (e Entry) ID() ids.EntryID {
	return e.id
}

func (e Entry) Fields() Fields {
	return e.fields
}

func (e Entry) CreatedAt() time.Time {
	return e.createdAt
}

func (f Fields) validate() error {
	if f.WalletID.IsZero() || f.TransactionID.IsZero() {
		return fmt.Errorf("%w: missing wallet or transaction", ErrInvalidEntry)
	}
	if f.WalletVersion < 1 {
		return fmt.Errorf("%w: wallet version below 1", ErrInvalidEntry)
	}
	if !f.Amount.IsPositive() {
		return fmt.Errorf("%w: amount must be positive", ErrInvalidEntry)
	}
	if f.BalanceBefore.IsNegative() || f.BalanceAfter.IsNegative() {
		return fmt.Errorf("%w: negative balance", ErrInvalidEntry)
	}

	expected, err := f.expectedBalanceAfter()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidEntry, err)
	}
	if !expected.Equal(f.BalanceAfter) {
		return fmt.Errorf("%w: balance after does not match the movement", ErrInvalidEntry)
	}
	return nil
}

func (f Fields) expectedBalanceAfter() (money.Money, error) {
	switch f.Direction {
	case Credit:
		return f.BalanceBefore.Add(f.Amount)
	case Debit:
		return f.BalanceBefore.Sub(f.Amount)
	default:
		return money.Money{}, ErrInvalidDirection
	}
}
