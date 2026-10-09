package ids

import (
	"encoding"
	"strings"
	"unicode/utf8"
)

var _ encoding.TextMarshaler = text{}

const maxTextLength = 255

type text struct {
	value string
}

func (t text) String() string {
	return t.value
}

func (t text) IsZero() bool {
	return t.value == ""
}

func (t text) MarshalText() ([]byte, error) {
	return []byte(t.value), nil
}

type (
	ProviderID            struct{ text }
	ExternalTransactionID struct{ text }
	RoundID               struct{ text }
	GameID                struct{ text }
	IdempotencyKey        struct{ text }
)

func ParseProviderID(s string) (ProviderID, error) {
	v, err := parseText(s)
	return ProviderID{v}, err
}

func ParseExternalTransactionID(s string) (ExternalTransactionID, error) {
	v, err := parseText(s)
	return ExternalTransactionID{v}, err
}

func ParseRoundID(s string) (RoundID, error) {
	v, err := parseText(s)
	return RoundID{v}, err
}

func ParseGameID(s string) (GameID, error) {
	v, err := parseText(s)
	return GameID{v}, err
}

func ParseIdempotencyKey(s string) (IdempotencyKey, error) {
	v, err := parseText(s)
	return IdempotencyKey{v}, err
}

func parseText(s string) (text, error) {
	if s == "" || s != strings.TrimSpace(s) || utf8.RuneCountInString(s) > maxTextLength || !storable(s) {
		return text{}, ErrInvalid
	}
	return text{value: s}, nil
}

func storable(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
