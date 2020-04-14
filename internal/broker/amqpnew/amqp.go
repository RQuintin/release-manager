package amqpnew

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lunarway/release-manager/internal/broker"
	"github.com/lunarway/release-manager/internal/log"
	"github.com/pkg/errors"
	"github.com/streadway/amqp"
)

// Worker is a RabbtiMQ consumer and publisher. It will setup an AMQP channel
// to consume messages from an exchange through a queue and will withstand
// disconnects on connection to RabbtiMQ.
//
// Reconnection is implemented as a chan *connection that is consumed in the
// Start method. If connection loss is detected the reconnector Go routine will
// setup a new connection and push it on to the channel thus keeping Start
// blocking.
type Worker struct {
	config Config

	connection *amqp.Connection
	// connectionClosed is used to signal that a connection was lost and that the
	// reconnector should attempt to reestablish it.
	connectionClosed chan *amqp.Error
	done             chan struct{}

	// currentConsumer provides an active connection to consume from.
	currentConsumer chan *consumer
	// currentConsumer provides an active connection to publish on.
	currentPublisher publisher
	// shutdown is used to terminate the different Go routines in the worker. It
	// will be closed as a signal to stop.
	shutdown chan struct{}
}

type publisher interface {
	Publish(ctx context.Context, eventType, messageID string, message []byte) error
	Close() error
}

type Config struct {
	Connection          ConnectionConfig
	Exchange            string
	Queue               string
	RoutingKey          string
	Prefetch            int
	ReconnectionTimeout time.Duration
	AMQPConfig          *amqp.Config
	Logger              *log.Logger
}

type ConnectionConfig struct {
	Host        string
	User        string
	Password    string
	VirtualHost string
	Port        int
}

func (c *ConnectionConfig) Raw() string {
	return fmt.Sprintf("amqp://%s:%s@%s:%d/%s", c.User, c.Password, c.Host, c.Port, c.VirtualHost)
}

func (c *ConnectionConfig) String() string {
	return fmt.Sprintf("amqp://%s:%s@%s:%d/%s", c.User, "***", c.Host, c.Port, c.VirtualHost)
}

// NewWorker allocates and returns a Worker consuming and publising messages on
// an AMQP exchange.
func NewWorker(c Config) (*Worker, error) {
	worker := Worker{
		shutdown:         make(chan struct{}),
		currentConsumer:  make(chan *consumer),
		connectionClosed: make(chan *amqp.Error),
		config:           c,
	}
	go worker.handleReconnection()
	return &worker, nil
}

func (s *Worker) handleReconnection() {
	s.config.Logger.Info("Reconnector started")
	defer s.config.Logger.Info("Reconnector stopped")
	for {
		s.config.Logger.Info("Attempting to connect")

		err := s.connect()
		if err != nil {
			s.config.Logger.Infof("Failed to connect. Retrying: %v", err)
			select {
			case <-s.done:
				return
			case <-time.After(s.config.ReconnectionTimeout):
				continue
			}
		}

		done := s.handleReInit()
		if done {
			return
		}
	}
}

func (s *Worker) handleReInit() bool {
	for {
		err := s.init()
		if err != nil {
			s.config.Logger.Infof("Failed to initialize channel. Retrying: %v", err)
			select {
			case <-s.done:
				return true
			case amqpErr := <-s.connectionClosed:
				s.config.Logger.Infof("Connection closed: %v", amqpErr)
				return false
			}
		}
	}
}

func (s *Worker) connect() error {
	c := s.config
	logger := c.Logger.WithFields("host", c.Connection.Host, "user", c.Connection.User, "port", c.Connection.Port, "virtualHost", c.Connection.VirtualHost, "exchange", c.Exchange, "queue", c.Queue, "prefetch", c.Prefetch, "reconnectionTimeout", c.ReconnectionTimeout, "amqpConfig", fmt.Sprintf("%#v", c.AMQPConfig))
	logger.Infof("Connecting to: %s", c.Connection.String())

	if c.AMQPConfig != nil && c.Connection.VirtualHost != c.AMQPConfig.Vhost {
		logger.Infof("AMQP config overwrites provided virtual host")
	}
	if c.AMQPConfig == nil {
		c.AMQPConfig = &amqp.Config{
			Heartbeat: 60 * time.Second,
			Vhost:     c.Connection.VirtualHost,
		}
	}
	amqpConnection, err := amqp.DialConfig(c.Connection.Raw(), *c.AMQPConfig)
	if err != nil {
		return errors.WithMessage(err, "connect to amqp")
	}
	s.changeConnection(amqpConnection)
	logger.Infof("Connected to: %s", c.Connection.String())
	return nil
}

