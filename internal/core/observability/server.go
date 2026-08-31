package observability

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

type Server struct {
	config   Config
	registry *prometheus.Registry
	log      *logger.Logger
}

func NewServer(config Config, registry *prometheus.Registry, log *logger.Logger) (*Server, error) {
	if registry == nil || log == nil {
		return nil, fmt.Errorf("initialize metrics server: registry and logger are required")
	}
	return &Server{config: config, registry: registry, log: log}, nil
}

func (server *Server) Run(ctx context.Context) error {
	if !server.config.Enabled {
		server.log.Info("Prometheus metrics listener disabled")
		<-ctx.Done()
		return nil
	}
	mux := http.NewServeMux()
	handler := promhttp.HandlerFor(server.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
	mux.Handle("GET /metrics", promhttp.InstrumentMetricHandler(server.registry, handler))
	httpServer := &http.Server{
		Addr:              server.config.Addr,
		Handler:           mux,
		ReadHeaderTimeout: server.config.ReadHeaderTimeout,
	}
	errorsChannel := make(chan error, 1)
	go func() {
		server.log.Info("Prometheus metrics listener started", zap.String("address", server.config.Addr))
		if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errorsChannel <- err
		}
	}()
	select {
	case err := <-errorsChannel:
		return fmt.Errorf("serve Prometheus metrics: %w", err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), server.config.ShutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			_ = httpServer.Close()
			return fmt.Errorf("shutdown Prometheus metrics server: %w", err)
		}
		return nil
	}
}
