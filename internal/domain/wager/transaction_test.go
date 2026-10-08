package wager_test

import (
	"errors"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
	"jungle-gaming-challeng/internal/domain/wager"
)

func TestNewExternal(t *testing.T) {
	a := refundArgs(t)

	tx, err := a.create()
	if err != nil {
		t.Fatalf("NewExternal: %v", err)
	}

	state := tx.State()
	want := wager.State{
		ID:            state.ID,
		Kind:          wager.Refund,
		Status:        wager.Pending,
		WalletID:      a.walletID,
		PlayerID:      a.playerID,
		Amount:        a.amount,
		External:      a.external,
		CorrelationID: a.correlationID,
		CreatedAt:     state.CreatedAt,
		UpdatedAt:     state.CreatedAt,
	}
	if state != want {
		t.Fatalf("State() = %+v, want %+v", state, want)
	}
	if state.ID.IsZero() || state.CreatedAt.IsZero() {
		t.Fatal("NewExternal did not set the id or the creation time")
	}
	if state.CreatedAt != state.CreatedAt.UTC().Truncate(time.Microsecond) {
		t.Fatalf("CreatedAt = %s, want UTC with microsecond precision", state.CreatedAt)
	}
}

func TestNewExternalRejectsMissingFieldsAndInternalKinds(t *testing.T) {
	unsupported := failure.InvalidInputError{Code: failure.UnsupportedKind}

	tests := []struct {
		name    string
		apply   func(*args)
		wantErr error
	}{
		{"missing wallet", func(a *args) { a.walletID = ids.WalletID{} }, wager.ErrInvalidTransaction},
		{"missing player", func(a *args) { a.playerID = ids.PlayerID{} }, wager.ErrInvalidTransaction},
		{"missing provider", func(a *args) { a.external.ProviderID = ids.ProviderID{} }, wager.ErrInvalidTransaction},
		{"missing external id", func(a *args) { a.external.ExternalID = ids.ExternalTransactionID{} }, wager.ErrInvalidTransaction},
		{"missing idempotency key", func(a *args) { a.external.IdempotencyKey = ids.IdempotencyKey{} }, wager.ErrInvalidTransaction},
		{"missing payload hash", func(a *args) { a.external.PayloadHash = "" }, wager.ErrInvalidTransaction},
		{"missing round", func(a *args) { a.external.RoundID = ids.RoundID{} }, wager.ErrInvalidTransaction},
		{"missing game", func(a *args) { a.external.GameID = ids.GameID{} }, wager.ErrInvalidTransaction},
		{"missing correlation id", func(a *args) { a.correlationID = "" }, wager.ErrInvalidTransaction},
		{"opening", func(a *args) { a.kind = wager.Opening }, unsupported},
		{"unknown kind", func(a *args) { a.kind = wager.Kind("BONUS") }, unsupported},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := betArgs(t)
			tt.apply(&a)

			tx, err := a.create()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tx != nil {
				t.Fatalf("NewExternal returned %+v with an error", tx)
			}
		})
	}
}

