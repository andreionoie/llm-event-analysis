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
	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type Config struct {
	Port string
}

func loadConfig() Config {
	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	return Config{
		Port: port,
	}
}

// Server state
type Server struct {
	cfg   Config
	ready atomic.Bool
}

func main() {
	common.InitSlog(os.Getenv("LOG_LEVEL"))

	s := &Server{}
	s.cfg = loadConfig()

	e := echo.New()
	e.Server.ReadHeaderTimeout = 2 * time.Second
	e.Server.ReadTimeout = 5 * time.Second
	e.Server.WriteTimeout = 10 * time.Second
	e.Server.IdleTimeout = 2 * time.Minute
	e.Use(middleware.Recover())
	e.Use(middleware.BodyLimit("16M"))
	e.Use(echoprometheus.NewMiddleware("ingest_service"))
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogStatus:   true,
		LogURI:      true,
		LogError:    true,
		LogMethod:   true,
		LogLatency:  true,
		HandleError: true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			//if v.URI == "/healthz" || v.URI == "/readyz" {
			//	return nil
			//}

			if v.Error != nil {
				slog.Error("request", "method", v.Method, "uri", v.URI, "status", v.Status, "latency", v.Latency, "error", v.Error)
			} else {
				slog.Info("request", "method", v.Method, "uri", v.URI, "status", v.Status, "latency", v.Latency)
			}
			return nil
		},
	}))

	e.GET("/healthz", s.handleHealth)
	e.GET("/readyz", s.handleReady)
	e.GET("/metrics", echoprometheus.NewHandler())

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
