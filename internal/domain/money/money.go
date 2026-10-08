package money

import (
	"cmp"
	"fmt"
	"math"
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

	digits, negative := strings.CutPrefix(amount, "-")
	if !strictFormat(digits) {
		return Money{}, ErrInvalidAmount
	}
	if negative {
		return Money{}, ErrNegativeAmount
	}

	var units int64
	for i := 0; i < len(digits); i++ {
		if digits[i] == '.' {
			continue
		}
		d := int64(digits[i] - '0')
		if units > (math.MaxInt64-d)/10 {
			return Money{}, ErrOverflow
		}
		units = units*10 + d
	}

	return Money{units: units, currency: c}, nil
}

func strictFormat(s string) bool {
	point := len(s) - 3
	if point < 1 || s[point] != '.' {
		return false
	}
	if point > 1 && s[0] == '0' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if i == point {
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func (m Money) MinorUnits() int64 {
	return m.units
}

func (m Money) Currency() Currency {
	return m.currency
}

func (m Money) Amount() string {
	sign := ""
	abs := uint64(m.units)
	if m.units < 0 {
		sign = "-"
		abs = -abs
	}
	return fmt.Sprintf("%s%d.%02d", sign, abs/100, abs%100)
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
