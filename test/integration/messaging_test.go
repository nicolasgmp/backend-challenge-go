//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/jackc/pgx/v5/pgxpool"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/app/apptest"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/wager"
	"jungle-gaming-challeng/internal/domain/wallet"
	"jungle-gaming-challeng/internal/infra/postgres"
	"jungle-gaming-challeng/internal/infra/sqs"
	"jungle-gaming-challeng/internal/infra/sqs/sqstest"
	"jungle-gaming-challeng/internal/worker"
)

type messaging struct {
	environment
	client  *awssqs.Client
	queues  sqs.Queues
	handler *sqs.Handler
	metrics *apptest.Metrics
}

func newMessaging(t *testing.T) messaging {
	t.Helper()

	ctx := context.Background()
	e := newEnvironment(t)
	client := sqstest.Start(ctx, t)
	queues, err := sqs.EnsureQueues(ctx, client)
	if err != nil {
		t.Fatalf("EnsureQueues: %v", err)
	}
	metrics := &apptest.Metrics{}
	e.service.Metrics = metrics
	handler := &sqs.Handler{
		Tx: e.service.Tx, Inbox: postgres.NewInboxStore(e.pool), Service: e.service,
		Metrics: metrics, Logger: slog.New(slog.DiscardHandler),
	}
	return messaging{environment: e, client: client, queues: queues, handler: handler, metrics: metrics}
}

