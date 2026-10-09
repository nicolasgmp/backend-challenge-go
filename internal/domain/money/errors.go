package money

import "errors"

var (
	ErrInvalidAmount    = errors.New("money: invalid amount")
	ErrNegativeAmount   = errors.New("money: negative amount")
	ErrInvalidCurrency  = errors.New("money: invalid currency")
	ErrCurrencyMismatch = errors.New("money: currency mismatch")
	ErrOverflow         = errors.New("money: overflow")
	ErrUninitialized    = errors.New("money: uninitialized value")
)
