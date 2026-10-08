//go:build integration

package sqstest

import (
	"context"
	"encoding/json"
	"fmt"
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
	startupTimeout       = 60 * time.Second
	localAccessKeyID     = "local-test-access-key"
	localSecretAccessKey = "local-test-secret-key"
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

	began := time.Now()

	ctr, err := testcontainers.Run(ctx, Image,
		testcontainers.WithExposedPorts(gatewayPort),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP(healthPath).WithPort(gatewayPort).WithStartupTimeout(startupTimeout),
		),
	)
	testcontainers.CleanupContainer(tb, ctr)
	if err != nil {
		tb.Fatalf("sqstest: start %s: %v", Image, err)
	}

	endpoint, err := ctr.PortEndpoint(ctx, gatewayPort, "http")
	if err != nil {
		tb.Fatalf("sqstest: resolve endpoint: %v", err)
	}

	client := sqs.New(sqs.Options{
		Region:       Region,
		BaseEndpoint: aws.String(endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(localAccessKeyID, localSecretAccessKey, ""),
	})

	if err := waitForSQS(ctx, client); err != nil {
		tb.Fatalf("sqstest: %v", err)
	}
	tb.Logf("sqstest: %s ready in %s", Image, time.Since(began).Round(10*time.Millisecond))

	return client
}

func CreateFIFOQueue(ctx context.Context, tb testing.TB, client *sqs.Client, cfg QueueConfig) Queues {
	tb.Helper()

	queues, err := createFIFOQueue(ctx, client, cfg)
	if err != nil {
		tb.Fatalf("sqstest: create queue %q: %v", cfg.Name, err)
	}
	return queues
}

func createFIFOQueue(ctx context.Context, client *sqs.Client, cfg QueueConfig) (Queues, error) {
	fifo := string(types.QueueAttributeNameFifoQueue)

	dlq, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  aws.String(cfg.Name + "-dlq.fifo"),
		Attributes: map[string]string{fifo: "true"},
	})
	if err != nil {
		return Queues{}, fmt.Errorf("create dead-letter queue: %w", err)
	}

	arnName := types.QueueAttributeNameQueueArn
	attrs, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       dlq.QueueUrl,
		AttributeNames: []types.QueueAttributeName{arnName},
	})
	if err != nil {
		return Queues{}, fmt.Errorf("read dead-letter queue ARN: %w", err)
	}
	dlqARN, ok := attrs.Attributes[string(arnName)]
	if !ok {
		return Queues{}, fmt.Errorf("dead-letter queue has no %s attribute", arnName)
	}

	redrive, err := json.Marshal(map[string]string{
		"deadLetterTargetArn": dlqARN,
		"maxReceiveCount":     strconv.Itoa(cfg.MaxReceiveCount),
	})
	if err != nil {
		return Queues{}, fmt.Errorf("encode redrive policy: %w", err)
	}

	source, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(cfg.Name + ".fifo"),
		Attributes: map[string]string{
			fifo: "true",
			string(types.QueueAttributeNameVisibilityTimeout): strconv.Itoa(int(cfg.VisibilityTimeout / time.Second)),
			string(types.QueueAttributeNameRedrivePolicy):     string(redrive),
		},
	})
	if err != nil {
		return Queues{}, fmt.Errorf("create source queue: %w", err)
	}

	return Queues{URL: aws.ToString(source.QueueUrl), DLQURL: aws.ToString(dlq.QueueUrl)}, nil
}

func waitForSQS(ctx context.Context, client *sqs.Client) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		_, err := client.ListQueues(ctx, &sqs.ListQueuesInput{})
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("SQS not usable: %w (last error: %v)", ctx.Err(), err)
		case <-ticker.C:
		}
	}
}
