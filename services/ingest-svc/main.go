package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
	"github.com/labstack/echo/v4"
)

type Config struct {
	Port string
}

func loadConfig() Config {
	return Config{
		Port: common.GetenvOrDefault("PORT", "8080"),
	}
}

// Server state
type Server struct {
	cfg   Config
	ready atomic.Bool
}

func main() {
	common.InitSlog()

	s := &Server{
		cfg: loadConfig(),
	}

	e := echo.New()
	common.SetupEchoDefaults(e, s.handleHealth, s.handleReady)
	echoErrChan := make(chan error, 1)
	go func() {
		slog.Info("starting ingest service", "port", s.cfg.Port)
		s.ready.Store(true)
		if err := e.Start(":" + s.cfg.Port); err != nil && !errors.Is(err, http.ErrServerClosed) {
			echoErrChan <- err
		}
	}()

	// graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-quit:
		slog.Info("shutting down")
	case err := <-echoErrChan:
		slog.Error("echo failed to start", "error", err)
		os.Exit(1)
	}

	s.ready.Store(false)
	time.Sleep(5 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := e.Shutdown(ctx); err != nil {
		slog.Error("echo shutdown error", "error", err)
	}
	slog.Info("shutdown complete")
}

func (s *Server) handleHealth(c echo.Context) error {
	return c.NoContent(http.StatusOK)
}

func (s *Server) handleReady(c echo.Context) error {
	if !s.ready.Load() {
		return c.String(http.StatusServiceUnavailable, "not ready")
	}

	return c.NoContent(http.StatusOK)
}
