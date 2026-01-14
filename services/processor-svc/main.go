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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/twmb/franz-go/pkg/kgo"
)

type Config struct {
	Port               string
	KafkaBrokers       []string
	KafkaTopic         string
	KafkaConsumerGroup string
	DatabaseURL        string
}

func loadConfig() Config {
	return Config{
		Port:               common.GetenvOrDefault("PORT", "8080"),
		KafkaBrokers:       common.SplitCommaSeparated(common.RequireEnv("KAFKA_BROKERS")),
		KafkaTopic:         common.RequireEnv("KAFKA_TOPIC"),
		KafkaConsumerGroup: common.GetenvOrDefault("KAFKA_CONSUMER_GROUP", "processor-svc"),
		DatabaseURL:        common.RequireEnv("DATABASE_URL"),
	}
}

// Server state
type Server struct {
	cfg      Config
	ready    atomic.Bool
	consumer *kgo.Client
	db       *pgxpool.Pool
}

func main() {
	logLevel := common.InitSlog()

	s := &Server{
		cfg: loadConfig(),
	}
	db, err := connectDBWithRetry(context.Background(), s.cfg.DatabaseURL, 10, 3*time.Second)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := ensureSchema(context.Background(), db); err != nil {
		slog.Error("failed to ensure database schema", "error", err)
		os.Exit(1)
	}
	s.db = db

	kafkaLogLevel := common.KgoLogLevelFromString(logLevel)
	consumer, err := kgo.NewClient(
		kgo.SeedBrokers(s.cfg.KafkaBrokers...),
		kgo.WithLogger(common.NewKgoSlogLogger(slog.Default().With("component", "kafka"), kafkaLogLevel)),
		kgo.ConsumerGroup(s.cfg.KafkaConsumerGroup),
		kgo.ConsumeTopics(s.cfg.KafkaTopic),
		kgo.DisableAutoCommit(),
		// smarter readiness checks than a basic brokers ping
		kgo.OnPartitionsAssigned(func(ctx context.Context, cl *kgo.Client, assigned map[string][]int32) {
			if s.ready.CompareAndSwap(false, true) {
				slog.Info("consumer partitions assigned", "assignments", assigned)
			}
		}),
		kgo.OnPartitionsRevoked(func(ctx context.Context, cl *kgo.Client, revoked map[string][]int32) {
			if s.ready.CompareAndSwap(true, false) {
				slog.Info("consumer partitions revoked", "assignments", revoked)
			}
		}),
		kgo.OnPartitionsLost(func(ctx context.Context, cl *kgo.Client, lost map[string][]int32) {
			if s.ready.CompareAndSwap(true, false) {
				slog.Warn("consumer partitions lost", "assignments", lost)
			}
		}),
	)
	if err != nil {
		slog.Error("failed to create kafka client", "error", err)
		os.Exit(1)
	}
	defer consumer.Close()

	s.consumer = consumer
	kafkaCtx, kafkaCancel := context.WithCancel(context.Background())
	go s.consume(kafkaCtx)

	e := echo.New()
	common.SetupEchoDefaults(e, "processor-svc", s.handleHealth, s.handleReady)

	echoErrChan := make(chan error, 1)
	go func() {
		slog.Info("starting processor service", "port", s.cfg.Port)
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
	kafkaCancel()
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
