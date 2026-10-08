package events

import (
	"errors"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/money"
)

func TestEnvelope(t *testing.T) {
	tx := refund(t)

	event, err := NewWagerTransactionProcessed(origin, tx, amount(t, 100000))
	if err != nil {
		t.Fatalf("NewWagerTransactionProcessed: %v", err)
	}

	header := event.Header()
	want := Header{
		EventID:       header.EventID,
		EventType:     "WagerTransactionProcessed",
		AggregateID:   transactionUUID,
		CorrelationID: "correlation-1",
		CausationID:   "message-1",
		OccurredAt:    header.OccurredAt,
		Version:       1,
		WalletID:      tx.WalletID,
	}
	if header != want {
		t.Fatalf("Header() = %+v, want %+v", header, want)
	}
	if header.EventID.IsZero() || header.OccurredAt.IsZero() {
		t.Fatal("the event has no id or no occurrence time")
	}
	if header.OccurredAt != header.OccurredAt.UTC().Truncate(time.Microsecond) {
		t.Fatalf("OccurredAt = %s, want UTC with microsecond precision", header.OccurredAt)
	}
}

func TestEventsRejectMissingCorrelation(t *testing.T) {
	missing := Origin{CausationID: "message-1"}

	if _, err := NewWalletBalanceChanged(missing, debit(t)); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("err = %v, want %v", err, ErrInvalidEvent)
	}
}

func TestWagerTransactionProcessed(t *testing.T) {
	loss := refund(t)
	loss.Kind = "LOSS"
	loss.Money = amount(t, 0)
	loss.ReferenceExternalTransactionID = ids.ExternalTransactionID{}

	transactions := map[string]Transaction{"external": refund(t), "opening": opening(t), "loss of zero": loss}

	for name, tx := range transactions {
		t.Run(name, func(t *testing.T) {
			event, err := NewWagerTransactionProcessed(origin, tx, amount(t, 100000))
			if err != nil {
				t.Fatalf("NewWagerTransactionProcessed: %v", err)
			}
			want := WagerTransactionProcessed{Transaction: tx, Balance: amount(t, 100000)}
			if event.Data() != want {
				t.Fatalf("Data() = %+v, want %+v", event.Data(), want)
			}
		})
	}
}

func TestWagerTransactionProcessedRejects(t *testing.T) {
	tests := []struct {
		name    string
		apply   func(*Transaction)
		balance money.Money
	}{
		{"missing transaction", func(tx *Transaction) { tx.TransactionID = ids.TransactionID{} }, amount(t, 100000)},
		{"missing wallet", func(tx *Transaction) { tx.WalletID = ids.WalletID{} }, amount(t, 100000)},
		{"missing player", func(tx *Transaction) { tx.PlayerID = ids.PlayerID{} }, amount(t, 100000)},
		{"missing kind", func(tx *Transaction) { tx.Kind = "" }, amount(t, 100000)},
		{"uninitialized money", func(tx *Transaction) { tx.Money = money.Money{} }, amount(t, 100000)},
		{"uninitialized balance", func(*Transaction) {}, money.Money{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := refund(t)
			tt.apply(&tx)

			if _, err := NewWagerTransactionProcessed(origin, tx, tt.balance); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("err = %v, want %v", err, ErrInvalidEvent)
			}
		})
	}
}

func TestWagerTransactionRejected(t *testing.T) {
	tx := refund(t)

	event, err := NewWagerTransactionRejected(origin, tx, failure.ReferenceMismatch)
	if err != nil {
		t.Fatalf("NewWagerTransactionRejected: %v", err)
	}
	if event.Header().EventType != "WagerTransactionRejected" || event.Header().Version != 1 {
		t.Fatalf("Header() = %+v, want WagerTransactionRejected version 1", event.Header())
	}
	want := WagerTransactionRejected{Transaction: tx, FailureCode: failure.ReferenceMismatch}
	if event.Data() != want {
		t.Fatalf("Data() = %+v, want %+v", event.Data(), want)
	}

	for _, code := range []failure.Code{"", failure.InvalidMoney, failure.ProcessingFailed} {
		t.Run("code "+string(code), func(t *testing.T) {
			if _, err := NewWagerTransactionRejected(origin, tx, code); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("err = %v, want %v", err, ErrInvalidEvent)
			}
		})
	}
}

func TestWalletBalanceChanged(t *testing.T) {
	credit := debit(t)
	credit.Direction = ledger.Credit
	credit.BalanceBefore = amount(t, 97500)
	credit.BalanceAfter = amount(t, 100000)
	credit.WalletVersion = 3

	changes := map[string]WalletBalanceChanged{"debit": debit(t), "credit": credit}

	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			event, err := NewWalletBalanceChanged(origin, change)
			if err != nil {
				t.Fatalf("NewWalletBalanceChanged: %v", err)
			}

			header := event.Header()
			if header.EventType != "WalletBalanceChanged" || header.Version != 1 {
				t.Fatalf("Header() = %+v, want WalletBalanceChanged version 1", header)
			}
			if header.AggregateID != walletUUID || header.WalletID != change.WalletID {
				t.Fatalf("Header() = %+v, want the wallet as aggregate", header)
			}
			if event.Data() != change {
				t.Fatalf("Data() = %+v, want %+v", event.Data(), change)
			}
		})
	}
}

func TestWalletBalanceChangedRejects(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*WalletBalanceChanged)
	}{
		{"missing wallet", func(c *WalletBalanceChanged) { c.WalletID = ids.WalletID{} }},
		{"missing transaction", func(c *WalletBalanceChanged) { c.TransactionID = ids.TransactionID{} }},
		{"unknown direction", func(c *WalletBalanceChanged) { c.Direction = "TRANSFER" }},
		{"zero money", func(c *WalletBalanceChanged) { c.Money = amount(t, 0) }},
		{"uninitialized balance before", func(c *WalletBalanceChanged) { c.BalanceBefore = money.Money{} }},
		{"uninitialized balance after", func(c *WalletBalanceChanged) { c.BalanceAfter = money.Money{} }},
		{"wallet version zero", func(c *WalletBalanceChanged) { c.WalletVersion = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			change := debit(t)
			tt.apply(&change)

			if _, err := NewWalletBalanceChanged(origin, change); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("err = %v, want %v", err, ErrInvalidEvent)
			}
		})
	}
}

func TestWagerTransactionPendingReference(t *testing.T) {
	tx := refund(t)
	saoPaulo := time.FixedZone("-03", -3*60*60)

	event, err := NewWagerTransactionPendingReference(origin, tx, deadline.In(saoPaulo))
	if err != nil {
		t.Fatalf("NewWagerTransactionPendingReference: %v", err)
	}
	if event.Header().EventType != "WagerTransactionPendingReference" || event.Header().Version != 1 {
		t.Fatalf("Header() = %+v, want WagerTransactionPendingReference version 1", event.Header())
	}
	want := WagerTransactionPendingReference{Transaction: tx, ReferenceExpiresAt: deadline}
	if event.Data() != want {
		t.Fatalf("Data() = %+v, want %+v", event.Data(), want)
	}
}

func TestWagerTransactionPendingReferenceRejects(t *testing.T) {
	withoutReference := refund(t)
	withoutReference.ReferenceExternalTransactionID = ids.ExternalTransactionID{}

	tests := []struct {
		name      string
		tx        Transaction
		expiresAt time.Time
	}{
		{"missing reference", withoutReference, deadline},
		{"missing deadline", refund(t), time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewWagerTransactionPendingReference(origin, tt.tx, tt.expiresAt)
			if !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("err = %v, want %v", err, ErrInvalidEvent)
			}
		})
	}
}
