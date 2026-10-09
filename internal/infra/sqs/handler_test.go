package sqs

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/app/apptest"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
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

func (f fixture) externalTransactions() []wager.State {
	var external []wager.State
	for _, state := range f.store.TransactionStates() {
		if state.Kind.IsExternal() {
			external = append(external, state)
		}
	}
	return external
}

func TestHandleValidMessage(t *testing.T) {
	f := newFixture(t, "1000.00")

	outcome := f.handler.Handle(context.Background(), f.message("msg-1", "BET", "25.00", "transaction-123"), "")
	if outcome != (Outcome{Action: Delete}) {
		t.Fatalf("outcome = %+v, want the message deleted", outcome)
	}

	stored := f.externalTransactions()
	if len(stored) != 1 || stored[0].Status != wager.Processed || f.balance(t) != "975.00" {
		t.Fatalf("transactions = %+v, balance %s, want one processed bet and 975.00", stored, f.balance(t))
	}
	if stored[0].External.IdempotencyKey.String() != "provider-a:transaction-123" || stored[0].CorrelationID != "msg-1" {
		t.Fatalf("transaction = %+v, want the key of the message and the message id as correlation", stored[0])
	}
	events := f.store.Events()
	if last := events[len(events)-1].Header(); last.CausationID != "msg-1" || last.CorrelationID != "msg-1" {
		t.Fatalf("event header = %+v, want the message id as causation and as correlation", last)
	}
	if !slices.Contains(f.metrics.Calls, "TransactionResult sqs BET PROCESSED ") {
		t.Fatalf("metrics = %v, want the result on the sqs channel", f.metrics.Calls)
	}
	if !strings.Contains(f.logs.String(), `"messageId":"msg-1"`) {
		t.Fatal("the logs of the operation lack the message id")
	}
	logtest.AssertNoLeak(t, f.logs.String(), "25.00", "975.00", "1000.00")
}

func TestHandleRedelivery(t *testing.T) {
	f := newFixture(t, "1000.00")
	body := f.message("msg-1", "BET", "25.00", "transaction-123")
	f.handler.Handle(context.Background(), body, "")
	entries, events := len(f.store.Entries()), len(f.store.Events())

	if outcome := f.handler.Handle(context.Background(), body, ""); outcome != (Outcome{Action: Delete}) {
		t.Fatalf("redelivery outcome = %+v, want the message deleted", outcome)
	}
	if f.balance(t) != "975.00" || len(f.store.Entries()) != entries || len(f.store.Events()) != events {
		t.Fatal("the redelivery applied the operation again")
	}
	if !slices.Contains(f.metrics.Calls, "Duplicate inbox") || slices.Contains(f.metrics.Calls, "Duplicate replay") {
		t.Fatalf("metrics = %v, want the duplicate recognised by the inbox, before the use case", f.metrics.Calls)
	}

	changed := f.message("msg-1", "BET", "30.00", "transaction-999")
	if outcome := f.handler.Handle(context.Background(), changed, ""); outcome != (Outcome{Action: DeadLetter, Reason: ReasonMessageIDReused}) {
		t.Fatalf("same message id with another body: outcome = %+v, want the dead-letter queue", outcome)
	}
	if f.balance(t) != "975.00" || len(f.externalTransactions()) != 1 {
		t.Fatal("a message with a reused id was executed")
	}
}

func TestHandleSameOperationByHTTPAndBySQS(t *testing.T) {
	f := newFixture(t, "1000.00")
	f.handler.Handle(context.Background(), f.message("msg-1", "BET", "25.00", "transaction-123"), "")

	value := f.externalTransactions()[0].Amount
	in, err := app.RawOperation{
		ProviderID: "provider-a", ExternalTransactionID: "transaction-123", IdempotencyKey: "provider-a:transaction-123",
		PlayerID: playerUUID, WalletID: f.wallet.ID.String(), RoundID: "round-987", GameID: "fortune-chimp",
		Kind: "BET", Money: &value,
	}.Input()
	if err != nil {
		t.Fatalf("Input: %v", err)
	}
	in.Channel, in.CorrelationID = app.ChannelHTTP, "http-request"

	result, err := f.service.SubmitTransaction(context.Background(), in)
	if err != nil {
		t.Fatalf("SubmitTransaction: %v", err)
	}
	if !result.IdempotentReplay || f.balance(t) != "975.00" || len(f.store.Entries()) != 2 {
		t.Fatalf("result = %+v, balance %s, want the HTTP send recognised as a replay of the message: same content hash", result, f.balance(t))
	}

	again := f.message("msg-2", "BET", "25.00", "transaction-123")
	if outcome := f.handler.Handle(context.Background(), again, ""); outcome.Action != Delete || f.balance(t) != "975.00" {
		t.Fatalf("same operation in another message: outcome = %+v, balance %s, want a replay", outcome, f.balance(t))
	}
}

