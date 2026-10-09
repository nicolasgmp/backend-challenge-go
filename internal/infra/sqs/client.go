package sqs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"jungle-gaming-challeng/internal/app"
)

const (
	InboundQueue = "wager-transactions.fifo"
	InboundDLQ   = "wager-transactions-dlq.fifo"
	EventsQueue  = "wallet-events.fifo"
	EventsDLQ    = "wallet-events-dlq.fifo"

	VisibilityTimeout = 30 * time.Second
	MaxReceiveCount   = 5
)

var ErrInvalidConfig = errors.New("sqs: invalid configuration")

type Config struct {
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
}

type Queues struct {
	InboundURL    string
	InboundDLQURL string
	EventsURL     string
	EventsDLQURL  string
}

func NewClient(cfg Config) (*awssqs.Client, error) {
	if cfg.Endpoint == "" || cfg.Region == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, ErrInvalidConfig
	}
	return awssqs.New(awssqs.Options{
		Region:       cfg.Region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
	}), nil
}

func EnsureQueues(ctx context.Context, client *awssqs.Client) (Queues, error) {
	var queues Queues
	var err error
	if queues.InboundURL, queues.InboundDLQURL, err = ensurePair(ctx, client, InboundQueue, InboundDLQ); err != nil {
		return Queues{}, err
	}
	if queues.EventsURL, queues.EventsDLQURL, err = ensurePair(ctx, client, EventsQueue, EventsDLQ); err != nil {
		return Queues{}, err
	}
	return queues, nil
}

func FindQueues(ctx context.Context, client *awssqs.Client) (Queues, error) {
	var queues Queues
	var err error
	if queues.InboundURL, err = findQueue(ctx, client, InboundQueue); err != nil {
		return Queues{}, err
	}
	if queues.InboundDLQURL, err = findQueue(ctx, client, InboundDLQ); err != nil {
		return Queues{}, err
	}
	if queues.EventsURL, err = findQueue(ctx, client, EventsQueue); err != nil {
		return Queues{}, err
	}
	if queues.EventsDLQURL, err = findQueue(ctx, client, EventsDLQ); err != nil {
		return Queues{}, err
	}
	return queues, nil
}

func findQueue(ctx context.Context, client *awssqs.Client, name string) (string, error) {
	found, err := client.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err != nil {
		return "", unavailable("find queue "+name, err)
	}
	return aws.ToString(found.QueueUrl), nil
}

func Ping(ctx context.Context, client *awssqs.Client, queueURL string) error {
	_, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueURL),
		AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
	})
	if err != nil {
		return unavailable("ping", err)
	}
	return nil
}

func ensurePair(ctx context.Context, client *awssqs.Client, name, dlqName string) (string, string, error) {
	dlq, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName:  aws.String(dlqName),
		Attributes: map[string]string{"FifoQueue": "true"},
	})
	if err != nil {
		return "", "", unavailable("create queue "+dlqName, err)
	}
	attributes, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       dlq.QueueUrl,
		AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
	})
	if err != nil {
		return "", "", unavailable("read queue "+dlqName, err)
	}
	redrive, err := json.Marshal(map[string]string{
		"deadLetterTargetArn": attributes.Attributes[string(types.QueueAttributeNameQueueArn)],
		"maxReceiveCount":     strconv.Itoa(MaxReceiveCount),
	})
	if err != nil {
		return "", "", err
	}
	queue, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: aws.String(name),
		Attributes: map[string]string{
			"FifoQueue":         "true",
			"VisibilityTimeout": strconv.Itoa(int(VisibilityTimeout / time.Second)),
			"RedrivePolicy":     string(redrive),
		},
	})
	if err != nil {
		return "", "", unavailable("create queue "+name, err)
	}
	return aws.ToString(queue.QueueUrl), aws.ToString(dlq.QueueUrl), nil
}

func unavailable(operation string, err error) error {
	return fmt.Errorf("%w: sqs %s: %w", app.ErrTransient, operation, err)
}
