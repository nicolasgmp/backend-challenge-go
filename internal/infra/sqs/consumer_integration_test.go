//go:build integration

package sqs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/infra/sqs/sqstest"
)

func depth(t *testing.T, client *awssqs.Client, queueURL string) int {
	t.Helper()

	output, err := client.GetQueueAttributes(context.Background(), &awssqs.GetQueueAttributesInput{
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

func waitEmpty(t *testing.T, client *awssqs.Client, queueURL string, limit time.Duration) time.Duration {
	t.Helper()

	started := time.Now()
	for time.Since(started) < limit {
		if depth(t, client, queueURL) == 0 {
			return time.Since(started)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("queue still has %d messages after %s", depth(t, client, queueURL), limit)
	return 0
}

func send(t *testing.T, client *awssqs.Client, queueURL, group, deduplication, body string) {
	t.Helper()

	_, err := client.SendMessage(context.Background(), &awssqs.SendMessageInput{
		QueueUrl:               aws.String(queueURL),
		MessageBody:            aws.String(body),
		MessageGroupId:         aws.String(group),
		MessageDeduplicationId: aws.String(deduplication),
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
}

func receiveOne(t *testing.T, client *awssqs.Client, queueURL string, wait time.Duration) (types.Message, bool) {
	t.Helper()

	output, err := client.ReceiveMessage(context.Background(), &awssqs.ReceiveMessageInput{
		QueueUrl:                    aws.String(queueURL),
		MaxNumberOfMessages:         1,
		WaitTimeSeconds:             int32(wait / time.Second),
		MessageAttributeNames:       []string{"All"},
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameAll},
	})
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if len(output.Messages) == 0 {
		return types.Message{}, false
	}
	return output.Messages[0], true
}

func startConsumer(t *testing.T, client *awssqs.Client, queues Queues, handler *Handler, logger *slog.Logger) (stop func(), done <-chan struct{}) {
	t.Helper()

	consumer := NewConsumer(client, handler, handler.Metrics, logger, ConsumerConfig{
		QueueURL: queues.InboundURL, DLQURL: queues.InboundDLQURL,
		Concurrency: 1, WaitTime: time.Second, HandleTimeout: 10 * time.Second, RetryBaseDelay: 2 * time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		consumer.Run(ctx)
		close(finished)
	}()
	t.Cleanup(func() {
		cancel()
		<-finished
	})
	return cancel, finished
}

func TestEnsureQueuesIsIdempotent(t *testing.T) {
	ctx := context.Background()
	client := sqstest.Start(ctx, t)

	first, err := EnsureQueues(ctx, client)
	if err != nil {
		t.Fatalf("EnsureQueues: %v", err)
	}
	second, err := EnsureQueues(ctx, client)
	if err != nil || second != first {
		t.Fatalf("second run = %+v, %v, want the same queues as %+v", second, err, first)
	}
	found, err := FindQueues(ctx, client)
	if err != nil || found != first {
		t.Fatalf("FindQueues = %+v, %v, want %+v", found, err, first)
	}

	pairs := map[string]string{first.InboundURL: first.InboundDLQURL, first.EventsURL: first.EventsDLQURL}
	for queueURL, dlqURL := range pairs {
		attributes, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
			QueueUrl: aws.String(queueURL), AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameAll},
		})
		if err != nil {
			t.Fatalf("attributes of %s: %v", queueURL, err)
		}
		dlq, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
			QueueUrl: aws.String(dlqURL), AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
		})
		if err != nil {
			t.Fatalf("attributes of %s: %v", dlqURL, err)
		}
		var redrive map[string]any
		if err := json.Unmarshal([]byte(attributes.Attributes["RedrivePolicy"]), &redrive); err != nil {
			t.Fatalf("redrive policy of %s: %v", queueURL, err)
		}
		if redrive["deadLetterTargetArn"] != dlq.Attributes["QueueArn"] || fmt.Sprint(redrive["maxReceiveCount"]) != "5" {
			t.Fatalf("redrive policy of %s = %v, want its dead-letter queue after 5 receives", queueURL, redrive)
		}
		if attributes.Attributes["VisibilityTimeout"] != "30" || attributes.Attributes["FifoQueue"] != "true" {
			t.Fatalf("attributes of %s = %v, want a FIFO queue with a 30s visibility timeout", queueURL, attributes.Attributes)
		}
	}

	if err := Ping(ctx, client, first.InboundURL); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := Ping(ctx, client, first.InboundURL+"-missing"); !errors.Is(err, app.ErrTransient) {
		t.Fatalf("Ping of a missing queue: err = %v, want %v", err, app.ErrTransient)
	}
	unreachable, err := NewClient(Config{Endpoint: "http://127.0.0.1:1", Region: "us-east-1", AccessKeyID: "local", SecretAccessKey: "local"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	short, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := Ping(short, unreachable, first.InboundURL); !errors.Is(err, app.ErrTransient) {
		t.Fatalf("Ping of an unreachable broker: err = %v, want %v", err, app.ErrTransient)
	}
}

func TestConsumerAgainstMiniStack(t *testing.T) {
	ctx := context.Background()
	client := sqstest.Start(ctx, t)
	queues, err := EnsureQueues(ctx, client)
	if err != nil {
		t.Fatalf("EnsureQueues: %v", err)
	}
	f := newFixture(t, "1000.00")
	group := f.wallet.ID.String()
	stop, done := startConsumer(t, client, queues, f.handler, f.handler.Logger)

	send(t, client, queues.InboundURL, group, "msg-invalid", "not json")
	send(t, client, queues.InboundURL, group, "msg-1", f.message("msg-1", "BET", "25.00", "tx-1"))
	if waited := waitEmpty(t, client, queues.InboundURL, 15*time.Second); waited > 10*time.Second {
		t.Fatalf("the valid message waited %s behind an invalid one, want no wait for the redrive", waited)
	}

	deadLetter, found := receiveOne(t, client, queues.InboundDLQURL, 5*time.Second)
	if !found || aws.ToString(deadLetter.Body) != "not json" {
		t.Fatalf("dead-letter queue = %+v, want the invalid message", deadLetter)
	}
	if reason := aws.ToString(deadLetter.MessageAttributes["reason"].StringValue); reason != ReasonInvalidMessage {
		t.Fatalf("reason attribute = %q, want %q", reason, ReasonInvalidMessage)
	}
	if f.balance(t) != "975.00" {
		t.Fatalf("balance = %s, want the valid message processed", f.balance(t))
	}

	f.store.FailNext("wallets.GetForUpdate", app.ErrTransient)
	send(t, client, queues.InboundURL, group, "msg-2", f.message("msg-2", "BET", "25.00", "tx-2"))
	if waited := waitEmpty(t, client, queues.InboundURL, 20*time.Second); waited < time.Second {
		t.Fatalf("a message that failed once was processed after %s, want the retry delayed by the visibility change", waited)
	}
	if f.balance(t) != "950.00" {
		t.Fatalf("balance = %s, want the retried message processed exactly once", f.balance(t))
	}

	stop()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the consumer did not stop")
	}
	if !strings.Contains(f.logs.String(), "consumer stopped") {
		t.Fatal("the consumer did not log that it stopped")
	}
	send(t, client, queues.InboundURL, group, "msg-3", f.message("msg-3", "BET", "25.00", "tx-3"))
	time.Sleep(2 * time.Second)
	if depth(t, client, queues.InboundURL) != 1 {
		t.Fatal("a stopped consumer fetched a message")
	}
}

type blockingSubmitter struct {
	started chan struct{}
	release chan struct{}
}

func (s blockingSubmitter) SubmitTransaction(context.Context, app.SubmitInput) (app.SubmitResult, error) {
	close(s.started)
	<-s.release
	return app.SubmitResult{}, nil
}

func TestConsumerFinishesTheMessageInFlightOnShutdown(t *testing.T) {
	ctx := context.Background()
	client := sqstest.Start(ctx, t)
	queues, err := EnsureQueues(ctx, client)
	if err != nil {
		t.Fatalf("EnsureQueues: %v", err)
	}
	f := newFixture(t, "1000.00")
	submitter := blockingSubmitter{started: make(chan struct{}), release: make(chan struct{})}
	f.handler.Service = submitter
	stop, done := startConsumer(t, client, queues, f.handler, f.handler.Logger)

	send(t, client, queues.InboundURL, f.wallet.ID.String(), "msg-1", f.message("msg-1", "BET", "25.00", "tx-1"))
	<-submitter.started
	stop()

	select {
	case <-done:
		t.Fatal("the consumer returned while a message was still being handled")
	case <-time.After(time.Second):
	}
	close(submitter.release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the consumer did not stop after the message in flight finished")
	}
	if depth(t, client, queues.InboundURL) != 0 {
		t.Fatal("the message in flight was not deleted after its handling finished")
	}
}

func TestPostponeControlsTheRedelivery(t *testing.T) {
	ctx := context.Background()
	client := sqstest.Start(ctx, t)
	queues, err := EnsureQueues(ctx, client)
	if err != nil {
		t.Fatalf("EnsureQueues: %v", err)
	}
	f := newFixture(t, "1000.00")
	consumer := NewConsumer(client, f.handler, f.metrics, f.handler.Logger, ConsumerConfig{
		QueueURL: queues.InboundURL, DLQURL: queues.InboundDLQURL, RetryBaseDelay: 3 * time.Second,
	})
	send(t, client, queues.InboundURL, "group-1", "msg-1", "body")

	first, found := receiveOne(t, client, queues.InboundURL, 5*time.Second)
	if !found {
		t.Fatal("the message was not delivered")
	}
	if err := consumer.postpone(ctx, first, false); err != nil {
		t.Fatalf("postpone: %v", err)
	}
	started := time.Now()
	if _, early := receiveOne(t, client, queues.InboundURL, time.Second); early {
		t.Fatal("the message came back before the delay asked for")
	}
	second, found := receiveOne(t, client, queues.InboundURL, 10*time.Second)
	if !found || time.Since(started) < 2*time.Second {
		t.Fatalf("redelivered = %t after %s, want it back only after about 3s", found, time.Since(started))
	}

	if err := consumer.postpone(ctx, second, true); err != nil {
		t.Fatalf("postpone on shutdown: %v", err)
	}
	if _, released := receiveOne(t, client, queues.InboundURL, time.Second); !released {
		t.Fatal("a message released on shutdown was not visible at once")
	}
}

func TestPublisherRoutesByWalletAndDeduplicatesByEvent(t *testing.T) {
	ctx := context.Background()
	client := sqstest.Start(ctx, t)
	queues, err := EnsureQueues(ctx, client)
	if err != nil {
		t.Fatalf("EnsureQueues: %v", err)
	}
	eventID, err := ids.NewEventID()
	if err != nil {
		t.Fatalf("NewEventID: %v", err)
	}
	record := app.OutboxRecord{EventID: eventID, GroupKey: "wallet-1", Payload: []byte(`{"eventId":"` + eventID.String() + `","data":{}}`)}
	publisher := NewPublisher(client, queues.EventsURL)

	for range 2 {
		if err := publisher.Publish(ctx, record); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}

	message, found := receiveOne(t, client, queues.EventsURL, 5*time.Second)
	if !found || aws.ToString(message.Body) != string(record.Payload) {
		t.Fatalf("message = %+v, want the event payload as the body", message)
	}
	if message.Attributes["MessageGroupId"] != "wallet-1" || message.Attributes["MessageDeduplicationId"] != eventID.String() {
		t.Fatalf("attributes = %v, want the wallet as group and the event id as deduplication id", message.Attributes)
	}
	if depth(t, client, queues.EventsURL) != 1 {
		t.Fatalf("queue depth = %d, want the republication within five minutes dropped by the broker", depth(t, client, queues.EventsURL))
	}

	unreachable, err := NewClient(Config{Endpoint: "http://127.0.0.1:1", Region: "us-east-1", AccessKeyID: "local", SecretAccessKey: "local"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	short, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := NewPublisher(unreachable, queues.EventsURL).Publish(short, record); !errors.Is(err, app.ErrTransient) {
		t.Fatalf("Publish to an unreachable broker: err = %v, want %v", err, app.ErrTransient)
	}
}