func TestNewExternalAmountPolicy(t *testing.T) {
	invalid := failure.InvalidInputError{Code: failure.InvalidAmountForKind}

	tests := []struct {
		name    string
		kind    wager.Kind
		amount  money.Money
		wantErr error
	}{
		{"bet of zero", wager.Bet, amount(t, 0, "BRL"), invalid},
		{"bet above zero", wager.Bet, amount(t, 2500, "BRL"), nil},
		{"win of zero", wager.Win, amount(t, 0, "BRL"), invalid},
		{"win above zero", wager.Win, amount(t, 2500, "BRL"), nil},
		{"loss of zero", wager.Loss, amount(t, 0, "BRL"), nil},
		{"loss above zero", wager.Loss, amount(t, 2500, "BRL"), invalid},
		{"refund of zero", wager.Refund, amount(t, 0, "BRL"), invalid},
		{"refund above zero", wager.Refund, amount(t, 2500, "BRL"), nil},
		{"rollback of zero", wager.Rollback, amount(t, 0, "BRL"), invalid},
		{"rollback above zero", wager.Rollback, amount(t, 2500, "BRL"), nil},
		{"negative bet", wager.Bet, amount(t, -2500, "BRL"), invalid},
		{"uninitialized amount", wager.Bet, money.Money{}, invalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := argsFor(t, tt.kind)
			a.amount = tt.amount

			if _, err := a.create(); !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewExternalReferencePolicy(t *testing.T) {
	missing := failure.InvalidInputError{Code: failure.MissingReference}
	unexpected := failure.InvalidInputError{Code: failure.UnexpectedReference}
	none := ids.ExternalTransactionID{}
	some := parsed(t, ids.ParseExternalTransactionID, "tx-0")

	tests := []struct {
		name      string
		kind      wager.Kind
		reference ids.ExternalTransactionID
		wantErr   error
	}{
		{"bet without reference", wager.Bet, none, nil},
		{"bet with reference", wager.Bet, some, unexpected},
		{"win without reference", wager.Win, none, nil},
		{"win with reference", wager.Win, some, nil},
		{"loss without reference", wager.Loss, none, nil},
		{"loss with reference", wager.Loss, some, unexpected},
		{"refund without reference", wager.Refund, none, missing},
		{"refund with reference", wager.Refund, some, nil},
		{"rollback without reference", wager.Rollback, none, missing},
		{"rollback with reference", wager.Rollback, some, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := argsFor(t, tt.kind)
			a.external.ReferenceExternalID = tt.reference

			if _, err := a.create(); !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewOpening(t *testing.T) {
	a := betArgs(t)
	initial := amount(t, 100000, "BRL")

	tx, err := wager.NewOpening(a.walletID, a.playerID, initial, a.correlationID)
	if err != nil {
		t.Fatalf("NewOpening: %v", err)
	}

	state := tx.State()
	if state.Kind != wager.Opening || state.Status != wager.Processed || state.External != (wager.External{}) {
		t.Fatalf("State() = %+v, want a processed opening without provider data", state)
	}
	if state.WalletID != a.walletID || state.PlayerID != a.playerID || !state.Amount.Equal(initial) {
		t.Fatalf("State() = %+v, want the given wallet, player and amount", state)
	}
	if state.CorrelationID != a.correlationID {
		t.Fatalf("CorrelationID = %q, want %q", state.CorrelationID, a.correlationID)
	}
	if !state.ResultBalance.Equal(initial) || state.CompletedAt.IsZero() {
		t.Fatalf("ResultBalance, CompletedAt = %+v, %s, want the amount and a completion time", state.ResultBalance, state.CompletedAt)
	}
}

func TestNewOpeningRejects(t *testing.T) {
	a := betArgs(t)

	tests := []struct {
		name          string
		walletID      ids.WalletID
		playerID      ids.PlayerID
		amount        money.Money
		correlationID string
	}{
		{"zero amount", a.walletID, a.playerID, amount(t, 0, "BRL"), a.correlationID},
		{"uninitialized amount", a.walletID, a.playerID, money.Money{}, a.correlationID},
		{"missing wallet", ids.WalletID{}, a.playerID, amount(t, 100000, "BRL"), a.correlationID},
		{"missing player", a.walletID, ids.PlayerID{}, amount(t, 100000, "BRL"), a.correlationID},
		{"missing correlation id", a.walletID, a.playerID, amount(t, 100000, "BRL"), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := wager.NewOpening(tt.walletID, tt.playerID, tt.amount, tt.correlationID)
			if !errors.Is(err, wager.ErrInvalidTransaction) {
				t.Fatalf("err = %v, want %v", err, wager.ErrInvalidTransaction)
			}
		})
	}
}

func storedState(t *testing.T, status wager.Status) wager.State {
	t.Helper()

	a := refundArgs(t)
	state := wager.State{
		ID:            referenceID(t),
		Kind:          a.kind,
		Status:        status,
		WalletID:      a.walletID,
		PlayerID:      a.playerID,
		Amount:        a.amount,
		External:      a.external,
		CorrelationID: a.correlationID,
		CreatedAt:     longAgo,
		UpdatedAt:     longAgo,
	}
	switch status {
	case wager.PendingReference:
		state.ReferenceExpiresAt = longAgo.Add(5 * time.Minute)
		state.NextAttemptAt = longAgo.Add(time.Second)
	case wager.Rejected:
		state.FailureCode = failure.ReferenceNotFound
	case wager.Failed:
		state.FailureCode = failure.ProcessingFailed
	}
	return state
}

func TestRehydrate(t *testing.T) {
	statuses := []wager.Status{wager.Pending, wager.PendingReference, wager.Processed, wager.Rejected, wager.Failed}

	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			state := storedState(t, status)

			tx, err := wager.Rehydrate(state)
			if err != nil {
				t.Fatalf("Rehydrate: %v", err)
			}
			if tx.State() != state {
				t.Fatalf("State() = %+v, want %+v", tx.State(), state)
			}
		})
	}
}

func TestRehydrateLossAndOpening(t *testing.T) {
	loss := storedState(t, wager.Processed)
	loss.Kind = wager.Loss
	loss.Amount = amount(t, 0, "BRL")
	loss.External.ReferenceExternalID = ids.ExternalTransactionID{}

	opening := storedState(t, wager.Processed)
	opening.Kind = wager.Opening
	opening.External = wager.External{}

	tests := []struct {
		name  string
		state wager.State
	}{
		{"loss of zero", loss},
		{"opening", opening},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := wager.Rehydrate(tt.state)
			if err != nil {
				t.Fatalf("Rehydrate: %v", err)
			}
			if tx.State() != tt.state {
				t.Fatalf("State() = %+v, want %+v", tx.State(), tt.state)
			}
		})
	}
}

