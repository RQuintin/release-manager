package broker

import (
	"context"
	"fmt"
	"time"

	"github.com/lunarway/release-manager/internal/log"
)

// type Logger interface {
// 	With(...interface{}) Logger
// 	Errorf(string, ...interface{})
// 	Infof(string, ...interface{})
// }

func LoggingHandlers(log *log.Logger, handlers Handlers) Handlers {
	for k, h := range handlers {
		handlers[k] = HandleFunc(func(ctx context.Context, m Message) error {
			logger := log.With(
				"eventType", m.Type(),
			)
			now := time.Now()

			err := h.Handle(ctx, m)

			duration := time.Since(now)
			if err != nil {
				logger.With("res", map[string]interface{}{
					"status":       "failed",
					"responseTime": duration,
					"error":        fmt.Sprintf("%+v", err),
				}).Errorf("[FAILED] Failed to handle message: dropping it: %v", err)
				return err
			}
			logger.With("res", map[string]interface{}{
				"status":       "ok",
				"responseTime": duration,
			}).Info("[OK] Event handled successfully")
			return nil
		})
	}
	return handlers
}
