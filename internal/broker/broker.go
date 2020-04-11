package broker

import (
	"context"
	"errors"
)

// Broker is capable of publishing and consuming Publishable messages.
type Broker interface {
	// Publish publishes a Publishable message on the broker.
	Publish(ctx context.Context, message Publishable) error
	// StartConsumer consumes messages on a broker. This method is blocking and
	// will always return with ErrBrokerClosed after calls to Close.
	StartConsumer(handlers Handlers) error
	// Close closes the broker.
	Close() error
}

// Publishable represents an enty capable of being published by a Broker.
type Publishable interface {
	Type() string
	Marshal() ([]byte, error)
}

type Message interface {
	Type() string
	Body() []byte
}

type Handlers map[string]Handler

type Handler interface {
	Handle(context.Context, Message) error
}

type HandleFunc func(context.Context, Message) error

var _ Handler = HandleFunc(nil)

func (f HandleFunc) Handle(ctx context.Context, m Message) error {
	return f(ctx, m)
}

// ErrBrokerClosed indicates that the broker was closed by a call to Close.
var ErrBrokerClosed = errors.New("broker: broker closed")
