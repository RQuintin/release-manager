package broker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lunarway/release-manager/internal/broker"
	"github.com/lunarway/release-manager/internal/broker/amqp"
	"github.com/lunarway/release-manager/internal/broker/memory"
	"github.com/lunarway/release-manager/internal/broker/secureamqp"
	"github.com/lunarway/release-manager/internal/log"
	"github.com/lunarway/release-manager/internal/test"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zapcore"
)

// TestBrokers_PublishAndConsumer tests that we can publish and receive messages
// with different broker implementations
func TestBrokers_PublishAndConsumer(t *testing.T) {
	rabbitHost := test.RabbitMQIntegration(t)
	epoch := time.Now().UnixNano()
	tt := []struct {
		name   string
		broker func(log *log.Logger) (broker.Broker, error)
	}{
		{
			name: "memory",
			broker: func(logger *log.Logger) (broker.Broker, error) {
				return memory.New(logger, 5), nil
			},
		},
		{
			name: "amqp",
			broker: func(logger *log.Logger) (broker.Broker, error) {
				exchange := fmt.Sprintf("rm-test-exchange-%d", epoch)
				queue := fmt.Sprintf("rm-test-queue-%d", epoch)
				return amqp.NewWorker(amqp.Config{
					Connection: amqp.ConnectionConfig{
						Host:        rabbitHost,
						User:        "lunar",
						Password:    "lunar",
						VirtualHost: "/",
						Port:        5672,
					},
					ReconnectionTimeout: 50 * time.Millisecond,
					Exchange:            exchange,
					Queue:               queue,
					RoutingKey:          "#",
					Prefetch:            10,
					Logger:              logger,
				})
			},
		},
		{
			name: "secureamqp",
			broker: func(logger *log.Logger) (broker.Broker, error) {
				queue := fmt.Sprintf("rm-test-queue-%d", epoch)
				return secureamqp.New(secureamqp.Config{
					Connection: secureamqp.ConnectionConfig{
						Host:        rabbitHost,
						User:        "lunar",
						Password:    "lunar",
						VirtualHost: "/",
						Port:        5672,
					},
					ReconnectionTimeout: 50 * time.Millisecond,
					Exchange:            "",
					Queue:               queue,
					RoutingKey:          queue,
					Prefetch:            10,
					Logger:              logger,
				}), nil
			},
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			logger := log.New(&log.Configuration{
				Level: log.Level{
					Level: zapcore.DebugLevel,
				},
				Development: true,
			})

			publishedMessages := 100
			var receivedCount int32
			receivedAllEvents := make(chan struct{})

			brokerImplementation, err := tc.broker(logger)
			if !assert.NoError(t, err, "failed to instantiate broker") {
				return
			}

			time.Sleep(1 * time.Second)
			var consumerWg sync.WaitGroup
			consumerWg.Add(1)
			go func() {
				defer consumerWg.Done()
				err := brokerImplementation.StartConsumer(map[string]func([]byte) error{
					testEvent{}.Type(): func(d []byte) error {
						newCount := atomic.AddInt32(&receivedCount, 1)
						if int(newCount) == publishedMessages {
							close(receivedAllEvents)
						}
						var msg testEvent
						err := json.Unmarshal(d, &msg)
						if err != nil {
							return err
						}
						logger.Infof("Received %s", msg.Message)
						return nil
					},
				})
				assert.EqualError(t, err, broker.ErrBrokerClosed.Error(), "unexpected consumer error")
			}()

			var publisherWg sync.WaitGroup
			publisherWg.Add(1)
			go func() {
				logger.Infof("TEST: Starting to publish %d messages", publishedMessages)
				defer publisherWg.Done()
				for i := 1; i <= publishedMessages; i++ {
					logger.Infof("TEST: Published message %d", i)
					err := brokerImplementation.Publish(context.Background(), &testEvent{
						Message: fmt.Sprintf("Message %d", i),
					})
					assert.NoError(t, err, "unexpected error publishing message")
				}
			}()
			// block until all messages are sent
			publisherWg.Wait()

			// block until all messages are received with a timeout
			select {
			case <-receivedAllEvents:
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting to receive all events")
			}

			err = brokerImplementation.Close()
			assert.NoError(t, err, "unexpected close error")

			// wait for consumer Go routine to exit
			consumerWg.Wait()
			assert.Equal(t, publishedMessages, int(receivedCount), "received messages count not as expected")
		})
	}
}

type testEvent struct {
	Message string `json:"message"`
}

func (testEvent) Type() string {
	return "test-event"
}

func (t testEvent) Marshal() ([]byte, error) {
	return json.Marshal(t)
}

func (t *testEvent) Unmarshal(d []byte) error {
	return json.Unmarshal(d, t)
}