func TestRehydrateRejects(t *testing.T) {
	tests := []struct {
		name   string
		status wager.Status
		apply  func(*wager.State)
	}{
		{"missing id", wager.Processed, func(s *wager.State) { s.ID = ids.TransactionID{} }},
		{"missing wallet", wager.Processed, func(s *wager.State) { s.WalletID = ids.WalletID{} }},
		{"missing player", wager.Processed, func(s *wager.State) { s.PlayerID = ids.PlayerID{} }},
		{"uninitialized amount", wager.Processed, func(s *wager.State) { s.Amount = money.Money{} }},
		{"missing correlation id", wager.Processed, func(s *wager.State) { s.CorrelationID = "" }},
		{"missing creation time", wager.Processed, func(s *wager.State) { s.CreatedAt = time.Time{} }},
		{"missing update time", wager.Processed, func(s *wager.State) { s.UpdatedAt = time.Time{} }},
		{"unknown kind", wager.Processed, func(s *wager.State) { s.Kind = wager.Kind("BONUS") }},
		{"unknown status", wager.Processed, func(s *wager.State) { s.Status = wager.Status("DONE") }},
		{"external without provider data", wager.Processed, func(s *wager.State) { s.External = wager.External{} }},
		{"opening with provider data", wager.Processed, func(s *wager.State) { s.Kind = wager.Opening }},
		{"rejected without failure code", wager.Rejected, func(s *wager.State) { s.FailureCode = "" }},
		{"failed without failure code", wager.Failed, func(s *wager.State) { s.FailureCode = "" }},
		{"processed with failure code", wager.Processed, func(s *wager.State) { s.FailureCode = failure.ReferenceNotFound }},
		{"pending reference without deadline", wager.PendingReference, func(s *wager.State) { s.ReferenceExpiresAt = time.Time{} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := storedState(t, tt.status)
			tt.apply(&state)

			if _, err := wager.Rehydrate(state); !errors.Is(err, wager.ErrInvalidTransaction) {
				t.Fatalf("err = %v, want %v", err, wager.ErrInvalidTransaction)
			}
		})
	}
}
