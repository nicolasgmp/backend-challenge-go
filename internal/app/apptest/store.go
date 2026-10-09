package apptest

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"time"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/events"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/ledger"
	"jungle-gaming-challeng/internal/domain/wager"
	"jungle-gaming-challeng/internal/domain/wallet"
)

var ErrOutsideTransaction = errors.New("apptest: called outside a transaction")

type txKey struct{}

type data struct {
	wallets      map[ids.WalletID]wallet.State
	transactions map[ids.TransactionID]wager.State
	entries      []ledger.Entry
	events       []events.Event
	published    map[ids.EventID]bool
	attempts     map[ids.EventID]int
	inbox        map[string]string
}

func (d data) clone() data {
	return data{
		wallets:      maps.Clone(d.wallets),
		transactions: maps.Clone(d.transactions),
		entries:      slices.Clone(d.entries),
		events:       slices.Clone(d.events),
		published:    maps.Clone(d.published),
		attempts:     maps.Clone(d.attempts),
		inbox:        maps.Clone(d.inbox),
	}
}

type Store struct {
	Calls    []string
	data     data
	failures map[string][]error
}

var _ app.TxRunner = (*Store)(nil)

func NewStore() *Store {
	return &Store{
		data: data{
			wallets:      map[ids.WalletID]wallet.State{},
			transactions: map[ids.TransactionID]wager.State{},
			published:    map[ids.EventID]bool{},
			attempts:     map[ids.EventID]int{},
			inbox:        map[string]string{},
		},
		failures: map[string][]error{},
	}
}

func (s *Store) FailNext(call string, err error) {
	s.failures[call] = append(s.failures[call], err)
}

func (s *Store) Run(ctx context.Context, fn func(ctx context.Context) error) error {
	snapshot := s.data.clone()
	if err := fn(context.WithValue(ctx, txKey{}, true)); err != nil {
		s.data = snapshot
		return err
	}
	return nil
}

func (s *Store) RunReadOnly(ctx context.Context, fn func(ctx context.Context) error) error {
	return s.Run(ctx, fn)
}

func (s *Store) Events() []events.Event {
	return slices.Clone(s.data.events)
}

func (s *Store) Entries() []ledger.Entry {
	return slices.Clone(s.data.entries)
}

func (s *Store) TransactionStates() []wager.State {
	return slices.Collect(maps.Values(s.data.transactions))
}

func (s *Store) WalletStates() []wallet.State {
	return slices.Collect(maps.Values(s.data.wallets))
}

func (s *Store) call(name string) error {
	s.Calls = append(s.Calls, name)
	queue := s.failures[name]
	if len(queue) == 0 {
		return nil
	}
	s.failures[name] = queue[1:]
	return queue[0]
}

func (s *Store) lockingCall(ctx context.Context, name string) error {
	if !inTransaction(ctx) {
		return ErrOutsideTransaction
	}
	return s.call(name)
}

func inTransaction(ctx context.Context) bool {
	return ctx.Value(txKey{}) != nil
}

type walletRepository struct{ store *Store }

var _ app.WalletRepository = walletRepository{}

func (s *Store) Wallets() app.WalletRepository {
	return walletRepository{s}
}

func (r walletRepository) Insert(_ context.Context, w *wallet.Wallet) error {
	if err := r.store.call("wallets.Insert"); err != nil {
		return err
	}
	state := w.State()
	for _, existing := range r.store.data.wallets {
		if existing.PlayerID == state.PlayerID && existing.Balance.Currency() == state.Balance.Currency() {
			return app.ErrUniqueViolation
		}
	}
	r.store.data.wallets[state.ID] = state
	return nil
}

func (r walletRepository) Get(_ context.Context, id ids.WalletID) (*wallet.Wallet, error) {
	if err := r.store.call("wallets.Get"); err != nil {
		return nil, err
	}
	return r.rehydrate(id)
}

func (r walletRepository) GetForUpdate(ctx context.Context, id ids.WalletID) (*wallet.Wallet, error) {
	if err := r.store.lockingCall(ctx, "wallets.GetForUpdate"); err != nil {
		return nil, err
	}
	return r.rehydrate(id)
}

func (r walletRepository) UpdateBalance(_ context.Context, w *wallet.Wallet) error {
	if err := r.store.call("wallets.UpdateBalance"); err != nil {
		return err
	}
	state := w.State()
	if _, found := r.store.data.wallets[state.ID]; !found {
		return app.ErrNotFound
	}
	r.store.data.wallets[state.ID] = state
	return nil
}

func (r walletRepository) rehydrate(id ids.WalletID) (*wallet.Wallet, error) {
	state, found := r.store.data.wallets[id]
	if !found {
		return nil, app.ErrNotFound
	}
	return wallet.Rehydrate(state)
}

type transactionRepository struct{ store *Store }

var _ app.TransactionRepository = transactionRepository{}

