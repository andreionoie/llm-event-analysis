package common

import (
	"log/slog"
	"os"
	"strconv"
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

func GetenvOrDefaultInt(key, defaultValue string) int {
	strValue := GetenvOrDefault(key, defaultValue)
	out, err := strconv.Atoi(strValue)
	if err != nil {
		slog.Error("invalid environment variable value", "key", key, "value", strValue, "error", err)
	}
	return out
}

func SetupEchoDefaults(e *echo.Echo, subsystem string, healthHandler echo.HandlerFunc, readyHandler echo.HandlerFunc) {
	e.Server.ReadHeaderTimeout = time.Second * time.Duration(
		GetenvOrDefaultInt("READ_HEADER_TIMEOUT_SECONDS", "2"))
	e.Server.ReadTimeout = time.Second * time.Duration(
		GetenvOrDefaultInt("READ_TIMEOUT_SECONDS", "5"))
	e.Server.WriteTimeout = time.Second * time.Duration(
		GetenvOrDefaultInt("WRITE_TIMEOUT_SECONDS", "10"))
	e.Server.IdleTimeout = time.Second * time.Duration(
		GetenvOrDefaultInt("IDLE_TIMEOUT_SECONDS", "120"))

	e.Use(middleware.Recover())
	e.Use(middleware.BodyLimit(GetenvOrDefault("MAX_REQUEST_SIZE_MB", "64") + "MB"))
	e.Use(echoprometheus.NewMiddleware(subsystem))
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
