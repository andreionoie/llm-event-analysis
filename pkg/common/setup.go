package common

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func InitSlog() string {
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
	return levelStr
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

func SplitCommaSeparated(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return []string{raw}
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
			if v.URI == "/healthz" || v.URI == "/readyz" || v.URI == "/metrics" {
				return nil
			}

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

func StartKafkaHealthCheck(ctx context.Context, kafkaClient *kgo.Client, ready *atomic.Bool) {
	check := func() {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		err := kafkaClient.Ping(pingCtx)
		if err != nil {
			if ready.CompareAndSwap(true, false) {
				slog.Warn("kafka not reachable", "error", err, "brokers", getBrokers(pingCtx, kafkaClient))
			}
		} else {
			if ready.CompareAndSwap(false, true) {

				slog.Info("kafka connection established", "brokers", getBrokers(pingCtx, kafkaClient))
			}
		}
	}

	check()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

func getBrokers(ctx context.Context, kafkaClient *kgo.Client) []string {
	req := kmsg.NewMetadataRequest()
	md, mdErr := kafkaClient.RequestCachedMetadata(ctx, &req, 0)

	var brokerList []string
	if mdErr == nil {
		for _, b := range md.Brokers {
			addr := net.JoinHostPort(b.Host, strconv.Itoa(int(b.Port)))
			brokerList = append(brokerList, addr)
		}
	}

	return brokerList
}
