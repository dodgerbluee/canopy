package platform

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

func NewLogger(cfg Config) *slog.Logger {
	var lvl slog.Level
	switch cfg.LogLevel {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

// Readiness reports whether the process can serve traffic (dependencies up).
type Readiness func(ctx context.Context) error

// NewMux wires liveness and readiness distinctly: /healthz answers as long
// as the process is alive; /readyz only answers 200 when dependencies pass.
func NewMux(ready Readiness, log *slog.Logger) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"alive"}`))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := ready(ctx); err != nil {
			log.Warn("not ready", "err", err)
			WriteProblem(w, r, log, http.StatusServiceUnavailable, "NOT_READY", "Service not ready")
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, r, log, http.StatusNotFound, "NOT_FOUND", "Route not found")
	})
	return mux
}

// Server adds bounded graceful shutdown to an http.Server.
type Server struct {
	srv *http.Server
	// ShutdownTimeout bounds in-flight request completion.
	ShutdownTimeout time.Duration
	Log             *slog.Logger
}

func NewServer(cfg Config, handler http.Handler, log *slog.Logger) *Server {
	return &Server{
		srv: &http.Server{
			Addr:         cfg.Addr,
			Handler:      handler,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
			IdleTimeout:  cfg.IdleTimeout,
		},
		ShutdownTimeout: cfg.ShutdownTimeout,
		Log:             log,
	}
}

func (s *Server) ListenAndServe() error {
	if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Serve(ln net.Listener) error {
	if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Run serves until ctx is cancelled, then shuts down within the timeout.
func (s *Server) Run(ctx context.Context, ln func() error) error {
	errCh := make(chan error, 1)
	go func() { errCh <- ln() }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.Log.Info("shutting down", "timeout", s.ShutdownTimeout)
		sctx, cancel := context.WithTimeout(context.Background(), s.ShutdownTimeout)
		defer cancel()
		return s.srv.Shutdown(sctx)
	}
}
