package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type Server struct {
	server *http.Server
	logger *slog.Logger
	port   int
}

func NewServer(port int, m *Metrics, logger *slog.Logger) *Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", m.Handler())

	return &Server{
		server: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		logger: logger,
		port:   port,
	}
}

func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("Starting metrics server", slog.Int("port", s.port))

	errCh := make(chan error, 1)
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("metrics server failed: %w", err)
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("Stopping metrics server")
	return s.server.Shutdown(ctx)
}