func (s *Worker) changeConnection(connection *amqp.Connection) {
	s.connection = connection
	s.connectionClosed = make(chan *amqp.Error)
	s.connection.NotifyClose(s.connectionClosed)
}

func (s *Worker) init() error {
	s.config.Logger.Info("Initializing consumer and publisher")
	c := s.config

	err := s.declareExchange()
	if err != nil {
		return errors.WithMessage(err, "declare exchange")
	}

	// TODO: this could be instantiated from a list of exchange and queue pairs to
	// setup more than one consumer
	consumer, err := newConsumer(s.connection, c.Exchange, c.Queue, c.RoutingKey, c.Prefetch)
	if err != nil {
		return errors.WithMessage(err, "create consumer")
	}

	rawPublisher, err := newPublisher(s.connection, c.Exchange, func(ctx context.Context, reason error) {
		c.Logger.Infof("Publish retrying: %v", reason)
	})
	if err != nil {
		return errors.WithMessage(err, "create publisher")
	}
	s.currentPublisher = &loggingPublisher{
		publisher: rawPublisher,
		logger:    c.Logger,
	}

	// listen for connection failures on the specific connection along with
	// closing the connection if general shutdown is signalled
	go func() {
		c.Logger.Info("Connection close listener started")
		defer c.Logger.Info("Connection close listener stopped")
		select {
		case <-s.shutdown:
			if s.connection.IsClosed() {
				return
			}
			err := consumer.Close()
			if err != nil {
				c.Logger.Errorf("Failed to close consumer: %v", err)
			}
			err = s.currentPublisher.Close()
			if err != nil {
				c.Logger.Errorf("Failed to close publisher: %v", err)
			}
			err = s.connection.Close()
			if err != nil {
				c.Logger.Errorf("Failed to close amqp connection: %v", err)
			}
		case err, abnormalShutdown := <-s.connectionClosed:
			if !abnormalShutdown {
				c.Logger.Info("Connection closed due to normal shutdown")
				return
			}
			c.Logger.Info("Connection closed due to abnormal shutdown")
			// signal the worker that the connection was lost
			s.connectionClosed <- err
		}
	}()
	go func() {
		select {
		case <-s.shutdown:
		case s.currentConsumer <- consumer:
		}
	}()
	s.config.Logger.Info("Connected to AMQP successfully")
	return nil
}

func (s *Worker) declareExchange() error {
	exchangeChannel, err := s.connection.Channel()
	if err != nil {
		return errors.WithMessage(err, "open channel for exchange declaration")
	}
	err = exchangeChannel.ExchangeDeclare(
		s.config.Exchange,
		"topic", // kind
		true,    // durable
		false,   // autoDelete
		false,   // internal
		false,   // noWait
		nil,     // args
	)
	if err != nil {
		return errors.WithMessage(err, "declare exchange")
	}
	return nil
}

func (s *Worker) Close() error {
	close(s.shutdown)
	return nil
}

// reconnect attempts to reconnect to AMQP with the configured reconnection
// timeout between attempts.
func (s *Worker) reconnect() {
	for reconnectCount := 1; ; reconnectCount++ {
		s.config.Logger.Infof("Reconnecting to AMQP after connection closed: attempt %d", reconnectCount)
		err := s.connect()
		if err != nil {
			s.config.Logger.Infof("Failed to reconnect to AMQP: %v", err)
			time.Sleep(s.config.ReconnectionTimeout)
			continue
		}
		s.config.Logger.Info("Successfully reconnected to AMQP")
		return
	}
}

// StartConsumer starts the consumer on the worker. The method is blocking and
// will only return if the worker is stopped with Close.
func (s *Worker) StartConsumer(handlers map[string]func([]byte) error) error {
	for {
		select {
		// worker is instructed to shutdown from a Close call
		case <-s.shutdown:
			return broker.ErrBrokerClosed

		// worker has a new connection that can be used to consume messages with the handler
		case conn := <-s.currentConsumer:
			// this call is blocking as long as the connection is available.
			err := conn.Start(s.config.Logger, handlers)
			if err != nil {
				return err
			}
		}
	}
}

// Publish publishes a message on a configured AMQP exchange.
func (s *Worker) Publish(ctx context.Context, event broker.Publishable) error {
	uuid, err := uuid.NewRandom()
	if err != nil {
		s.config.Logger.Errorf("Failed to create a random message ID. Continue execution: %v", err)
	}
	body, err := event.Marshal()
	if err != nil {
		return errors.WithMessage(err, "get message body")
	}
	err = s.currentPublisher.Publish(ctx, event.Type(), uuid.String(), body)
	if err != nil {
		return err
	}
	return nil
}
