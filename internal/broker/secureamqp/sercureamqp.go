package secureamqp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lunarway/release-manager/internal/broker"
	"github.com/lunarway/release-manager/internal/log"
	"github.com/lunarway/release-manager/internal/tracing"
	"github.com/streadway/amqp"
)

type Session struct {
	config          Config
	logger          *log.Logger
	connection      *amqp.Connection
	channel         *amqp.Channel
	done            chan bool
	notifyConnClose chan *amqp.Error
	notifyChanClose chan *amqp.Error
	notifyConfirm   chan amqp.Confirmation
	isReady         bool
}

const (
	// When reconnecting to the server after connection failure
	reconnectDelay = 5 * time.Second

	// When setting up the channel after a channel exception
	reInitDelay = 2 * time.Second

	// When resending messages the server didn't confirm
	resendDelay = 5 * time.Second
)

var (
	errNotConnected  = errors.New("not connected to a server")
	errAlreadyClosed = errors.New("already closed: not connected to the server")
	errShutdown      = errors.New("session is shutting down")
)

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

// New creates a new consumer state instance, and automatically
// attempts to connect to the server.
func New(c Config) *Session {
	session := Session{
		logger: c.Logger.With("system", "secureamqp"),
		config: c,
		done:   make(chan bool),
	}
	go session.handleReconnect(c.Connection.Host)
	return &session
}

// handleReconnect will wait for a connection error on
// notifyConnClose, and then continuously attempt to reconnect.
func (session *Session) handleReconnect(addr string) {
	for {
		session.isReady = false
		session.logger.Info("Attempting to connect")

		conn, err := session.connect(addr)

		if err != nil {
			session.logger.Info("Failed to connect. Retrying...")

			select {
			case <-session.done:
				return
			case <-time.After(reconnectDelay):
			}
			continue
		}

		if done := session.handleReInit(conn); done {
			break
		}
	}
}

// connect will create a new AMQP connection
func (session *Session) connect(addr string) (*amqp.Connection, error) {
	conn, err := amqp.Dial(session.config.Connection.Raw())

	if err != nil {
		return nil, err
	}

	session.changeConnection(conn)
	session.logger.Info("Connected!")
	return conn, nil
}

// handleReconnect will wait for a channel error
// and then continuously attempt to re-initialize both channels
func (session *Session) handleReInit(conn *amqp.Connection) bool {
	for {
		session.isReady = false

		err := session.init(conn)

		if err != nil {
			session.logger.Info("Failed to initialize channel. Retrying...")

			select {
			case <-session.done:
				return true
			case <-time.After(reInitDelay):
			}
			continue
		}

		select {
		case <-session.done:
			return true
		case <-session.notifyConnClose:
			session.logger.Info("Connection closed. Reconnecting...")
			return false
		case <-session.notifyChanClose:
			session.logger.Info("Channel closed. Re-running init...")
		}
	}
}

// init will initialize channel & declare queue
func (session *Session) init(conn *amqp.Connection) error {
	ch, err := conn.Channel()

	if err != nil {
		return err
	}

	err = ch.Confirm(false)

	if err != nil {
		return err
	}
	_, err = ch.QueueDeclare(
		session.config.Queue,
		false, // Durable
		false, // Delete when unused
		false, // Exclusive
		false, // No-wait
		nil,   // Arguments
	)

	if err != nil {
		return err
	}

	session.changeChannel(ch)
	session.isReady = true
	session.logger.Info("Setup!")

	return nil
}

// changeConnection takes a new connection to the queue,
// and updates the close listener to reflect this.
func (session *Session) changeConnection(connection *amqp.Connection) {
	session.connection = connection
	session.notifyConnClose = make(chan *amqp.Error)
	session.connection.NotifyClose(session.notifyConnClose)
}

// changeChannel takes a new channel to the queue,
// and updates the channel listeners to reflect this.
func (session *Session) changeChannel(channel *amqp.Channel) {
	session.channel = channel
	session.notifyChanClose = make(chan *amqp.Error)
	session.notifyConfirm = make(chan amqp.Confirmation, 1)
	session.channel.NotifyClose(session.notifyChanClose)
	session.channel.NotifyPublish(session.notifyConfirm)
}

func (session *Session) Publish(ctx context.Context, message broker.Publishable) error {
	data, err := message.Marshal()
	if err != nil {
		return fmt.Errorf("marshall message: %w", err)
	}
	uuid, err := uuid.NewRandom()
	if err != nil {
		session.logger.Errorf("Failed to create a random message ID. Continue execution: %v", err)
	}
	return session.push(amqp.Publishing{
		Type:          message.Type(),
		ContentType:   "application/json",
		Body:          data,
		MessageId:     uuid.String(),
		CorrelationId: tracing.RequestIDFromContext(ctx),
	})
}