func (m messaging) consume(t *testing.T, handler *sqs.Handler) (stop func()) {
	t.Helper()

	consumer := sqs.NewConsumer(m.client, handler, m.metrics, slog.New(slog.DiscardHandler), sqs.ConsumerConfig{
		QueueURL: m.queues.InboundURL, DLQURL: m.queues.InboundDLQURL,
		Concurrency: 5, WaitTime: time.Second, HandleTimeout: 10 * time.Second, RetryBaseDelay: time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		consumer.Run(ctx)
		close(done)
	}()
	stop = sync.OnceFunc(func() {
		cancel()
		<-done
	})
	t.Cleanup(stop)
	return stop
}

func message(w wallet.State, messageID, kind, value, externalID string) string {
	return `{"messageId":"` + messageID + `","type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00.000Z",` +
		`"data":{"providerId":"provider-a","externalTransactionId":"` + externalID + `","idempotencyKey":"key-` + externalID + `",` +
		`"playerId":"` + w.PlayerID.String() + `","walletId":"` + w.ID.String() + `","roundId":"round-1","gameId":"game-1",` +
		`"kind":"` + kind + `","money":{"amount":"` + value + `","currency":"BRL"}}}`
}

func (m messaging) send(t *testing.T, w wallet.State, deduplication, body string) {
	t.Helper()

	_, err := m.client.SendMessage(context.Background(), &awssqs.SendMessageInput{
		QueueUrl:               aws.String(m.queues.InboundURL),
		MessageBody:            aws.String(body),
		MessageGroupId:         aws.String(w.ID.String()),
		MessageDeduplicationId: aws.String(deduplication),
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
}

func (m messaging) depth(t *testing.T, queueURL string) int {
	t.Helper()

	output, err := m.client.GetQueueAttributes(context.Background(), &awssqs.GetQueueAttributesInput{
		QueueUrl: aws.String(queueURL),
		AttributeNames: []types.QueueAttributeName{
			types.QueueAttributeNameApproximateNumberOfMessages,
			types.QueueAttributeNameApproximateNumberOfMessagesNotVisible,
		},
	})
	if err != nil {
		t.Fatalf("queue attributes: %v", err)
	}
	visible, _ := strconv.Atoi(output.Attributes["ApproximateNumberOfMessages"])
	inFlight, _ := strconv.Atoi(output.Attributes["ApproximateNumberOfMessagesNotVisible"])
	return visible + inFlight
}

func (m messaging) waitDepth(t *testing.T, queueURL string, want int, limit time.Duration) {
	t.Helper()

	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if m.depth(t, queueURL) == want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("queue has %d messages after %s, want %d", m.depth(t, queueURL), limit, want)
}

func (m messaging) deadLetterReasons(t *testing.T, want int) []string {
	t.Helper()

	m.waitDepth(t, m.queues.InboundDLQURL, want, 15*time.Second)
	output, err := m.client.ReceiveMessage(context.Background(), &awssqs.ReceiveMessageInput{
		QueueUrl: aws.String(m.queues.InboundDLQURL), MaxNumberOfMessages: 10, WaitTimeSeconds: 5,
		MessageAttributeNames: []string{"All"},
	})
	if err != nil {
		t.Fatalf("receive from the dead-letter queue: %v", err)
	}
	var reasons []string
	for _, message := range output.Messages {
		reasons = append(reasons, aws.ToString(message.MessageAttributes["reason"].StringValue))
	}
	return reasons
}

func TestRedeliveryAfterCommitIsRecognisedByTheInbox(t *testing.T) {
	ctx := context.Background()
	m := newMessaging(t)
	w := m.open(t, "1000.00")
	body := message(w, "msg-1", "BET", "25.00", "tx-1")
	m.send(t, w, "msg-1", body)

	received, err := m.client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: aws.String(m.queues.InboundURL), WaitTimeSeconds: 5})
	if err != nil || len(received.Messages) != 1 {
		t.Fatalf("receive: %d messages, err %v", len(received.Messages), err)
	}
	if outcome := m.handler.Handle(ctx, body); outcome.Action != sqs.Delete {
		t.Fatalf("outcome = %+v, want the handling committed", outcome)
	}
	m.wantWallet(t, w.ID, "975.00", 2)
	afterCommit := m.counts(t)

	_, err = m.client.ChangeMessageVisibility(ctx, &awssqs.ChangeMessageVisibilityInput{
		QueueUrl: aws.String(m.queues.InboundURL), ReceiptHandle: received.Messages[0].ReceiptHandle, VisibilityTimeout: 0,
	})
	if err != nil {
		t.Fatalf("release the message as if the consumer had died before deleting it: %v", err)
	}

	m.consume(t, m.handler)
	m.waitDepth(t, m.queues.InboundURL, 0, 15*time.Second)

	if got := m.counts(t); got != afterCommit {
		t.Fatalf("rows = %+v, want them unchanged by the redelivery: %+v", got, afterCommit)
	}
	m.wantWallet(t, w.ID, "975.00", 2)
	if m.depth(t, m.queues.InboundDLQURL) != 0 {
		t.Fatal("a redelivered message went to the dead-letter queue")
	}
	if recorded := m.metrics.Calls; !slices.Contains(recorded, "Duplicate inbox") {
		t.Fatalf("metrics = %v, want the duplicate counted", recorded)
	}
}

func TestInvalidMessagesGoToTheDeadLetterQueueWithoutBlockingTheWallet(t *testing.T) {
	m := newMessaging(t)
	w := m.open(t, "1000.00")
	m.consume(t, m.handler)

	m.send(t, w, "msg-1", message(w, "msg-1", "BET", "25.00", "tx-1"))
	m.waitDepth(t, m.queues.InboundURL, 0, 15*time.Second)
	m.wantWallet(t, w.ID, "975.00", 2)

	m.send(t, w, "msg-1-again", message(w, "msg-1", "BET", "40.00", "tx-2"))
	m.send(t, w, "msg-opening", message(w, "msg-3", "OPENING", "40.00", "tx-3"))
	m.send(t, w, "msg-4", message(w, "msg-4", "BET", "25.00", "tx-4"))
	started := time.Now()
	m.waitDepth(t, m.queues.InboundURL, 0, 20*time.Second)
	if waited := time.Since(started); waited > 15*time.Second {
		t.Fatalf("the valid message waited %s behind two invalid ones, want no wait for the redrive", waited)
	}

	m.wantWallet(t, w.ID, "950.00", 3)
	reasons := m.deadLetterReasons(t, 2)
	if len(reasons) != 2 || !slices.Contains(reasons, sqs.ReasonMessageIDReused) || !slices.Contains(reasons, string(failure.UnsupportedKind)) {
		t.Fatalf("dead-letter reasons = %v, want the reused message id and the unsupported kind", reasons)
	}
	if !slices.Contains(m.metrics.Calls, "DeadLetter "+string(failure.UnsupportedKind)) {
		t.Fatalf("metrics = %v, want the dead letter counted with its reason", m.metrics.Calls)
	}
	if got := m.counts(t); got.transactions != 3 {
		t.Fatalf("transactions = %d, want the opening and the two valid bets only", got.transactions)
	}
}

type switchableTx struct {
	up   app.TxRunner
	down app.TxRunner
	off  atomic.Bool
}

func (s *switchableTx) Run(ctx context.Context, fn func(context.Context) error) error {
	if s.off.Load() {
		return s.down.Run(ctx, fn)
	}
	return s.up.Run(ctx, fn)
}

func (s *switchableTx) RunReadOnly(ctx context.Context, fn func(context.Context) error) error {
	if s.off.Load() {
		return s.down.RunReadOnly(ctx, fn)
	}
	return s.up.RunReadOnly(ctx, fn)
}

func (m messaging) withUnreachableDatabase(t *testing.T) (*sqs.Handler, *switchableTx) {
	t.Helper()

	dead, err := pgxpool.New(context.Background(), "postgres://wallet:local-test-password@127.0.0.1:1/wallet?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("pool for an unreachable database: %v", err)
	}
	t.Cleanup(dead.Close)

	tx := &switchableTx{up: m.service.Tx, down: postgres.NewTxRunner(dead, time.Second)}
	tx.off.Store(true)
	handler := *m.handler
	handler.Tx = tx
	return &handler, tx
}

type failingInbox struct {
	app.InboxStore
	fail atomic.Bool
}

func (i *failingInbox) Complete(ctx context.Context, consumer, messageID string) error {
	if i.fail.Load() {
		return errBoom
	}
	return i.InboxStore.Complete(ctx, consumer, messageID)
}

func TestFailureAfterTheDomainChangeCommitsNothing(t *testing.T) {
	ctx := context.Background()
	m := newMessaging(t)
	w := m.open(t, "1000.00")
	before := m.counts(t)
	inbox := &failingInbox{InboxStore: m.handler.Inbox}
	inbox.fail.Store(true)
	handler := *m.handler
	handler.Inbox = inbox
	body := message(w, "msg-1", "BET", "25.00", "tx-1")

	if outcome := handler.Handle(ctx, body); outcome.Action != sqs.Retry {
		t.Fatalf("outcome = %+v, want a retry", outcome)
	}
	if got := m.counts(t); got != before || m.count(t, `SELECT count(*) FROM inbox`) != 0 {
		t.Fatalf("rows = %+v with %d inbox rows, want nothing committed: %+v", got, m.count(t, `SELECT count(*) FROM inbox`), before)
	}
	m.wantWallet(t, w.ID, "1000.00", 1)

	inbox.fail.Store(false)
	if outcome := handler.Handle(ctx, body); outcome.Action != sqs.Delete {
		t.Fatalf("redelivery outcome = %+v, want the message handled", outcome)
	}
	m.wantWallet(t, w.ID, "975.00", 2)
}

func (m messaging) count(t *testing.T, query string) int {
	t.Helper()

	var count int
	if err := m.pool.QueryRow(context.Background(), query).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	return count
}

func TestMessageIsProcessedOnceAfterTheDatabaseComesBack(t *testing.T) {
	m := newMessaging(t)
	w := m.open(t, "1000.00")
	handler, database := m.withUnreachableDatabase(t)
	m.consume(t, handler)

	m.send(t, w, "msg-1", message(w, "msg-1", "BET", "25.00", "tx-1"))
	time.Sleep(3 * time.Second)
	if m.depth(t, m.queues.InboundURL) != 1 || !slices.Contains(m.metrics.Calls, "Retry consumer") {
		t.Fatalf("depth %d, metrics %v, want the message kept in the queue and a retry counted", m.depth(t, m.queues.InboundURL), m.metrics.Calls)
	}
	m.wantWallet(t, w.ID, "1000.00", 1)

	database.off.Store(false)
	m.waitDepth(t, m.queues.InboundURL, 0, 30*time.Second)
	m.wantWallet(t, w.ID, "975.00", 2)
	if m.depth(t, m.queues.InboundDLQURL) != 0 {
		t.Fatal("a message that succeeded on retry went to the dead-letter queue")
	}
}

func TestPersistentFailureReachesTheDeadLetterQueue(t *testing.T) {
	m := newMessaging(t)
	w := m.open(t, "1000.00")
	handler, _ := m.withUnreachableDatabase(t)
	m.consume(t, handler)

	m.send(t, w, "msg-1", message(w, "msg-1", "BET", "25.00", "tx-1"))
	m.waitDepth(t, m.queues.InboundDLQURL, 1, 90*time.Second)

	if m.depth(t, m.queues.InboundURL) != 0 {
		t.Fatal("the message is still in the source queue after the redrive")
	}
	if !slices.Contains(m.metrics.Calls, "DeadLetter "+sqs.ReasonRetriesExceeded) {
		t.Fatalf("metrics = %v, want the exhausted retries counted", m.metrics.Calls)
	}
	m.wantWallet(t, w.ID, "1000.00", 1)
}

type countingPublisher struct {
	worker.EventPublisher
	mu     sync.Mutex
	counts map[ids.EventID]int
	bodies map[ids.EventID]string
}

func newCountingPublisher(inner worker.EventPublisher) *countingPublisher {
	return &countingPublisher{EventPublisher: inner, counts: map[ids.EventID]int{}, bodies: map[ids.EventID]string{}}
}

func (p *countingPublisher) Publish(ctx context.Context, record app.OutboxRecord) error {
	err := p.EventPublisher.Publish(ctx, record)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err == nil {
		p.counts[record.EventID]++
		if previous, seen := p.bodies[record.EventID]; seen && previous != string(record.Payload) {
			return errors.New("the same event was republished with another payload")
		}
		p.bodies[record.EventID] = string(record.Payload)
	}
	return err
}

func (p *countingPublisher) snapshot() map[ids.EventID]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	copied := map[ids.EventID]int{}
	for id, count := range p.counts {
		copied[id] = count
	}
	return copied
}

