package app

import (
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
)

type RawOperation struct {
	ProviderID                     string
	ExternalTransactionID          string
	IdempotencyKey                 string
	PlayerID                       string
	WalletID                       string
	RoundID                        string
	GameID                         string
	Kind                           string
	Money                          *money.Money
	ReferenceExternalTransactionID string
}

func (raw RawOperation) Input() (SubmitInput, error) {
	var in SubmitInput
	if raw.Money == nil || raw.Kind == "" {
		return in, failure.InvalidInputError{Code: failure.MalformedRequest}
	}
	in.Money = *raw.Money

	var err error
	if in.External.ProviderID, err = ParseRequired(ids.ParseProviderID, raw.ProviderID); err != nil {
		return in, err
	}
	if in.External.ExternalID, err = ParseRequired(ids.ParseExternalTransactionID, raw.ExternalTransactionID); err != nil {
		return in, err
	}
	if in.PlayerID, err = ParseRequired(ids.ParsePlayerID, raw.PlayerID); err != nil {
		return in, err
	}
	if in.WalletID, err = ParseRequired(ids.ParseWalletID, raw.WalletID); err != nil {
		return in, err
	}
	if in.External.RoundID, err = ParseRequired(ids.ParseRoundID, raw.RoundID); err != nil {
		return in, err
	}
	if in.External.GameID, err = ParseRequired(ids.ParseGameID, raw.GameID); err != nil {
		return in, err
	}
	if raw.ReferenceExternalTransactionID != "" {
		if in.External.ReferenceExternalID, err = ParseRequired(ids.ParseExternalTransactionID, raw.ReferenceExternalTransactionID); err != nil {
			return in, err
		}
	}
	if in.Kind, err = wager.ParseKind(raw.Kind); err != nil {
		return in, failure.InvalidInputError{Code: failure.UnsupportedKind}
	}
	if raw.IdempotencyKey == "" {
		return in, failure.InvalidInputError{Code: failure.MissingIdempotencyKey}
	}
	if in.External.IdempotencyKey, err = ParseRequired(ids.ParseIdempotencyKey, raw.IdempotencyKey); err != nil {
		return in, err
	}
	return in, nil
}

func ParseRequired[T any](parse func(string) (T, error), value string) (T, error) {
	if value == "" {
		var none T
		return none, failure.InvalidInputError{Code: failure.MalformedRequest}
	}
	parsed, err := parse(value)
	if err != nil {
		return parsed, failure.InvalidInputError{Code: failure.InvalidIdentifier}
	}
	return parsed, nil
}
