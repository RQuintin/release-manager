package instrumentation

import (
	"net/http"

	"github.com/lunarway/release-manager/internal/log"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Instrumentor struct {
	registry     *prometheus.Registry
	gitHistogram *prometheus.HistogramVec
}

func NewInstrumentor() (*Instrumentor, error) {
	in := Instrumentor{
		registry: prometheus.NewRegistry(),
		gitHistogram: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "release_manager_git_duration_milliseconds",
			Help: "Git request durations in milliseconds",
			Buckets: []float64{
				5, 10, 50, 75,
				100, 200, 300, 400, 500, 600, 700, 800, 900,
				1000, 2000, 4000, 8000, 10000, 20000, 40000, 80000, 100000,
			}, // 5 ms to 100 seconds
		}, []string{"operation"}),
	}

	err := in.registry.Register(in.gitHistogram)
	if err != nil {
		return nil, errors.WithMessage(err, "registor git histogram")
	}
	return &in, nil
}

// HTTPHandler returns an http.Handler that writes current metric values
func (in *Instrumentor) HTTPHandler() http.Handler {
	return promhttp.InstrumentMetricHandler(
		in.registry, promhttp.HandlerFor(in.registry, promhttp.HandlerOpts{
			ErrorLog: &promLogger{},
		}),
	)
}

type promLogger struct{}

func (l *promLogger) Println(v ...interface{}) {
	log.Error(v...)
}
