package app

import (
	"context"
	"time"

	"jungle-gaming-challeng/internal/domain/events"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/wager"
	"jungle-gaming-challeng/internal/domain/wallet"
)

type TxRunner interface {
	Run(ctx context.Context, fn func(ctx context.Context) error) error
	RunReadOnly(ctx context.Context, fn func(ctx context.Context) error) error
}

type WalletRepository interface {
	Insert(ctx context.Context, w *wallet.Wallet) error
	Get(ctx context.Context, id ids.WalletID) (*wallet.Wallet, error)
	GetForUpdate(ctx context.Context, id ids.WalletID) (*wallet.Wallet, error)
	UpdateBalance(ctx context.Context, w *wallet.Wallet) error
}

type TransactionRepository interface {
	Insert(ctx context.Context, tx *wager.Transaction) error
	Update(ctx context.Context, tx *wager.Transaction) error
	Get(ctx context.Context, id ids.TransactionID) (*wager.Transaction, error)
	GetForUpdate(ctx context.Context, id ids.TransactionID) (*wager.Transaction, error)
	FindByIdempotencyKey(ctx context.Context, providerID ids.ProviderID, key ids.IdempotencyKey) (*wager.Transaction, error)
	FindByExternalID(ctx context.Context, providerID ids.ProviderID, externalID ids.ExternalTransactionID) (*wager.Transaction, error)
	HasProcessedReversal(ctx context.Context, referenceID ids.TransactionID) (bool, error)
	ListDuePendingReferences(ctx context.Context, now time.Time, limit int) ([]*wager.Transaction, error)
}

type LedgerTotals struct {
	CreditUnits int64
	DebitUnits  int64
	Entries     int
}

type LedgerRepository interface {
	Insert(ctx context.Context, entry ledger.Entry) error
	ListPage(ctx context.Context, walletID ids.WalletID, afterVersion int64, limit int) ([]ledger.Entry, error)
	Totals(ctx context.Context, walletID ids.WalletID) (LedgerTotals, error)
}

type OutboxRecord struct {
	EventID  ids.EventID
	GroupKey string
	Payload  []byte
	Attempts int
}

type OutboxStore interface {
	Insert(ctx context.Context, event events.Event) error
	Claim(ctx context.Context, limit int, lease time.Duration) ([]OutboxRecord, error)
	MarkPublished(ctx context.Context, eventID ids.EventID) error
	MarkFailed(ctx context.Context, eventID ids.EventID, nextAttemptAt time.Time) error
	OldestPendingAge(ctx context.Context) (time.Duration, error)
}

type InboxStatus string

const (
	InboxNew          InboxStatus = "NEW"
	InboxDuplicate    InboxStatus = "DUPLICATE"
	InboxHashMismatch InboxStatus = "HASH_MISMATCH"
)

type InboxStore interface {
	Register(ctx context.Context, consumer, messageID, payloadHash string) (InboxStatus, error)
	Complete(ctx context.Context, consumer, messageID string) error
}

type Clock interface {
	Now() time.Time
}

type Metrics interface {
	TransactionResult(channel, kind, status, failureCode string)
	ProcessingDuration(channel string, duration time.Duration)
	Duplicate(source string)
	Retry(component string)
	DeadLetter(reason string)
	ConcurrencyConflict(conflict string)
	OutboxLag(age time.Duration)
	ReconciliationDivergence()
}
