//go:build integration

package sqstest_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"jungle-gaming-challeng/internal/infra/sqs/sqstest"
)

func TestFIFOFidelity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := sqstest.Start(ctx, t)

	t.Run("group is blocked while a message is in flight", func(t *testing.T) {
		q := sqstest.CreateFIFOQueue(ctx, t, client, sqstest.QueueConfig{
			Name: "blocking", VisibilityTimeout: 60 * time.Second, MaxReceiveCount: 5,
		})
		send(ctx, t, client, q.URL, "group-a", "a-1")
		send(ctx, t, client, q.URL, "group-a", "a-2")

		first := receiveOne(ctx, t, client, q.URL)
		if got := aws.ToString(first.Body); got != "a-1" {
			t.Fatalf("first receive: got %q, want %q", got, "a-1")
		}
		if msgs := receive(ctx, t, client, q.URL); len(msgs) != 0 {
			t.Fatalf("group-a delivered %q while a-1 is in flight", aws.ToString(msgs[0].Body))
		}

		send(ctx, t, client, q.URL, "group-b", "b-1")
		if got := aws.ToString(receiveOne(ctx, t, client, q.URL).Body); got != "b-1" {
			t.Fatalf("other group: got %q, want %q", got, "b-1")
		}

		_, err := client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
			QueueUrl:      aws.String(q.URL),
			ReceiptHandle: first.ReceiptHandle,
		})
		if err != nil {
			t.Fatalf("delete a-1: %v", err)
		}
		if got := aws.ToString(receiveOne(ctx, t, client, q.URL).Body); got != "a-2" {
			t.Fatalf("after delete: got %q, want %q", got, "a-2")
		}
	})

	t.Run("message is redelivered after the visibility timeout", func(t *testing.T) {
		q := sqstest.CreateFIFOQueue(ctx, t, client, sqstest.QueueConfig{
			Name: "visibility", VisibilityTimeout: 3 * time.Second, MaxReceiveCount: 5,
		})
		send(ctx, t, client, q.URL, "group-a", "only")

		receiveOne(ctx, t, client, q.URL)
		if msgs := receive(ctx, t, client, q.URL); len(msgs) != 0 {
			t.Fatal("message redelivered before the visibility timeout")
		}

		again := receiveOne(ctx, t, client, q.URL)
		if got := receiveCount(again); got != "2" {
			t.Fatalf("ApproximateReceiveCount = %q, want %q", got, "2")
		}
	})

	t.Run("message goes to the DLQ after 5 receives", func(t *testing.T) {
		q := sqstest.CreateFIFOQueue(ctx, t, client, sqstest.QueueConfig{
			Name: "redrive", VisibilityTimeout: time.Second, MaxReceiveCount: 5,
		})
		send(ctx, t, client, q.URL, "group-a", "poison")

		for i := 0; i < 5; i++ {
			receiveOne(ctx, t, client, q.URL)
		}

		var dead []types.Message
		deadline := time.Now().Add(15 * time.Second)
		for len(dead) == 0 && time.Now().Before(deadline) {
			if msgs := receive(ctx, t, client, q.URL); len(msgs) != 0 {
				t.Fatalf("source delivered a sixth time (ApproximateReceiveCount = %q)", receiveCount(msgs[0]))
			}
			dead = receive(ctx, t, client, q.DLQURL)
		}
		if len(dead) != 1 || aws.ToString(dead[0].Body) != "poison" {
			t.Fatalf("DLQ: got %d messages, want 1 with body %q", len(dead), "poison")
		}
	})
}

func send(ctx context.Context, t *testing.T, client *sqs.Client, queueURL, group, body string) {
	t.Helper()

	_, err := client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               aws.String(queueURL),
		MessageBody:            aws.String(body),
		MessageGroupId:         aws.String(group),
		MessageDeduplicationId: aws.String(body),
	})
	if err != nil {
		t.Fatalf("send %s/%s: %v", group, body, err)
	}
}

func receive(ctx context.Context, t *testing.T, client *sqs.Client, queueURL string) []types.Message {
	t.Helper()

	out, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:                    aws.String(queueURL),
		MaxNumberOfMessages:         1,
		WaitTimeSeconds:             1,
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameAll},
	})
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	return out.Messages
}

func receiveOne(ctx context.Context, t *testing.T, client *sqs.Client, queueURL string) types.Message {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if msgs := receive(ctx, t, client, queueURL); len(msgs) != 0 {
			return msgs[0]
		}
	}
	t.Fatal("no message received within 15s")
	return types.Message{}
}

func receiveCount(msg types.Message) string {
	return msg.Attributes[string(types.MessageSystemAttributeNameApproximateReceiveCount)]
}
