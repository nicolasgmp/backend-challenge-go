package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"jungle-gaming-challeng/internal/infra/sqs"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "queues:", err)
		os.Exit(1)
	}
}

func run() error {
	client := sqs.NewClient(sqs.Config{
		Endpoint:        os.Getenv("SQS_ENDPOINT"),
		Region:          os.Getenv("AWS_REGION"),
		AccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	queues, err := sqs.EnsureQueues(ctx, client)
	if err != nil {
		return err
	}
	fmt.Println("queues ready:", queues.InboundURL, queues.InboundDLQURL, queues.EventsURL, queues.EventsDLQURL)
	return nil
}