func (m messaging) outboxPublisher(publisher worker.EventPublisher, outbox app.OutboxStore, lease time.Duration) *worker.OutboxPublisher {
	return &worker.OutboxPublisher{
		Outbox: outbox, Publisher: publisher, Metrics: m.metrics, Clock: systemClock{},
		Logger: slog.New(slog.DiscardHandler), Batch: 10, Lease: lease,
	}
}

func (m messaging) pendingEvents(t *testing.T) int {
	t.Helper()

	var pending int
	if err := m.pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&pending); err != nil {
		t.Fatalf("count pending events: %v", err)
	}
	return pending
}

func TestTwoPublishersShareTheOutbox(t *testing.T) {
	m := newMessaging(t)
	w := m.open(t, "0.00")
	for i := range 50 {
		m.submit(t, operation(t, w, wager.Win, "1.00", fmt.Sprintf("tx-%d", i), ""))
	}
	if pending := m.pendingEvents(t); pending != 100 {
		t.Fatalf("pending events = %d, want 100", pending)
	}

	counting := newCountingPublisher(sqs.NewPublisher(m.client, m.queues.EventsURL))
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			publisher := m.outboxPublisher(counting, m.service.Outbox, time.Minute)
			for {
				published, err := publisher.PublishPending(context.Background())
				if err != nil {
					t.Errorf("PublishPending: %v", err)
					return
				}
				if published == 0 {
					return
				}
			}
		})
	}
	group.Wait()

	counts := counting.snapshot()
	if len(counts) != 100 || m.pendingEvents(t) != 0 {
		t.Fatalf("published %d distinct events with %d still pending, want all 100", len(counts), m.pendingEvents(t))
	}
	for id, times := range counts {
		if times != 1 {
			t.Fatalf("event %s was published %d times, want each one reserved by a single publisher", id, times)
		}
	}
	m.waitDepth(t, m.queues.EventsURL, 100, 10*time.Second)
}

