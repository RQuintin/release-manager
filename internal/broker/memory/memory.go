package memory

import (
	"context"
	"time"

	"github.com/lunarway/release-manager/internal/broker"
	"github.com/lunarway/release-manager/internal/log"
)

type Broker struct {
	logger *log.Logger
	queue  chan broker.Publishable
}

// New allocates and returns an in-memory Broker with provided queue size.
func New(logger *log.Logger, queueSize int) *Broker {
	return &Broker{
		logger: logger,
		queue:  make(chan broker.Publishable, queueSize),
	}
}

func (b *Broker) Close() error {
	close(b.queue)
	return nil
}

func (b *Broker) Publish(ctx context.Context, event broker.Publishable) error {
	b.logger.WithFields("message", event).Info("Publishing message")
	now := time.Now()
	b.queue <- event
	duration := time.Since(now).Milliseconds()
	b.logger.With(
		"eventType", event.Type(),
		"res", map[string]interface{}{
			"status":       "ok",
			"responseTime": duration,
		}).Info("[publisher] [OK] Published message successfully")
	return nil
}

func (b *Broker) StartConsumer(handlers broker.Handlers) error {
	for msg := range b.queue {
		handler, ok := handlers[msg.Type()]
		if !ok {
			b.logger.With(
				"eventType", msg.Type(),
				"res", map[string]interface{}{
					"status": "failed",
					"error":  "unprocessable",
				}).Errorf("mem [consumer] [UNPROCESSABLE] Failed to handle message: no handler registered for event type '%s': dropping it", msg.Type)
			continue
		}
		ctx := context.Background()
		handler.Handle(ctx, &message{
			p: msg,
		})
	}
	return broker.ErrBrokerClosed
}

type message struct {
	p broker.Publishable
}

var _ broker.Message = &message{}

func (m *message) Type() string {
	return m.p.Type()
}

func (m *message) Body() []byte {
	b, _ := m.p.Marshal()
	return b
}
