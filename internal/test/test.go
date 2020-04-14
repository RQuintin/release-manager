package test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/lunarway/release-manager/internal/broker"
)

// RabbitMQIntegration skips the test if no RabbitMQ integration test host name
// is available as an environment variable. If it is available its value is
// returned.
func RabbitMQIntegration(t *testing.T) string {
	host := os.Getenv("RELEASE_MANAGER_INTEGRATION_RABBITMQ_HOST")
	if host == "" {
		t.Skip("RabbitMQ integration tests not enabled as RELEASE_MANAGER_INTEGRATION_RABBITMQ_HOST is empty")
	}
	return host
}

var _ broker.Publishable = &Event{}

type Event struct {
	Message string `json:"message"`
}

func (Event) Type() string {
	return "test-event"
}

func (t Event) Marshal() ([]byte, error) {
	return json.Marshal(t)
}

func (t *Event) Unmarshal(d []byte) error {
	return json.Unmarshal(d, t)
}