type forgetfulOutbox struct {
	app.OutboxStore
	forget atomic.Bool
}

func (o *forgetfulOutbox) MarkPublished(ctx context.Context, eventID ids.EventID) error {
	if o.forget.Load() {
		return errors.New("process killed before confirming the publication")
	}
	return o.OutboxStore.MarkPublished(ctx, eventID)
}

func TestPublicationRecoversAfterAnInterruption(t *testing.T) {
	ctx := context.Background()
	m := newMessaging(t)
	w := m.open(t, "0.00")
	m.submit(t, operation(t, w, wager.Win, "1.00", "tx-1", ""))
	if pending := m.pendingEvents(t); pending != 2 {
		t.Fatalf("pending events = %d, want the two of the committed operation that nobody published yet", pending)
	}

	counting := newCountingPublisher(sqs.NewPublisher(m.client, m.queues.EventsURL))
	dying := &forgetfulOutbox{OutboxStore: m.service.Outbox}
	dying.forget.Store(true)
	if _, err := m.outboxPublisher(counting, dying, time.Second).PublishPending(ctx); err == nil {
		t.Fatal("the interrupted publisher confirmed its publication")
	}
	if pending := m.pendingEvents(t); pending != 2 {
		t.Fatalf("pending events = %d, want both still pending after the interruption", pending)
	}

	survivor := m.outboxPublisher(counting, m.service.Outbox, time.Second)
	if published, err := survivor.PublishPending(ctx); err != nil || published != 0 {
		t.Fatalf("another publisher during the lease = %d, %v, want nothing: both events are still reserved", published, err)
	}
	time.Sleep(1500 * time.Millisecond)
	if published, err := survivor.PublishPending(ctx); err != nil || published != 2 {
		t.Fatalf("another publisher after the lease expired = %d, %v, want the two abandoned events taken over", published, err)
	}

	if m.pendingEvents(t) != 0 {
		t.Fatalf("pending events = %d, want none", m.pendingEvents(t))
	}
	republished := 0
	for _, times := range counting.snapshot() {
		if times == 2 {
			republished++
		}
	}
	if republished != 1 || len(counting.snapshot()) != 2 {
		t.Fatalf("publications = %v, want one event republished with the same id and the other published once", counting.snapshot())
	}
}

