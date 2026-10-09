package failure

type Code string

const (
	MalformedRequest      Code = "MALFORMED_REQUEST"
	InvalidIdentifier     Code = "INVALID_IDENTIFIER"
	InvalidMoney          Code = "INVALID_MONEY"
	MissingIdempotencyKey Code = "MISSING_IDEMPOTENCY_KEY"
	UnsupportedKind       Code = "UNSUPPORTED_KIND"
	InvalidAmountForKind  Code = "INVALID_AMOUNT_FOR_KIND"
	MissingReference      Code = "MISSING_REFERENCE"
	UnexpectedReference   Code = "UNEXPECTED_REFERENCE"
	InvalidCursor         Code = "INVALID_CURSOR"
	WalletNotFound        Code = "WALLET_NOT_FOUND"
	TransactionNotFound   Code = "TRANSACTION_NOT_FOUND"

	InsufficientFunds         Code = "INSUFFICIENT_FUNDS"
	ReversalInsufficientFunds Code = "REVERSAL_INSUFFICIENT_FUNDS"
	BalanceOverflow           Code = "BALANCE_OVERFLOW"
	PlayerWalletMismatch      Code = "PLAYER_WALLET_MISMATCH"
	CurrencyMismatch          Code = "CURRENCY_MISMATCH"
	ReferenceNotFound         Code = "REFERENCE_NOT_FOUND"
	ReferenceNotProcessed     Code = "REFERENCE_NOT_PROCESSED"
	ReferenceMismatch         Code = "REFERENCE_MISMATCH"
	ReferenceKindNotAllowed   Code = "REFERENCE_KIND_NOT_ALLOWED"
	ReferenceAlreadyReversed  Code = "REFERENCE_ALREADY_REVERSED"

	ProcessingFailed Code = "PROCESSING_FAILED"
)

func (c Code) IsRejection() bool {
	switch c {
	case InsufficientFunds, ReversalInsufficientFunds, BalanceOverflow,
		PlayerWalletMismatch, CurrencyMismatch, ReferenceNotFound, ReferenceNotProcessed,
		ReferenceMismatch, ReferenceKindNotAllowed, ReferenceAlreadyReversed:
		return true
	default:
		return false
	}
}

type InvalidInputError struct {
	Code Code
}

func (e InvalidInputError) Error() string {
	return "invalid input: " + string(e.Code)
}

type RejectionError struct {
	Code Code
}

func (e RejectionError) Error() string {
	return "rejected: " + string(e.Code)
}
