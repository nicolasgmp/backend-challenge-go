package money

import "fmt"

type Currency struct {
	code string
}

func ParseCurrency(code string) (Currency, error) {
	switch code {
	case "BRL", "USD", "EUR":
		return Currency{code: code}, nil
	default:
		return Currency{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, code)
	}
}

func (c Currency) Code() string {
	return c.code
}

func (c Currency) valid() bool {
	return c.code != ""
}
