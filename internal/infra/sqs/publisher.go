package sqs

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"jungle-gaming-challeng/internal/app"
)

type Publisher struct {
	client   *awssqs.Client
	queueURL string
}

func NewPublisher(client *awssqs.Client, queueURL string) *Publisher {
	return &Publisher{client: client, queueURL: queueURL}
}

func (p *Publisher) Publish(ctx context.Context, record app.OutboxRecord) error {
	_, err := p.client.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:               aws.String(p.queueURL),
		MessageBody:            aws.String(string(record.Payload)),
		MessageGroupId:         aws.String(record.GroupKey),
		MessageDeduplicationId: aws.String(record.EventID.String()),
	})
	if err != nil {
		return unavailable("publish event", err)
	}
	return nil
}
