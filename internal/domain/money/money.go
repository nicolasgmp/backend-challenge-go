package money

import (
	"cmp"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type Money struct {
	units    int64
	currency Currency
}

func Zero(c Currency) (Money, error) {
	return FromMinorUnits(0, c)
}

func FromMinorUnits(units int64, c Currency) (Money, error) {
	if !c.valid() {
		return Money{}, ErrUninitialized
	}
	return Money{units: units, currency: c}, nil
}

func Parse(amount string, c Currency) (Money, error) {
	if !c.valid() {
		return Money{}, ErrUninitialized
	}

	text, negative := strings.CutPrefix(amount, "-")
	whole, cents, hasPoint := strings.Cut(text, ".")
	if !hasPoint || len(cents) != 2 || !onlyDigits(whole) || !onlyDigits(cents) || hasLeadingZero(whole) {
		return Money{}, ErrInvalidAmount
	}
	if negative {
		return Money{}, ErrNegativeAmount
	}

	units, err := strconv.ParseInt(whole+cents, 10, 64)
	if err != nil {
		return Money{}, ErrOverflow
	}
	return Money{units: units, currency: c}, nil
}

func onlyDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func hasLeadingZero(whole string) bool {
	return len(whole) > 1 && whole[0] == '0'
}

func (m Money) MinorUnits() int64 {
	return m.units
}

func (m Money) Currency() Currency {
	return m.currency
}

func (m Money) Amount() string {
	sign := ""
	whole, cents := m.units/100, m.units%100
	if m.units < 0 {
		sign, whole, cents = "-", -whole, -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, whole, cents)
}

func (m Money) Add(other Money) (Money, error) {
	if err := m.sameCurrency(other); err != nil {
		return Money{}, err
	}
	if other.units > 0 && m.units > math.MaxInt64-other.units {
		return Money{}, ErrOverflow
	}
	if other.units < 0 && m.units < math.MinInt64-other.units {
		return Money{}, ErrOverflow
	}
	return Money{units: m.units + other.units, currency: m.currency}, nil
}

func (m Money) Sub(other Money) (Money, error) {
	if err := m.sameCurrency(other); err != nil {
		return Money{}, err
	}
	if other.units < 0 && m.units > math.MaxInt64+other.units {
		return Money{}, ErrOverflow
	}
	if other.units > 0 && m.units < math.MinInt64+other.units {
		return Money{}, ErrOverflow
	}
	return Money{units: m.units - other.units, currency: m.currency}, nil
}

func (m Money) Neg() (Money, error) {
	if !m.currency.valid() {
		return Money{}, ErrUninitialized
	}
	if m.units == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return Money{units: -m.units, currency: m.currency}, nil
}

func (m Money) Cmp(other Money) (int, error) {
	if err := m.sameCurrency(other); err != nil {
		return 0, err
	}
	return cmp.Compare(m.units, other.units), nil
}

func (m Money) Equal(other Money) bool {
	return m.currency.valid() && m == other
}

func (m Money) IsZero() bool {
	return m.currency.valid() && m.units == 0
}

func (m Money) IsPositive() bool {
	return m.currency.valid() && m.units > 0
}

func (m Money) IsNegative() bool {
	return m.currency.valid() && m.units < 0
}

func (m Money) sameCurrency(other Money) error {
	if !m.currency.valid() || !other.currency.valid() {
		return ErrUninitialized
	}
	if m.currency != other.currency {
		return ErrCurrencyMismatch
	}
	return nil
}
