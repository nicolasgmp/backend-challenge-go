package payloadhash

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
)

type Fields struct {
	ExternalTransactionID          ids.ExternalTransactionID `json:"externalTransactionId"`
	GameID                         ids.GameID                `json:"gameId"`
	Kind                           wager.Kind                `json:"kind"`
	Money                          money.Money               `json:"money"`
	PlayerID                       ids.PlayerID              `json:"playerId"`
	ProviderID                     ids.ProviderID            `json:"providerId"`
	ReferenceExternalTransactionID ids.ExternalTransactionID `json:"referenceExternalTransactionId,omitzero"`
	RoundID                        ids.RoundID               `json:"roundId"`
	WalletID                       ids.WalletID              `json:"walletId"`
}

func Canonical(fields Fields) ([]byte, error) {
	return json.Marshal(fields)
}

func Sum(fields Fields) (string, error) {
	canonical, err := Canonical(fields)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}