func (s *Store) Transactions() app.TransactionRepository {
	return transactionRepository{s}
}

func (r transactionRepository) Insert(_ context.Context, tx *wager.Transaction) error {
	if err := r.store.call("transactions.Insert"); err != nil {
		return err
	}
	state := tx.State()
	if r.conflicts(state) {
		return app.ErrUniqueViolation
	}
	r.store.data.transactions[state.ID] = state
	return nil
}

func (r transactionRepository) Update(_ context.Context, tx *wager.Transaction) error {
	if err := r.store.call("transactions.Update"); err != nil {
		return err
	}
	state := tx.State()
	if _, found := r.store.data.transactions[state.ID]; !found {
		return app.ErrNotFound
	}
	if r.reversalTaken(state) {
		return app.ErrUniqueViolation
	}
	r.store.data.transactions[state.ID] = state
	return nil
}

func (r transactionRepository) Get(_ context.Context, id ids.TransactionID) (*wager.Transaction, error) {
	if err := r.store.call("transactions.Get"); err != nil {
		return nil, err
	}
	return r.rehydrate(id)
}

func (r transactionRepository) GetForUpdate(ctx context.Context, id ids.TransactionID) (*wager.Transaction, error) {
	if err := r.store.lockingCall(ctx, "transactions.GetForUpdate"); err != nil {
		return nil, err
	}
	return r.rehydrate(id)
}

func (r transactionRepository) FindByIdempotencyKey(_ context.Context, providerID ids.ProviderID, key ids.IdempotencyKey) (*wager.Transaction, error) {
	if err := r.store.call("transactions.FindByIdempotencyKey"); err != nil {
		return nil, err
	}
	return r.find(func(state wager.State) bool {
		return state.External.ProviderID == providerID && state.External.IdempotencyKey == key
	})
}

func (r transactionRepository) FindByExternalID(_ context.Context, providerID ids.ProviderID, externalID ids.ExternalTransactionID) (*wager.Transaction, error) {
	if err := r.store.call("transactions.FindByExternalID"); err != nil {
		return nil, err
	}
	return r.find(func(state wager.State) bool {
		return state.External.ProviderID == providerID && state.External.ExternalID == externalID
	})
}

func (r transactionRepository) HasProcessedReversal(_ context.Context, referenceID ids.TransactionID) (bool, error) {
	if err := r.store.call("transactions.HasProcessedReversal"); err != nil {
		return false, err
	}
	for _, state := range r.store.data.transactions {
		if isProcessedReversal(state) && state.ReferenceID == referenceID {
			return true, nil
		}
	}
	return false, nil
}

func (r transactionRepository) ListDuePendingReferences(_ context.Context, now time.Time, limit int) ([]*wager.Transaction, error) {
	if err := r.store.call("transactions.ListDuePendingReferences"); err != nil {
		return nil, err
	}
	var due []wager.State
	for _, state := range r.store.data.transactions {
		if state.Status == wager.PendingReference && !state.NextAttemptAt.After(now) {
			due = append(due, state)
		}
	}
	slices.SortFunc(due, func(a, b wager.State) int {
		return cmp.Or(a.NextAttemptAt.Compare(b.NextAttemptAt), cmp.Compare(a.ID.String(), b.ID.String()))
	})

	var transactions []*wager.Transaction
	for _, state := range due[:min(limit, len(due))] {
		tx, err := wager.Rehydrate(state)
		if err != nil {
			return nil, err
		}
		transactions = append(transactions, tx)
	}
	return transactions, nil
}

func (r transactionRepository) rehydrate(id ids.TransactionID) (*wager.Transaction, error) {
	state, found := r.store.data.transactions[id]
	if !found {
		return nil, app.ErrNotFound
	}
	return wager.Rehydrate(state)
}

func (r transactionRepository) find(matches func(wager.State) bool) (*wager.Transaction, error) {
	for _, state := range r.store.data.transactions {
		if matches(state) {
			return wager.Rehydrate(state)
		}
	}
	return nil, app.ErrNotFound
}

func (r transactionRepository) conflicts(state wager.State) bool {
	for _, existing := range r.store.data.transactions {
		if !state.Kind.IsExternal() || existing.External.ProviderID != state.External.ProviderID {
			continue
		}
		if existing.External.IdempotencyKey == state.External.IdempotencyKey {
			return true
		}
		if existing.External.ExternalID == state.External.ExternalID {
			return true
		}
	}
	return r.reversalTaken(state)
}

func (r transactionRepository) reversalTaken(state wager.State) bool {
	if !isProcessedReversal(state) {
		return false
	}
	for _, existing := range r.store.data.transactions {
		if isProcessedReversal(existing) && existing.ReferenceID == state.ReferenceID {
			return true
		}
	}
	return false
}

func isProcessedReversal(state wager.State) bool {
	return state.Status == wager.Processed && (state.Kind == wager.Refund || state.Kind == wager.Rollback)
}

