package wager

import (
	"errors"

	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ledger"
)

var ErrReferencePending = errors.New("wager: reference still pending")

func EffectOf(kind, referenceKind Kind) (ledger.Direction, bool, error) {
	switch {
	case kind == Bet && referenceKind == "":
		return ledger.Debit, true, nil
	case kind == Loss && referenceKind == "":
		return "", false, nil
	case kind == Win && (referenceKind == "" || referenceKind == Bet):
		return ledger.Credit, true, nil
	case kind == Refund && referenceKind == Bet:
		return ledger.Credit, true, nil
	case kind == Rollback && referenceKind == Bet:
		return ledger.Credit, true, nil
	case kind == Rollback && (referenceKind == Win || referenceKind == Refund):
		return ledger.Debit, true, nil
	default:
		return "", false, failure.RejectionError{Code: failure.ReferenceKindNotAllowed}
	}
}

func ValidateReference(operation, reference *Transaction) error {
	op, ref := operation.state, reference.state
	if !ref.Status.IsTerminal() {
		return ErrReferencePending
	}
	if ref.Status != Processed {
		return failure.RejectionError{Code: failure.ReferenceNotProcessed}
	}
	if _, _, err := EffectOf(op.Kind, ref.Kind); err != nil {
		return err
	}
	if !agrees(op, ref) {
		return failure.RejectionError{Code: failure.ReferenceMismatch}
	}
	return nil
}

func agrees(op, ref State) bool {
	if op.External.ProviderID != ref.External.ProviderID || op.External.RoundID != ref.External.RoundID {
		return false
	}
	if op.PlayerID != ref.PlayerID || op.WalletID != ref.WalletID {
		return false
	}
	if op.Amount.Currency() != ref.Amount.Currency() {
		return false
	}
	return op.Kind == Win || op.Amount.Equal(ref.Amount)
}
