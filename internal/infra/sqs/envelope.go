package sqs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/money"
)

const (
	MessageType = "WagerTransactionRequested"

	ReasonInvalidMessage  = "INVALID_MESSAGE"
	ReasonUnknownType     = "UNKNOWN_MESSAGE_TYPE"
	ReasonMessageIDReused = "MESSAGE_ID_REUSED"
	ReasonRetriesExceeded = "RETRIES_EXHAUSTED"
)

var errInvalidMessage = errors.New("sqs: invalid message")

type envelope struct {
	MessageID  string          `json:"messageId"`
	Type       string          `json:"type"`
	OccurredAt *time.Time      `json:"occurredAt"`
	Data       json.RawMessage `json:"data"`
}

type operationData struct {
	ProviderID                     string       `json:"providerId"`
	ExternalTransactionID          string       `json:"externalTransactionId"`
	IdempotencyKey                 string       `json:"idempotencyKey"`
	PlayerID                       string       `json:"playerId"`
	WalletID                       string       `json:"walletId"`
	RoundID                        string       `json:"roundId"`
	GameID                         string       `json:"gameId"`
	Kind                           string       `json:"kind"`
	Money                          *money.Money `json:"money"`
	ReferenceExternalTransactionID string       `json:"referenceExternalTransactionId"`
}

func parseEnvelope(body string) (envelope, string) {
	var parsed envelope
	if err := strictDecode([]byte(body), &parsed); err != nil {
		return envelope{}, ReasonInvalidMessage
	}
	if parsed.MessageID == "" || parsed.OccurredAt == nil || len(parsed.Data) == 0 {
		return envelope{}, ReasonInvalidMessage
	}
	if parsed.Type != MessageType {
		return envelope{}, ReasonUnknownType
	}
	return parsed, ""
}

func (e envelope) operation() (app.RawOperation, error) {
	var data operationData
	if err := strictDecode(e.Data, &data); err != nil {
		return app.RawOperation{}, err
	}
	return app.RawOperation{
		ProviderID:                     data.ProviderID,
		ExternalTransactionID:          data.ExternalTransactionID,
		IdempotencyKey:                 data.IdempotencyKey,
		PlayerID:                       data.PlayerID,
		WalletID:                       data.WalletID,
		RoundID:                        data.RoundID,
		GameID:                         data.GameID,
		Kind:                           data.Kind,
		Money:                          data.Money,
		ReferenceExternalTransactionID: data.ReferenceExternalTransactionID,
	}, nil
}

func strictDecode(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || decoder.More() {
		return errInvalidMessage
	}
	return nil
}

func bodyHash(body string) string {
	digest := sha256.Sum256([]byte(body))
	return hex.EncodeToString(digest[:])
}
