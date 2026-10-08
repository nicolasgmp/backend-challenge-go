package money

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func (m Money) MarshalJSON() ([]byte, error) {
	if !m.currency.valid() {
		return nil, ErrUninitialized
	}
	return json.Marshal(struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}{
		Amount:   m.Amount(),
		Currency: m.currency.code,
	})
}

func (m *Money) UnmarshalJSON(data []byte) error {
	var raw struct {
		Amount   *string `json:"amount"`
		Currency *string `json:"currency"`
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return fmt.Errorf("%w: malformed money object", ErrInvalidAmount)
	}
	if raw.Amount == nil {
		return fmt.Errorf("%w: amount is required", ErrInvalidAmount)
	}
	if raw.Currency == nil {
		return fmt.Errorf("%w: currency is required", ErrInvalidCurrency)
	}

	currency, err := ParseCurrency(*raw.Currency)
	if err != nil {
		return err
	}
	parsed, err := Parse(*raw.Amount, currency)
	if err != nil {
		return err
	}

	*m = parsed
	return nil
}
