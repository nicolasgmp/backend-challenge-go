package sqs

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"jungle-gaming-challeng/internal/app"
)

const (
	attributeReason        = "reason"
	attributeCorrelationID = "correlationId"
	maxRetryDelay          = 60 * time.Second
	maxBatch               = 10
	outcomeTimeout         = 5 * time.Second
)

type ConsumerConfig struct {
	QueueURL       string
	DLQURL         string
	Concurrency    int
	WaitTime       time.Duration
	HandleTimeout  time.Duration
	RetryBaseDelay time.Duration
}

type Consumer struct {
	client  *awssqs.Client
	handler *Handler
	metrics app.Metrics
	logger  *slog.Logger
	cfg     ConsumerConfig
}

func NewConsumer(client *awssqs.Client, handler *Handler, metrics app.Metrics, logger *slog.Logger, cfg ConsumerConfig) *Consumer {
	return &Consumer{client: client, handler: handler, metrics: metrics, logger: logger, cfg: cfg}
}

func (c *Consumer) Run(ctx context.Context) {
	for ctx.Err() == nil {
		messages, err := c.receive(ctx)
		if err != nil {
			c.pause(ctx, err)
			continue
		}
		var batch sync.WaitGroup
		for _, message := range messages {
			batch.Go(func() { c.process(ctx, message) })
		}
		batch.Wait()
	}
	c.logger.Info("consumer stopped")
}

func (c *Consumer) receive(ctx context.Context) ([]types.Message, error) {
	output, err := c.client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:                    aws.String(c.cfg.QueueURL),
		MaxNumberOfMessages:         int32(min(c.cfg.Concurrency, maxBatch)),
		WaitTimeSeconds:             int32(c.cfg.WaitTime / time.Second),
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameApproximateReceiveCount, types.MessageSystemAttributeNameMessageGroupId},
		MessageAttributeNames:       []string{attributeCorrelationID},
	})
	if err != nil {
		return nil, err
	}
	return output.Messages, nil
}

func (c *Consumer) pause(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}
	c.logger.Warn("receive failed", slog.String("error", err.Error()))
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
	}
}

func (c *Consumer) process(ctx context.Context, message types.Message) {
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.cfg.HandleTimeout)
	defer cancel()

	outcome := c.handler.Handle(work, aws.ToString(message.Body), correlationOf(message))

	apply, cancelApply := context.WithTimeout(context.WithoutCancel(ctx), outcomeTimeout)
	defer cancelApply()
	var err error
	switch outcome.Action {
	case Delete:
		err = c.delete(apply, message)
	case DeadLetter:
		err = c.deadLetter(apply, message, outcome.Reason)
	default:
		err = c.postpone(apply, message, ctx.Err() != nil)
	}
	if err != nil {
		c.logger.Warn("message outcome not applied", slog.String("action", string(outcome.Action)), slog.String("error", err.Error()))
	}
}

func (c *Consumer) delete(ctx context.Context, message types.Message) error {
	_, err := c.client.DeleteMessage(ctx, &awssqs.DeleteMessageInput{
		QueueUrl:      aws.String(c.cfg.QueueURL),
		ReceiptHandle: message.ReceiptHandle,
	})
	return err
}

func (c *Consumer) deadLetter(ctx context.Context, message types.Message, reason string) error {
	_, err := c.client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:               aws.String(c.cfg.DLQURL),
		MessageBody:            message.Body,
		MessageGroupId:         aws.String(message.Attributes[string(types.MessageSystemAttributeNameMessageGroupId)]),
		MessageDeduplicationId: message.MessageId,
		MessageAttributes: map[string]types.MessageAttributeValue{
			attributeReason: {DataType: aws.String("String"), StringValue: aws.String(reason)},
		},
	})
	if err != nil {
		return err
	}
	c.metrics.DeadLetter(reason)
	return c.delete(ctx, message)
}

func (c *Consumer) postpone(ctx context.Context, message types.Message, shuttingDown bool) error {
	received := receiveCount(message)
	delay := retryDelay(c.cfg.RetryBaseDelay, received)
	if shuttingDown {
		delay = 0
	}
	if received >= MaxReceiveCount {
		c.metrics.DeadLetter(ReasonRetriesExceeded)
		delay = 0
	}
	_, err := c.client.ChangeMessageVisibility(ctx, &awssqs.ChangeMessageVisibilityInput{
		QueueUrl:          aws.String(c.cfg.QueueURL),
		ReceiptHandle:     message.ReceiptHandle,
		VisibilityTimeout: int32(delay / time.Second),
	})
	return err
}

func retryDelay(base time.Duration, received int) time.Duration {
	delay := base
	for i := 1; i < received && delay < maxRetryDelay; i++ {
		delay *= 2
	}
	return min(delay, maxRetryDelay)
}

func receiveCount(message types.Message) int {
	count, err := strconv.Atoi(message.Attributes[string(types.MessageSystemAttributeNameApproximateReceiveCount)])
	if err != nil {
		return 1
	}
	return count
}

func correlationOf(message types.Message) string {
	return aws.ToString(message.MessageAttributes[attributeCorrelationID].StringValue)
}
