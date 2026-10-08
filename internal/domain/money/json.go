package money

import (
	"bytes"
	"encoding/json"
)

var (
	_ json.Marshaler   = Money{}
	_ json.Unmarshaler = (*Money)(nil)
)

type moneyJSON struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func (m Money) MarshalJSON() ([]byte, error) {
	if !m.currency.valid() {
		return nil, ErrUninitialized
	}
	return json.Marshal(moneyJSON{Amount: m.Amount(), Currency: m.currency.code})
}

func (m *Money) UnmarshalJSON(data []byte) error {
	var raw moneyJSON
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return ErrInvalidAmount
	}

	currency, err := ParseCurrency(raw.Currency)
	if err != nil {
		return err
	}
	parsed, err := Parse(raw.Amount, currency)
	if err != nil {
		return err
	}

	*m = parsed
	return nil
}
