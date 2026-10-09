package sqs

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/app/apptest"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wallet"
	"jungle-gaming-challeng/internal/infra/observability"
	"jungle-gaming-challeng/internal/infra/observability/logtest"
)

const playerUUID = "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1"

type fixture struct {
	handler *Handler
	service *app.Service
	store   *apptest.Store
	metrics *apptest.Metrics
	logs    *logtest.Buffer
	wallet  wallet.State
}

func newFixture(t *testing.T, balance string) fixture {
	t.Helper()

	store := apptest.NewStore()
	metrics := &apptest.Metrics{}
	logs := &logtest.Buffer{}
	logger := observability.NewLogger(logs, slog.LevelInfo)
	service := &app.Service{
		Tx: store, Wallets: store.Wallets(), Transactions: store.Transactions(), Ledger: store.Ledger(), Outbox: store.Outbox(),
		Clock:   &apptest.Clock{Current: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)},
		Metrics: metrics, Logger: logger, ReferenceTTL: 5 * time.Minute,
	}

	currency, err := money.ParseCurrency("BRL")
	if err != nil {
		t.Fatalf("ParseCurrency: %v", err)
	}
	initial, err := money.Parse(balance, currency)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	player, err := ids.ParsePlayerID(playerUUID)
	if err != nil {
		t.Fatalf("ParsePlayerID: %v", err)
	}
	opened, err := service.OpenWallet(context.Background(), app.OpenWalletInput{PlayerID: player, InitialBalance: initial, CorrelationID: "open"})
	if err != nil {
		t.Fatalf("OpenWallet: %v", err)
	}

	return fixture{
		handler: &Handler{Tx: store, Inbox: store.Inbox(), Service: service, Metrics: metrics, Logger: logger},
		service: service, store: store, metrics: metrics, logs: logs, wallet: opened,
	}
}

func (f fixture) message(messageID, kind, amount, externalID string) string {
	return `{"messageId":"` + messageID + `","type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00.000Z",` +
		`"data":{"providerId":"provider-a","externalTransactionId":"` + externalID + `","idempotencyKey":"provider-a:` + externalID + `",` +
		`"playerId":"` + playerUUID + `","walletId":"` + f.wallet.ID.String() + `","roundId":"round-987","gameId":"fortune-chimp",` +
		`"kind":"` + kind + `","money":{"amount":"` + amount + `","currency":"BRL"}}}`
}

func (f fixture) balance(t *testing.T) string {
	t.Helper()

	state, err := f.service.GetWallet(context.Background(), f.wallet.ID)
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	return state.Balance.Amount()
}
