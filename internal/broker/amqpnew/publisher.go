package amqpnew

import (
	"context"
	"fmt"
	"time"

	"github.com/lunarway/release-manager/internal/broker"
	"github.com/lunarway/release-manager/internal/log"
	"github.com/lunarway/release-manager/internal/tracing"
	"github.com/pkg/errors"
	"github.com/streadway/amqp"
)

type rawPublisher struct {
	channel       *amqp.Channel
	confirmations chan amqp.Confirmation
	resendDelay   time.Duration
	exchangeName  string
	routingKey    string
	notifyRetry   func(ctx context.Context, reason error)
	closed        chan struct{}
}

func newPublisher(amqpConn *amqp.Connection, exchangeName string, notifyRetry func(ctx context.Context, reason error)) (publisher, error) {
	channel, err := amqpConn.Channel()
	if err != nil {
		return nil, errors.WithMessage(err, "get channel")
	}
	err = channel.Confirm(false)
	if err != nil {
		return nil, errors.WithMessage(err, "enable confirm mode")
	}
	confirmations := make(chan amqp.Confirmation, 1)
	channel.NotifyPublish(confirmations)
	return &rawPublisher{
		channel:       channel,
		exchangeName:  exchangeName,
		confirmations: confirmations,
		resendDelay:   1 * time.Second,
		notifyRetry:   notifyRetry,
		closed:        make(chan struct{}),
	}, nil
}

func (p *rawPublisher) Publish(ctx context.Context, eventType, messageID string, message []byte) error {
	pub := amqp.Publishing{
		ContentType:   "application/json",
		Type:          eventType,
		Body:          message,
		MessageId:     messageID,
		CorrelationId: tracing.RequestIDFromContext(ctx),
	}
	for {
		err := p.channel.Publish(p.exchangeName, p.routingKey, false, false, pub)
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-p.closed:
				return broker.ErrBrokerClosed
			case <-time.After(p.resendDelay):
				p.notifyRetry(ctx, err)
				continue
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.closed:
			return broker.ErrBrokerClosed
		case confirm, ok := <-p.confirmations:
			if !ok {
				return errors.New("confirmation channel closed")
			}
			if confirm.Ack {
				fmt.Println("Confirmed")
				return nil
			}
		case <-time.After(p.resendDelay):
			p.notifyRetry(ctx, errors.New("published timed out"))
		}
	}
}

func (p *rawPublisher) Close() error {
	fmt.Println("Closing publisher")
	close(p.closed)
	err := p.channel.Close()
	if err != nil {
		return errors.WithMessage(err, "close amqp channel")
	}
	return nil
}

type loggingPublisher struct {
	publisher publisher
	logger    *log.Logger
}

func (p *loggingPublisher) Publish(ctx context.Context, eventType, messageID string, message []byte) error {
	logger := p.logger.WithContext(ctx).WithFields("body", message)
	logger.Debug("Publishing message")
	now := time.Now()
	err := p.publisher.Publish(ctx, eventType, messageID, message)
	duration := time.Since(now).Milliseconds()
	if err != nil {
		logger.With(
			"messageId", messageID,
			"eventType", eventType,
			"correlationId", tracing.RequestIDFromContext(ctx),
			"res", map[string]interface{}{
				"status":       "failed",
				"responseTime": duration,
				"error":        err,
			}).Errorf("[publisher] [FAILED] Failed to publish message: %v", err)
		return err
	}
	logger.With(
		"messageId", messageID,
		"eventType", eventType,
		"correlationId", tracing.RequestIDFromContext(ctx),
		"res", map[string]interface{}{
			"status":       "ok",
			"responseTime": duration,
		}).Info("[publisher] [OK] Published message successfully")
	return nil
}

func (p *loggingPublisher) Close() error {
	return p.publisher.Close()
}