// push will push data onto the queue, and wait for a confirm.
// If no confirms are received until within the resendTimeout,
// it continuously re-sends messages until a confirm is received.
// This will block until the server sends a confirm. Errors are
// only returned if the push action itself fails, see UnsafePush.
func (session *Session) push(p amqp.Publishing) error {
	if !session.isReady {
		return errors.New("failed to push: not connected")
	}
	for {
		err := session.channel.Publish(
			session.config.Exchange,   // Exchange
			session.config.RoutingKey, // Routing key
			false,                     // Mandatory
			false,                     // Immediate
			p,
		)
		if err != nil {
			session.logger.Info("Push failed. Retrying...")
			select {
			case <-session.done:
				return errShutdown
			case <-time.After(resendDelay):
			}
			continue
		}
		select {
		case confirm := <-session.notifyConfirm:
			if confirm.Ack {
				session.logger.Info("Push confirmed!")
				return nil
			}
		case <-time.After(resendDelay):
		}
		session.logger.Info("Push didn't confirm. Retrying...")
	}
}

// StartConsumer starts the consumer on the worker. The method is blocking and
// will only return if the worker is stopped with Close.
func (session *Session) StartConsumer(handlers map[string]func([]byte) error) error {
	msgs, err := session.stream()
	if err != nil {
		return fmt.Errorf("get message stream: %w", err)
	}
	for msg := range msgs {
		logger := session.logger.With(
			"routingKey", msg.RoutingKey,
			"messageId", msg.MessageId,
			"eventType", msg.Type,
			"correlationId", msg.CorrelationId,
			"headers", fmt.Sprintf("%#v", msg.Headers),
		)
		logger.Infof("Received message type=%s from exchange=%s routingKey=%s messageId=%s correlationId=%s timestamp=%s", msg.Type, msg.Exchange, msg.RoutingKey, msg.MessageId, msg.CorrelationId, msg.Timestamp)
		now := time.Now()

		handler, ok := handlers[msg.Type]
		if !ok {
			logger.With("res", map[string]interface{}{
				"status": "failed",
				"error":  "unprocessable",
			}).Errorf("[consumer] [UNPROCESSABLE] Failed to handle message: no handler registered for event type '%s': dropping it", msg.Type)
			err := msg.Nack(false, false)
			if err != nil {
				logger.Errorf("Failed to nack message: %v", err)
			}
			continue
		}
		err := handler(msg.Body)
		duration := time.Since(now).Milliseconds()
		if err != nil {
			logger.With("res", map[string]interface{}{
				"status":       "failed",
				"responseTime": duration,
				"error":        fmt.Sprintf("%+v", err),
			}).Errorf("[consumer] [FAILED] Failed to handle message: nacking and requeing: %v", err)
			// TODO: remove comments to allow for redelivery. This will put events
			// into the unacknowledged state

			// err := msg.Nack(false, true) if err != nil {
			//  logger.WithFields("error", fmt.Sprintf("%+v", err)).Errorf("Failed to nack message: %v", err)
			// }
			continue
		}
		logger.With("res", map[string]interface{}{
			"status":       "ok",
			"responseTime": duration,
		}).Info("[OK] Event handled successfully")
		err = msg.Ack(false)
		if err != nil {
			logger.Errorf("Failed to ack message: %v", err)
		}
	}
	return nil
}

// stream will continuously put queue items on the channel.
// It is required to call delivery.Ack when it has been
// successfully processed, or delivery.Nack when it fails.
// Ignoring this will cause data to build up on the server.
func (session *Session) stream() (<-chan amqp.Delivery, error) {
	if !session.isReady {
		return nil, errNotConnected
	}
	return session.channel.Consume(
		session.config.Queue, // Queue
		"release-manager",    // Consumer
		false,                // Auto-Ack
		false,                // Exclusive
		false,                // No-local
		false,                // No-Wait
		nil,                  // Args
	)
}

// Close will cleanly shutdown the channel and connection.
func (session *Session) Close() error {
	if !session.isReady {
		return errAlreadyClosed
	}
	err := session.channel.Close()
	if err != nil {
		return err
	}
	err = session.connection.Close()
	if err != nil {
		return err
	}
	close(session.done)
	session.isReady = false
	return nil
}