func TestHandleDeadLetters(t *testing.T) {
	tests := []struct {
		name   string
		body   func(f fixture) string
		reason string
	}{
		{"not json", func(fixture) string { return "not json" }, ReasonInvalidMessage},
		{"missing message id", func(f fixture) string {
			return strings.Replace(f.message("msg-1", "BET", "25.00", "tx-1"), `"messageId":"msg-1",`, "", 1)
		}, ReasonInvalidMessage},
		{"missing occurredAt", func(f fixture) string {
			return strings.Replace(f.message("msg-1", "BET", "25.00", "tx-1"), `"occurredAt":"2026-09-08T12:00:00.000Z",`, "", 1)
		}, ReasonInvalidMessage},
		{"missing data", func(fixture) string {
			return `{"messageId":"msg-1","type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00.000Z"}`
		}, ReasonInvalidMessage},
		{"unknown envelope field", func(f fixture) string {
			return strings.Replace(f.message("msg-1", "BET", "25.00", "tx-1"), `"type"`, `"extra":1,"type"`, 1)
		}, ReasonInvalidMessage},
		{"unknown type", func(f fixture) string {
			return strings.Replace(f.message("msg-1", "BET", "25.00", "tx-1"), "WagerTransactionRequested", "SomethingElse", 1)
		}, ReasonUnknownType},
		{"unknown data field", func(f fixture) string {
			return strings.Replace(f.message("msg-1", "BET", "25.00", "tx-1"), `"kind"`, `"bonus":true,"kind"`, 1)
		}, "MALFORMED_REQUEST"},
		{"missing idempotency key", func(f fixture) string {
			return strings.Replace(f.message("msg-1", "BET", "25.00", "tx-1"), `"idempotencyKey":"provider-a:tx-1",`, "", 1)
		}, "MISSING_IDEMPOTENCY_KEY"},
		{"numeric amount", func(f fixture) string {
			return strings.Replace(f.message("msg-1", "BET", "25.00", "tx-1"), `"25.00"`, `25.00`, 1)
		}, "MALFORMED_REQUEST"},
		{"opening", func(f fixture) string { return f.message("msg-1", "OPENING", "25.00", "tx-1") }, "UNSUPPORTED_KIND"},
		{"bet of zero", func(f fixture) string { return f.message("msg-1", "BET", "0.00", "tx-1") }, "INVALID_AMOUNT_FOR_KIND"},
		{"unknown wallet", func(f fixture) string {
			return strings.Replace(f.message("msg-1", "BET", "25.00", "tx-1"), f.wallet.ID.String(), "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a9", 1)
		}, "WALLET_NOT_FOUND"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, "1000.00")

			outcome := f.handler.Handle(context.Background(), tt.body(f), "")
			if outcome != (Outcome{Action: DeadLetter, Reason: tt.reason}) {
				t.Fatalf("outcome = %+v, want the dead-letter queue with %s", outcome, tt.reason)
			}
			if len(f.externalTransactions()) != 0 || f.balance(t) != "1000.00" {
				t.Fatal("an invalid message changed the wallet or recorded a transaction")
			}

			valid := f.message("msg-1", "BET", "25.00", "tx-1")
			if next := f.handler.Handle(context.Background(), valid, ""); next.Action != Delete || f.balance(t) != "975.00" {
				t.Fatalf("corrected message with the same id: outcome = %+v, balance %s, want it processed", next, f.balance(t))
			}
		})
	}
}

func TestHandleTransientFailureIsRetried(t *testing.T) {
	failingCalls := []string{"inbox.Register", "wallets.GetForUpdate", "outbox.Insert", "inbox.Complete"}

	for _, call := range failingCalls {
		t.Run(call, func(t *testing.T) {
			f := newFixture(t, "1000.00")
			body := f.message("msg-1", "BET", "25.00", "tx-1")
			f.store.FailNext(call, app.ErrTransient)

			if outcome := f.handler.Handle(context.Background(), body, ""); outcome != (Outcome{Action: Retry}) {
				t.Fatalf("outcome = %+v, want a retry", outcome)
			}
			if f.balance(t) != "1000.00" || len(f.externalTransactions()) != 0 {
				t.Fatal("a failed handling left a movement or a transaction behind")
			}
			if !slices.Contains(f.metrics.Calls, "Retry consumer") {
				t.Fatalf("metrics = %v, want the retry counted", f.metrics.Calls)
			}

			if outcome := f.handler.Handle(context.Background(), body, ""); outcome != (Outcome{Action: Delete}) || f.balance(t) != "975.00" {
				t.Fatalf("redelivery outcome = %+v, balance %s, want the message processed once", outcome, f.balance(t))
			}
		})
	}
}
