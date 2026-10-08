package wager

import "errors"

type Status string

const (
	Pending          Status = "PENDING"
	PendingReference Status = "PENDING_REFERENCE"
	Processed        Status = "PROCESSED"
	Rejected         Status = "REJECTED"
	Failed           Status = "FAILED"
)

var ErrInvalidStatus = errors.New("wager: invalid status")

func ParseStatus(s string) (Status, error) {
	switch status := Status(s); status {
	case Pending, PendingReference, Processed, Rejected, Failed:
		return status, nil
	default:
		return "", ErrInvalidStatus
	}
}

func (s Status) IsTerminal() bool {
	return s == Processed || s == Rejected || s == Failed
}
