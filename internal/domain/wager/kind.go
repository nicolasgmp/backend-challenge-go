package wager

import "errors"

type Kind string

const (
	Opening  Kind = "OPENING"
	Bet      Kind = "BET"
	Win      Kind = "WIN"
	Loss     Kind = "LOSS"
	Refund   Kind = "REFUND"
	Rollback Kind = "ROLLBACK"
)

var ErrInvalidKind = errors.New("wager: invalid kind")

func ParseKind(s string) (Kind, error) {
	kind := Kind(s)
	if kind == Opening || kind.IsExternal() {
		return kind, nil
	}
	return "", ErrInvalidKind
}

func (k Kind) IsExternal() bool {
	switch k {
	case Bet, Win, Loss, Refund, Rollback:
		return true
	default:
		return false
	}
}
