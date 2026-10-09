package app_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/app/apptest"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
	"jungle-gaming-challeng/internal/domain/wallet"
)

const (
	playerUUID = "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1"
	otherUUID  = "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a9"
	ttl        = 5 * time.Minute
)

var (
	errBoom = errors.New("boom")
	start   = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
)

type fixture struct {
	service *app.Service
	store   *apptest.Store
	metrics *apptest.Metrics
	clock   *apptest.Clock
	logs    *bytes.Buffer
}

func newFixture() fixture {
	store := apptest.NewStore()
	metrics := &apptest.Metrics{}
	clock := &apptest.Clock{Current: start}
	logs := &bytes.Buffer{}
	return fixture{
		service: &app.Service{
			Tx:           store,
			Wallets:      store.Wallets(),
			Transactions: store.Transactions(),
			Ledger:       store.Ledger(),
			Outbox:       store.Outbox(),
			Clock:        clock,
			Metrics:      metrics,
			Logger:       slog.New(slog.NewJSONHandler(logs, nil)),
			ReferenceTTL: ttl,
		},
		store:   store,
		metrics: metrics,
		clock:   clock,
		logs:    logs,
	}
}

func parsed[T any](t *testing.T, parse func(string) (T, error), text string) T {
	t.Helper()

	value, err := parse(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return value
}

func amount(t *testing.T, units int64, code string) money.Money {
	t.Helper()

	m, err := money.FromMinorUnits(units, parsed(t, money.ParseCurrency, code))
	if err != nil {
		t.Fatalf("FromMinorUnits(%d, %s): %v", units, code, err)
	}
	return m
}

func (f fixture) open(t *testing.T, units int64) wallet.State {
	t.Helper()

	state, err := f.service.OpenWallet(context.Background(), app.OpenWalletInput{
		PlayerID:       parsed(t, ids.ParsePlayerID, playerUUID),
		InitialBalance: amount(t, units, "BRL"),
		CorrelationID:  "correlation-open",
	})
	if err != nil {
		t.Fatalf("OpenWallet: %v", err)
	}
	return state
}

func operation(t *testing.T, w wallet.State, kind wager.Kind, units int64, externalID, reference string) app.SubmitInput {
	t.Helper()

	in := app.SubmitInput{
		Channel:  app.ChannelHTTP,
		Kind:     kind,
		WalletID: w.ID,
		PlayerID: w.PlayerID,
		Money:    amount(t, units, "BRL"),
		External: wager.External{
			ProviderID:     parsed(t, ids.ParseProviderID, "provider-a"),
			ExternalID:     parsed(t, ids.ParseExternalTransactionID, externalID),
			IdempotencyKey: parsed(t, ids.ParseIdempotencyKey, "key-"+externalID),
			RoundID:        parsed(t, ids.ParseRoundID, "round-1"),
			GameID:         parsed(t, ids.ParseGameID, "game-1"),
		},
		CorrelationID: "correlation-" + externalID,
	}
	if reference != "" {
		in.External.ReferenceExternalID = parsed(t, ids.ParseExternalTransactionID, reference)
	}
	return in
}

func (f fixture) submit(t *testing.T, in app.SubmitInput) wager.State {
	t.Helper()

	result, err := f.service.SubmitTransaction(context.Background(), in)
	if err != nil {
		t.Fatalf("SubmitTransaction(%s %s): %v", in.Kind, in.External.ExternalID, err)
	}
	return result.Transaction
}

func (f fixture) wallet(t *testing.T, id ids.WalletID) wallet.State {
	t.Helper()

	state, err := f.service.GetWallet(context.Background(), id)
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	return state
}

func (f fixture) eventTypes() []string {
	var types []string
	for _, event := range f.store.Events() {
		types = append(types, event.Header().EventType)
	}
	return types
}

type snapshot struct {
	balance      string
	version      int64
	entries      int
	events       int
	transactions int
}

func (f fixture) snapshot(t *testing.T, id ids.WalletID) snapshot {
	t.Helper()

	state := f.wallet(t, id)
	return snapshot{
		balance:      state.Balance.Amount(),
		version:      state.Version,
		entries:      len(f.store.Entries()),
		events:       len(f.store.Events()),
		transactions: len(f.store.TransactionStates()),
	}
}

func wantRejection(t *testing.T, state wager.State, code failure.Code) {
	t.Helper()

	if state.Status != wager.Rejected || state.FailureCode != code {
		t.Fatalf("status, failureCode = %s, %q, want REJECTED, %q", state.Status, state.FailureCode, code)
	}
}

func wantInvalidInput(t *testing.T, err error, code failure.Code) {
	t.Helper()

	if !errors.Is(err, failure.InvalidInputError{Code: code}) {
		t.Fatalf("err = %v, want invalid input %s", err, code)
	}
}

func wantConflict(t *testing.T, err error, code string) {
	t.Helper()

	if !errors.Is(err, app.ConflictError{Code: code}) {
		t.Fatalf("err = %v, want conflict %s", err, code)
	}
}
