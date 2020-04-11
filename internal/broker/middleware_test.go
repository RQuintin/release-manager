package broker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lunarway/release-manager/internal/broker"
	"github.com/lunarway/release-manager/internal/log"
	"go.uber.org/zap/zapcore"
)

func TestLoggingHandlers(t *testing.T) {
	tt := []struct {
		name       string
		handlerErr error
	}{
		{
			name:       "no handler error",
			handlerErr: nil,
		},
		{
			name:       "simple handler error",
			handlerErr: errors.New("an error"),
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
			handlers := broker.LoggingHandlers(logger, broker.Handlers{
				"testEvent": broker.HandleFunc(func(ctx context.Context, msg broker.Message) error {
					return tc.handlerErr
				}),
			})

			handlers["testEvent"].Handle(context.Background(), &message{
				messageType: "testEvent",
				body:        []byte(`some data`),
			})
		})
	}
}

type message struct {
	messageType string
	body        []byte
}

var _ broker.Message = &message{}

func (m *message) Type() string {
	return m.messageType
}

func (m *message) Body() []byte {
	return m.body
}