func TestEventsLeaveAfterTheBrokerComesBack(t *testing.T) {
	ctx := context.Background()
	m := newMessaging(t)
	w := m.open(t, "0.00")
	unreachable := sqs.NewClient(sqs.Config{Endpoint: "http://127.0.0.1:1", Region: sqstest.Region, AccessKeyID: "local", SecretAccessKey: "local"})
	offline := m.outboxPublisher(sqs.NewPublisher(unreachable, m.queues.EventsURL), m.service.Outbox, time.Second)

	m.submit(t, operation(t, w, wager.Win, "1.00", "tx-1", ""))
	short, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := offline.PublishPending(short); err != nil {
		t.Fatalf("PublishPending with the broker down: %v", err)
	}
	if bet := m.submit(t, operation(t, w, wager.Bet, "1.00", "tx-2", "")); bet.Status != wager.Processed {
		t.Fatalf("operation while the broker is down = %s, want PROCESSED", bet.Status)
	}

	var attempts int
	if err := m.pool.QueryRow(ctx, `SELECT COALESCE(max(attempts), 0) FROM outbox`).Scan(&attempts); err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	if attempts == 0 || m.pendingEvents(t) != 4 {
		t.Fatalf("attempts = %d, pending = %d, want the failures recorded and no event lost", attempts, m.pendingEvents(t))
	}
	if postponed := m.count(t, `SELECT count(*) FROM outbox WHERE attempts > 0 AND next_attempt_at > now()`); postponed == 0 {
		t.Fatal("a failed publication was not rescheduled for later")
	}

	online := m.outboxPublisher(sqs.NewPublisher(m.client, m.queues.EventsURL), m.service.Outbox, time.Second)
	deadline := time.Now().Add(30 * time.Second)
	for m.pendingEvents(t) > 0 && time.Now().Before(deadline) {
		if _, err := online.PublishPending(ctx); err != nil {
			t.Fatalf("PublishPending after the broker came back: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if m.pendingEvents(t) != 0 {
		t.Fatalf("pending events = %d after the broker came back, want none", m.pendingEvents(t))
	}
	m.waitDepth(t, m.queues.EventsURL, 4, 10*time.Second)
}

func TestPendingReferenceExpiresThroughTheWorker(t *testing.T) {
	ctx := context.Background()
	m := newMessaging(t)
	m.service.ReferenceTTL = 2 * time.Second
	w := m.open(t, "1000.00")
	refund := m.submit(t, operation(t, w, wager.Refund, "25.00", "tx-refund", "tx-never"))
	if refund.Status != wager.PendingReference {
		t.Fatalf("refund = %s, want PENDING_REFERENCE", refund.Status)
	}

	restarted := newService(m.pool)
	resolver := worker.Worker{
		Name: "pending-references", Interval: 200 * time.Millisecond, CycleTimeout: 5 * time.Second, Batch: 10,
		Logger: slog.New(slog.DiscardHandler),
		Cycle:  func(ctx context.Context) (int, error) { return restarted.ResolveDuePendingReferences(ctx, 10) },
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		resolver.Run(runCtx)
		close(done)
	}()
	defer func() {
		stop()
		<-done
	}()

	deadline := time.Now().Add(20 * time.Second)
	var stored wager.State
	for time.Now().Before(deadline) {
		var err error
		if stored, err = m.service.GetTransaction(ctx, app.Caller{}, refund.ID); err != nil {
			t.Fatalf("GetTransaction: %v", err)
		}
		if stored.Status.IsTerminal() {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if stored.Status != wager.Rejected || stored.FailureCode != failure.ReferenceNotFound {
		t.Fatalf("refund = %s %s, want REJECTED with REFERENCE_NOT_FOUND after the deadline", stored.Status, stored.FailureCode)
	}

	var rejections int
	err := m.pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type = 'WagerTransactionRejected' AND aggregate_id = $1`, refund.ID.String()).Scan(&rejections)
	if err != nil || rejections != 1 {
		t.Fatalf("rejection events = %d, %v, want 1", rejections, err)
	}
	m.wantWallet(t, w.ID, "1000.00", 1)
}
