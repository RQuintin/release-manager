package instrumentation

import (
	"fmt"
	"time"

	"github.com/lunarway/release-manager/internal/log"
)

func (in *Instrumentor) ObserveGitClone(depth int) func() {
	start := time.Now()
	log.Infof("Starting observe at depth %d", depth)
	return func() {
		duration := time.Since(start)
		milliseconds := duration.Nanoseconds() / 1000000
		log.Infof("Ended observe at depth %d to %s (%v)", depth, duration, milliseconds)
		in.gitHistogram.WithLabelValues(fmt.Sprintf("clone_%d", depth)).Observe(float64(milliseconds))
	}
}
