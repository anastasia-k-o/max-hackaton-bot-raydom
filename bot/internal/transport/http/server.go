package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// Server wraps http.Server with timeouts and graceful shutdown.
type Server struct {
	http     *http.Server
	logger   *slog.Logger
	listener net.Listener
}

// ServerConfig configures the listener.
type ServerConfig struct {
	Addr            string
	Handler         http.Handler
	Logger          *slog.Logger
	ShutdownTimeout time.Duration
}

// NewServer builds the HTTP server.
//
// Every timeout is set explicitly. A server with no ReadTimeout will happily
// hold a connection open forever, which is how a slow-loris turns a bot into
// an outage; defaults are worth stating even in a hackathon project.
func NewServer(cfg ServerConfig) *Server {
	return &Server{
		http: &http.Server{
			Addr:    cfg.Addr,
			Handler: cfg.Handler,
			// Generous enough for MAX webhooks, tight enough to reclaim
			// stuck connections.
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    1 << 20,
		},
		logger: cfg.Logger,
	}
}

// Listen binds the port without serving yet.
//
// Splitting binding from serving exists for one reason: the long-polling loop
// must not start before we know the process can actually run. Polling has a
// side effect on MAX's side — fetching a batch and passing the marker back
// COMMITS those events — so a poller that starts, takes a batch and then dies
// on a busy port would consume events nobody handled. Binding first turns that
// into a clean refusal to start.
//
// Calling Listen is optional: Run binds on its own if it was not called.
func (s *Server) Listen() error {
	if s.listener != nil {
		return nil
	}

	// Bind before logging. ListenAndServe would let the process announce
	// "listening" and only then fail on a busy port, which reads as a server
	// that started and then died — the opposite of what happened.
	listener, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("cannot bind %s: %w\n"+
			"  Порт занят другим процессом. Посмотреть кем:  ss -ltnp | grep %s\n"+
			"  Либо укажите свободный порт в .env: HTTP_PORT=8081",
			s.http.Addr, err, strings.TrimPrefix(s.http.Addr, ":"))
	}
	s.listener = listener
	return nil
}

// Run serves until ctx is cancelled, then shuts down gracefully.
//
// In-flight webhooks are allowed to finish: dropping a confirmation mid-flight
// would leave the user staring at a spinning button.
func (s *Server) Run(ctx context.Context, shutdownTimeout time.Duration) error {
	if err := s.Listen(); err != nil {
		return err
	}
	listener := s.listener

	errCh := make(chan error, 1)

	go func() {
		s.logger.Info("http server listening", "addr", listener.Addr().String())
		if err := s.http.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		if shutdownTimeout <= 0 {
			shutdownTimeout = 10 * time.Second
		}
		s.logger.Info("http server shutting down", "timeout", shutdownTimeout.String())

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := s.http.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("http shutdown: %w", err)
		}
		return nil
	}
}