type ledgerRepository struct{ store *Store }

var _ app.LedgerRepository = ledgerRepository{}

func (s *Store) Ledger() app.LedgerRepository {
	return ledgerRepository{s}
}

func (r ledgerRepository) Insert(_ context.Context, entry ledger.Entry) error {
	if err := r.store.call("ledger.Insert"); err != nil {
		return err
	}
	fields := entry.Fields()
	for _, existing := range r.store.data.entries {
		other := existing.Fields()
		if other.WalletID != fields.WalletID {
			continue
		}
		if other.TransactionID == fields.TransactionID || other.WalletVersion == fields.WalletVersion {
			return app.ErrUniqueViolation
		}
	}
	r.store.data.entries = append(r.store.data.entries, entry)
	return nil
}

func (r ledgerRepository) ListPage(_ context.Context, walletID ids.WalletID, afterVersion int64, limit int) ([]ledger.Entry, error) {
	if err := r.store.call("ledger.ListPage"); err != nil {
		return nil, err
	}
	var page []ledger.Entry
	for _, entry := range r.store.data.entries {
		if entry.Fields().WalletID == walletID && entry.Fields().WalletVersion > afterVersion {
			page = append(page, entry)
		}
	}
	return page[:min(limit, len(page))], nil
}

func (r ledgerRepository) Totals(_ context.Context, walletID ids.WalletID) (app.LedgerTotals, error) {
	if err := r.store.call("ledger.Totals"); err != nil {
		return app.LedgerTotals{}, err
	}
	var totals app.LedgerTotals
	for _, entry := range r.store.data.entries {
		fields := entry.Fields()
		if fields.WalletID != walletID {
			continue
		}
		totals.Entries++
		if fields.Direction == ledger.Credit {
			totals.CreditUnits += fields.Amount.MinorUnits()
		} else {
			totals.DebitUnits += fields.Amount.MinorUnits()
		}
	}
	return totals, nil
}

type outboxStore struct{ store *Store }

var _ app.OutboxStore = outboxStore{}

func (s *Store) Outbox() app.OutboxStore {
	return outboxStore{s}
}

func (o outboxStore) Insert(_ context.Context, event events.Event) error {
	if err := o.store.call("outbox.Insert"); err != nil {
		return err
	}
	o.store.data.events = append(o.store.data.events, event)
	return nil
}

func (o outboxStore) Claim(_ context.Context, limit int, _ time.Duration) ([]app.OutboxRecord, error) {
	if err := o.store.call("outbox.Claim"); err != nil {
		return nil, err
	}
	var records []app.OutboxRecord
	for _, event := range o.store.data.events {
		header := event.Header()
		if o.store.data.published[header.EventID] || len(records) == limit {
			continue
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		records = append(records, app.OutboxRecord{
			EventID:  header.EventID,
			GroupKey: header.WalletID.String(),
			Payload:  payload,
			Attempts: o.store.data.attempts[header.EventID],
		})
	}
	return records, nil
}

func (o outboxStore) MarkPublished(_ context.Context, eventID ids.EventID) error {
	if err := o.store.call("outbox.MarkPublished"); err != nil {
		return err
	}
	o.store.data.published[eventID] = true
	return nil
}

func (o outboxStore) MarkFailed(_ context.Context, eventID ids.EventID, _ time.Time, _ string) error {
	if err := o.store.call("outbox.MarkFailed"); err != nil {
		return err
	}
	o.store.data.attempts[eventID]++
	return nil
}

func (o outboxStore) OldestPendingAge(_ context.Context) (time.Duration, error) {
	if err := o.store.call("outbox.OldestPendingAge"); err != nil {
		return 0, err
	}
	for _, event := range o.store.data.events {
		if !o.store.data.published[event.Header().EventID] {
			return time.Since(event.Header().OccurredAt), nil
		}
	}
	return 0, nil
}

type inboxStore struct{ store *Store }

var _ app.InboxStore = inboxStore{}

func (s *Store) Inbox() app.InboxStore {
	return inboxStore{s}
}

func (i inboxStore) Register(_ context.Context, consumer, messageID, payloadHash string) (app.InboxStatus, error) {
	if err := i.store.call("inbox.Register"); err != nil {
		return "", err
	}
	key := consumer + "/" + messageID
	stored, found := i.store.data.inbox[key]
	if !found {
		i.store.data.inbox[key] = payloadHash
		return app.InboxNew, nil
	}
	if stored != payloadHash {
		return app.InboxHashMismatch, nil
	}
	return app.InboxDuplicate, nil
}

func (i inboxStore) Complete(_ context.Context, consumer, messageID string) error {
	if err := i.store.call("inbox.Complete"); err != nil {
		return err
	}
	if _, found := i.store.data.inbox[consumer+"/"+messageID]; !found {
		return app.ErrNotFound
	}
	return nil
}
