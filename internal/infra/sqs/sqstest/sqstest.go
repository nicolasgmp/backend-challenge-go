//go:build integration

package sqstest

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	Image                = "ministackorg/ministack:1.5.23"
	Region               = "us-east-1"
	gatewayPort          = "4566/tcp"
	healthPath           = "/_ministack/health"
	LocalAccessKeyID     = "local-test-access-key"
	LocalSecretAccessKey = "local-test-secret-key"
)

type QueueConfig struct {
	Name              string
	VisibilityTimeout time.Duration
	MaxReceiveCount   int
}

type Queues struct {
	URL    string
	DLQURL string
}

func Start(ctx context.Context, tb testing.TB) *sqs.Client {
	tb.Helper()

	return sqs.New(sqs.Options{
		Region:       Region,
		BaseEndpoint: aws.String(StartEndpoint(ctx, tb)),
		Credentials:  credentials.NewStaticCredentialsProvider(LocalAccessKeyID, LocalSecretAccessKey, ""),
	})
}

func StartEndpoint(ctx context.Context, tb testing.TB) string {
	tb.Helper()

	ctr, err := testcontainers.Run(ctx, Image,
		testcontainers.WithExposedPorts(gatewayPort),
		testcontainers.WithWaitStrategy(wait.ForHTTP(healthPath).WithPort(gatewayPort)),
	)
	testcontainers.CleanupContainer(tb, ctr)
	if err != nil {
		tb.Fatalf("sqstest: start %s: %v", Image, err)
	}

	endpoint, err := ctr.PortEndpoint(ctx, gatewayPort, "http")
	if err != nil {
		tb.Fatalf("sqstest: resolve endpoint: %v", err)
	}
	return endpoint
}

func CreateFIFOQueue(ctx context.Context, tb testing.TB, client *sqs.Client, cfg QueueConfig) Queues {
	tb.Helper()

	dlq, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  aws.String(cfg.Name + "-dlq.fifo"),
		Attributes: map[string]string{"FifoQueue": "true"},
	})
	if err != nil {
		tb.Fatalf("sqstest: create dead-letter queue %q: %v", cfg.Name, err)
	}

	attrs, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       dlq.QueueUrl,
		AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
	})
	if err != nil {
		tb.Fatalf("sqstest: read dead-letter queue ARN %q: %v", cfg.Name, err)
	}

	redrive, err := json.Marshal(map[string]string{
		"deadLetterTargetArn": attrs.Attributes["QueueArn"],
		"maxReceiveCount":     strconv.Itoa(cfg.MaxReceiveCount),
	})
	if err != nil {
		tb.Fatalf("sqstest: encode redrive policy: %v", err)
	}

	queue, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(cfg.Name + ".fifo"),
		Attributes: map[string]string{
			"FifoQueue":         "true",
			"VisibilityTimeout": strconv.Itoa(int(cfg.VisibilityTimeout / time.Second)),
			"RedrivePolicy":     string(redrive),
		},
	})
	if err != nil {
		tb.Fatalf("sqstest: create queue %q: %v", cfg.Name, err)
	}

	return Queues{URL: aws.ToString(queue.QueueUrl), DLQURL: aws.ToString(dlq.QueueUrl)}
}
