package apptest

import (
	"strings"
	"time"

	"jungle-gaming-challeng/internal/app"
)

type Clock struct {
	Current time.Time
}

var _ app.Clock = (*Clock)(nil)

func (c *Clock) Now() time.Time {
	return c.Current
}

type Metrics struct {
	Calls []string
}

var _ app.Metrics = (*Metrics)(nil)

func (m *Metrics) record(parts ...string) {
	m.Calls = append(m.Calls, strings.Join(parts, " "))
}

func (m *Metrics) TransactionResult(channel, kind, status, failureCode string) {
	m.record("TransactionResult", channel, kind, status, failureCode)
}

func (m *Metrics) ProcessingDuration(channel string, _ time.Duration) {
	m.record("ProcessingDuration", channel)
}

func (m *Metrics) Duplicate(source string) {
	m.record("Duplicate", source)
}

func (m *Metrics) Retry(component string) {
	m.record("Retry", component)
}

func (m *Metrics) DeadLetter(reason string) {
	m.record("DeadLetter", reason)
}

func (m *Metrics) ConcurrencyConflict(conflict string) {
	m.record("ConcurrencyConflict", conflict)
}

func (m *Metrics) OutboxLag(time.Duration) {
	m.record("OutboxLag")
}

func (m *Metrics) ReconciliationDivergence() {
	m.record("ReconciliationDivergence")
}
