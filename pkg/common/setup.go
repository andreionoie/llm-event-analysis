package common

import (
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func InitSlog() {
	levelStr := GetenvOrDefault("LOG_LEVEL", "info")

	var level slog.Level
	switch strings.ToLower(levelStr) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))

	slog.SetDefault(logger)
}

func RequireEnv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		slog.Error("required environment variable not set", "key", key)
		os.Exit(1)
	}
	return value
}

func GetenvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	slog.Warn("environment variable not set, will fallback to default", "key", key, "default", defaultValue)
	return defaultValue
}

func SetupEchoDefaults(e *echo.Echo, healthHandler echo.HandlerFunc, readyHandler echo.HandlerFunc) {
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
			//if v.URI == "/healthz" || v.URI == "/readyz" || v.URI == "/metrics" {
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

	e.GET("/healthz", healthHandler)
	e.GET("/readyz", readyHandler)
	e.GET("/metrics", echoprometheus.NewHandler())
}
