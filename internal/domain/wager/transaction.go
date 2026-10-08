package wager

import (
	"errors"
	"time"

	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/money"
)

var (
	ErrInvalidTransaction = errors.New("wager: invalid transaction")
	ErrInvalidTransition  = errors.New("wager: invalid transition")
)

type External struct {
	ProviderID          ids.ProviderID
	ExternalID          ids.ExternalTransactionID
	IdempotencyKey      ids.IdempotencyKey
	PayloadHash         string
	RoundID             ids.RoundID
	GameID              ids.GameID
	ReferenceExternalID ids.ExternalTransactionID
}

type State struct {
	ID                 ids.TransactionID
	Kind               Kind
	Status             Status
	WalletID           ids.WalletID
	PlayerID           ids.PlayerID
	Amount             money.Money
	External           External
	ReferenceID        ids.TransactionID
	FailureCode        failure.Code
	ResultBalance      money.Money
	ReferenceExpiresAt time.Time
	NextAttemptAt      time.Time
	Attempts           int
	ErrorCount         int
	CorrelationID      string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	CompletedAt        time.Time
}

type Transaction struct {
	state State
}

func NewExternal(kind Kind, walletID ids.WalletID, playerID ids.PlayerID, amount money.Money, external External, correlationID string) (*Transaction, error) {
	if walletID.IsZero() || playerID.IsZero() || !external.complete() || correlationID == "" {
		return nil, ErrInvalidTransaction
	}
	if !kind.IsExternal() {
		return nil, failure.InvalidInputError{Code: failure.UnsupportedKind}
	}
	if !amountAllowed(kind, amount) {
		return nil, failure.InvalidInputError{Code: failure.InvalidAmountForKind}
	}
	if err := checkReferencePresence(kind, external.ReferenceExternalID); err != nil {
		return nil, err
	}
	return create(kind, walletID, playerID, amount, external, correlationID)
}

func NewOpening(walletID ids.WalletID, playerID ids.PlayerID, amount money.Money, correlationID string) (*Transaction, error) {
	if walletID.IsZero() || playerID.IsZero() || !amount.IsPositive() || correlationID == "" {
		return nil, ErrInvalidTransaction
	}
	t, err := create(Opening, walletID, playerID, amount, External{}, correlationID)
	if err != nil {
		return nil, err
	}
	t.state.ResultBalance = amount
	t.complete(Processed)
	return t, nil
}

func Rehydrate(state State) (*Transaction, error) {
	if !state.coherent() {
		return nil, ErrInvalidTransaction
	}
	return &Transaction{state: state}, nil
}

func (t *Transaction) State() State {
	return t.state
}

func create(kind Kind, walletID ids.WalletID, playerID ids.PlayerID, amount money.Money, external External, correlationID string) (*Transaction, error) {
	id, err := ids.NewTransactionID()
	if err != nil {
		return nil, err
	}
	createdAt := utcNow()
	return &Transaction{state: State{
		ID:            id,
		Kind:          kind,
		Status:        Pending,
		WalletID:      walletID,
		PlayerID:      playerID,
		Amount:        amount,
		External:      external,
		CorrelationID: correlationID,
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}}, nil
}

func amountAllowed(kind Kind, amount money.Money) bool {
	if kind == Loss {
		return amount.IsZero()
	}
	return amount.IsPositive()
}

func checkReferencePresence(kind Kind, reference ids.ExternalTransactionID) error {
	switch kind {
	case Refund, Rollback:
		if reference.IsZero() {
			return failure.InvalidInputError{Code: failure.MissingReference}
		}
	case Bet, Loss:
		if !reference.IsZero() {
			return failure.InvalidInputError{Code: failure.UnexpectedReference}
		}
	}
	return nil
}

func (e External) complete() bool {
	if e.ProviderID.IsZero() || e.ExternalID.IsZero() || e.IdempotencyKey.IsZero() {
		return false
	}
	return e.PayloadHash != "" && !e.RoundID.IsZero() && !e.GameID.IsZero()
}

func (s State) coherent() bool {
	if s.ID.IsZero() || s.WalletID.IsZero() || s.PlayerID.IsZero() || !notNegative(s.Amount) {
		return false
	}
	if s.CorrelationID == "" || s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() {
		return false
	}
	if _, err := ParseKind(string(s.Kind)); err != nil {
		return false
	}
	if _, err := ParseStatus(string(s.Status)); err != nil {
		return false
	}
	if s.Kind == Opening && s.External != (External{}) {
		return false
	}
	if s.Kind.IsExternal() && !s.External.complete() {
		return false
	}
	if s.Status == PendingReference && s.ReferenceExpiresAt.IsZero() {
		return false
	}
	closedWithFailure := s.Status == Rejected || s.Status == Failed
	return closedWithFailure == (s.FailureCode != "")
}

func notNegative(m money.Money) bool {
	return m.IsZero() || m.IsPositive()
}

func utcNow() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
